package stealthgw

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// A session's states.
const (
	statePending int32 = iota // opened, WireGuard has not spoken yet
	stateLive                 // the one its device is answered on
	stateRetired              // replaced, and only still read
	stateOver
)

// session is one opening's two keys.
type session struct {
	recvMu sync.Mutex
	recv   *stealth.UDPReceiver

	sendMu   sync.Mutex
	send     *stealth.UDPSender
	budget   int // the opening's size: the first answer is no larger
	answered bool

	state      atomic.Int32
	opened     time.Time
	handshakes bucket // the gateway's lock
	echoes     atomic.Int32
	sent       atomic.Uint64
}

func newSession(keys stealth.UDPKeys, pad stealth.Padder, budget int, now time.Time) *session {
	return &session{
		recv: stealth.NewUDPReceiver(keys.Up), send: stealth.NewUDPSender(keys.Down, pad),
		budget: budget, opened: now, handshakes: newBucket(sessionHandshakes, now),
	}
}

func (s *session) isPending() bool { return s.state.Load() == statePending }
func (s *session) isLive() bool    { return s.state.Load() == stateLive }
func (s *session) over() bool      { return s.state.Load() == stateOver }
func (s *session) end()            { s.state.Store(stateOver) }

// open opens a data packet of the session's client.
func (s *session) open(p, scratch []byte) ([]byte, error) {
	if s.over() {
		return nil, stealth.ErrRefused
	}
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	return s.recv.Open(p, scratch)
}

// seal makes a data packet for the client. The first one the session sends
// is its first answer, held to the size of the opening: a datagram too large
// for that goes unsent and padding alone takes its place. After that a
// packet may be as large as room, or as the datagram needs -- WireGuard's
// datagrams are sent whole, padding only ever giving way.
func (s *session) seal(d []byte, room int) ([]byte, error) {
	if s.over() || s.isPending() {
		return nil, stealth.ErrRefused
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	first := !s.answered
	s.answered = true
	if first {
		p, err := s.send.Seal(nil, d, false, s.budget)
		if err == stealth.ErrSize && d != nil {
			p, err = s.send.Seal(nil, nil, false, s.budget)
		}
		if err == nil {
			s.sent.Add(1)
		}
		return p, err
	}
	if d != nil {
		room = max(room, len(d)+2+stealth.DataMin)
	}
	p, err := s.send.Seal(nil, d, false, room)
	if err == nil {
		s.sent.Add(1)
	}
	return p, err
}

// slot is what one source address and port has of one device: its socket to
// the tunnel, and up to three sessions -- one waiting, one live, and the one
// the live one replaced, still read for a while. The session fields are the
// gateway's lock.
type slot struct {
	source  netip.AddrPort
	dev     *device
	wg      *net.UDPConn
	created time.Time

	pending, live, old *session
	oldUntil           time.Time

	lastActive atomic.Int64

	mu       sync.Mutex
	counters map[uint32]uint64 // the highest transport counter per index
	closed   bool
}

// readable is the slot's sessions a data packet is tried on, in order. The
// gateway's lock is held.
func (s *slot) readable(now time.Time) []*session {
	out := make([]*session, 0, 3)
	for _, x := range []*session{s.live, s.pending} {
		if x != nil && !x.over() {
			out = append(out, x)
		}
	}
	if s.old != nil && !s.old.over() && now.Before(s.oldUntil) {
		out = append(out, s.old)
	}
	return out
}

// fresh says whether a transport message carries a counter higher than any
// seen on the slot under its index. Only such a message counts as activity:
// a recorded one sent again carries an old counter.
func (s *slot) fresh(d []byte) bool {
	idx, c := receiverIndex(d), transportCounter(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if seen, ok := s.counters[idx]; ok && c <= seen {
		return false
	}
	if len(s.counters) >= 16 {
		clear(s.counters) // indexes a rekey left behind
	}
	s.counters[idx] = c
	return true
}

func (s *slot) active(now time.Time) { s.lastActive.Store(now.UnixNano()) }

// tidy ends what is over and says whether anything is left. The gateway's
// lock is held.
func (s *slot) tidy(now time.Time) bool {
	if s.pending != nil && (s.pending.over() || now.Sub(s.pending.opened) >= pendingFor) {
		s.pending.end()
		s.pending = nil
		s.dev.pending--
	}
	if s.old != nil && (s.old.over() || !now.Before(s.oldUntil)) {
		s.old.end()
		s.old = nil
	}
	if s.live != nil && (s.live.over() || now.Sub(time.Unix(0, s.lastActive.Load())) >= idleFor) {
		s.live.end()
		s.live = nil
	}
	return s.pending != nil || s.live != nil || s.old != nil
}

// close ends the slot's sessions and its socket.
func (s *slot) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, x := range []*session{s.pending, s.live, s.old} {
		if x != nil {
			x.end()
		}
	}
	s.wg.Close()
}
