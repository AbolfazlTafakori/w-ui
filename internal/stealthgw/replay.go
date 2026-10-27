package stealthgw

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The replay rule.
//
// An opening that was seen once must never start a session again, from any
// address, even across a restart. Within one run that is a cache of the salts
// accepted inside the window. Across a restart it is a limit kept on disk:
// the server accepts no timestamp above the limit it has written, and after a
// start it accepts none at or below it. Whatever the last run accepted had a
// timestamp no higher than its limit, so a restart can never take it again,
// and no record of the cache has to survive.
//
// The limit runs a little ahead of the clock and is raised before it is
// reached, each raise written and synced before a timestamp above the old
// limit is taken. The cost is a short pause after a restart: an honest client
// is refused until the clock passes the floor, at most limitStep and the time
// the restart took.

// Replay constants, in seconds but for the cache.
const (
	// window is how far an opening's timestamp may be from the server's
	// clock, either way.
	window = 60
	// limitStep is how far above the clock, or above a timestamp from a
	// client whose clock runs ahead, a raised limit lies.
	limitStep = 20
	// limitAhead is how close the clock may come to the limit before it is
	// raised.
	limitAhead = 10
	// cacheMax bounds the salts the cache holds; past it every opening is
	// refused until entries leave.
	cacheMax = 262_144
)

// verdict is what the rule says of one opening.
type verdict int

const (
	accepted verdict = iota
	outsideWindow
	atFloor
	seenBefore
	cacheFull
	aboveLimit
)

func (v verdict) String() string {
	return [...]string{"accepted", "window", "floor", "seen", "cache full", "above limit"}[v]
}

// replayFile is where the limit is kept.
//
//	"WUIRPLY1" | limit (8, big-endian) | SHA-256 of the 16 bytes before (32)
const replayMagic = "WUIRPLY1"

// replay is the rule and its file. It is safe for concurrent use.
type replay struct {
	path string

	mu       sync.Mutex
	floor    uint64 // nothing at or below is accepted
	limit    uint64 // on disk: nothing above is accepted
	writing  bool   // a raise is being written
	salts    map[[32]byte]uint64
	oldest   uint64 // no entry is older than this
	failures int    // writes that failed in a row, for the log
}

// errNoLimit is a file that is missing or cannot be read.
var errNoLimit = errors.New("stealthgw: no usable replay limit")

// openReplay starts the rule from the file at path at the clock now. A file
// that is missing or damaged leaves nothing to go by, so the floor is put
// past anything a lost file could have covered: the clock plus the window.
// Nothing is accepted until a raise above it is written.
func openReplay(path string, now uint64) (*replay, error) {
	r := &replay{path: path, salts: make(map[[32]byte]uint64)}
	limit, err := readLimit(path)
	if err != nil {
		r.floor, r.limit = now+window, now+window
		return r, err
	}
	r.floor, r.limit = limit, limit
	return r, nil
}

func readLimit(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errNoLimit, err)
	}
	if len(b) != 48 || string(b[:8]) != replayMagic {
		return 0, fmt.Errorf("%w: not a replay file", errNoLimit)
	}
	sum := sha256.Sum256(b[:16])
	if !bytes.Equal(sum[:], b[16:]) {
		return 0, fmt.Errorf("%w: damaged", errNoLimit)
	}
	return binary.BigEndian.Uint64(b[8:16]), nil
}

// writeLimit puts limit on disk: a temporary file, synced, renamed over the
// old one, and the directory synced, so that the limit is either the old one
// or the new one after any crash, never neither.
func writeLimit(path string, limit uint64) error {
	b := make([]byte, 0, 48)
	b = append(b, replayMagic...)
	b = binary.BigEndian.AppendUint64(b, limit)
	sum := sha256.Sum256(b)
	b = append(b, sum[:]...)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".replay-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // gone after the rename; left by a failure
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir makes a rename durable where the system allows a directory to be
// synced. Where it does not, there is nothing more to do.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// judge says what the rule makes of an opening, and records nothing: the
// caller's limits decide that, and record spends the salt. raise is the
// limit to write for an opening above the limit, else zero. Only the
// gateway's reader judges and records, so nothing comes between the two.
func (r *replay) judge(salt [32]byte, ts, now uint64) (v verdict, raise uint64) {
	if ts+window < now || ts > now+window {
		return outsideWindow, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ts <= r.floor {
		return atFloor, 0
	}
	if _, ok := r.salts[salt]; ok {
		return seenBefore, 0
	}
	if len(r.salts) >= cacheMax {
		r.expireLocked(now)
		if len(r.salts) >= cacheMax {
			return cacheFull, 0
		}
	}
	if ts > r.limit {
		return aboveLimit, ts + limitStep
	}
	return accepted, 0
}

// record spends a salt judge passed. An opening that opens a session is
// recorded; so is one a limit dropped, while its device's allowance for that
// lasts, since left unrecorded it would be good later for whoever saw it.
func (r *replay) record(salt [32]byte, ts uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.salts) >= cacheMax {
		return
	}
	if len(r.salts) == 0 || ts < r.oldest {
		r.oldest = ts
	}
	r.salts[salt] = ts
}

// expire lets go of salts whose timestamps the window no longer admits.
func (r *replay) expire(now uint64) {
	r.mu.Lock()
	r.expireLocked(now)
	r.mu.Unlock()
}

func (r *replay) expireLocked(now uint64) {
	if len(r.salts) == 0 || r.oldest+window >= now {
		return
	}
	cut, oldest := now-window, now
	for s, ts := range r.salts {
		if ts < cut {
			delete(r.salts, s)
		} else if ts < oldest {
			oldest = ts
		}
	}
	r.oldest = oldest
}

// due is the limit to write now, or zero: the clock has come within
// limitAhead of the limit, or a client asked for more, and no write is
// running. The caller writes it with write and nothing else writes meanwhile.
func (r *replay) due(now, asked uint64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writing {
		return 0
	}
	want := uint64(0)
	if r.limit < now+limitAhead {
		want = now + limitStep
	}
	if asked > r.limit {
		want = max(want, min(asked, now+window+limitStep))
	}
	if want <= r.limit {
		return 0
	}
	r.writing = true
	return want
}

// write puts a limit due gave on disk and, once it is there, in force.
func (r *replay) write(limit uint64) error {
	err := writeLimit(r.path, limit)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writing = false
	if err != nil {
		r.failures++
		return err
	}
	r.failures = 0
	r.limit = max(r.limit, limit)
	return nil
}

// state is the floor and the limit, for the gateway's figures and its log.
func (r *replay) state() (floor, limit uint64, salts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.floor, r.limit, len(r.salts)
}
