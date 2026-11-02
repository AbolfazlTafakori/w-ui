package stealth

import (
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"strconv"

	"golang.org/x/crypto/chacha20poly1305"
)

// The relay requests.
//
// relay is for a CDN that will not carry WebSocket: the tunnel's
// datagrams travel in the bodies of ordinary POST requests to the
// transport's path, one request in flight at a time, and in the bodies of
// their answers. The first request is the proof, as drift's upgrade is; every
// later one names the session the proof opened and its own number:
//
//	opening  POST <path>?<token>
//	later    POST <path>?<id>.<n>      id = base64url(16 bytes), n = 1, 2, ...
//
// The id is the session's (StreamKeys.ID), and n is one more than the last
// request the server took, or the same number again for a request a front
// sent twice. Neither is a secret, and neither needs to be: a body is what
// proves a request is the device's.
//
// A body, either way, is the session's sealed chunks, each behind its
// length, in the order the stream's numbers give them, and holds at least
// one: a side with nothing to say sends a padding-only chunk.
//
//	[len u16 BE][sealed chunk] ... [len u16 BE][sealed chunk]
//
// The numbers run on across bodies in each direction as on drift, and a
// body is taken whole or not at all: every chunk must open at the next
// numbers before anything in it is used, and the numbers move on only when
// the body is taken. A body that does not open leaves the session exactly as
// it was, so whoever learns a session's id can send what they like under it
// and change nothing. A body whose every chunk opens and one of them does
// not parse can only be the device's own, and ends the session.

// Sizes of relay's requests.
const (
	// PollIDLen is the length of a session's id.
	PollIDLen = 16
	// PollBodyMin is the shortest body: one padding-only chunk of no
	// padding, behind its length.
	PollBodyMin = 2 + Overhead
	// PollBodyMax is the longest body either way.
	PollBodyMax = 65536
	// PollRequestMax is the last number a request may have. A client opens
	// a new session before it would pass it.
	PollRequestMax = math.MaxUint32
	// pollIDText is the id as base64url without padding.
	pollIDText = (PollIDLen*8 + 5) / 6
)

// PollQuery is the query of a later request: the session's id and the
// request's number, from 1.
func PollQuery(id [PollIDLen]byte, n uint32) string {
	return base64.RawURLEncoding.EncodeToString(id[:]) + "." + strconv.FormatUint(uint64(n), 10)
}

// ParsePollQuery reads the query of a later request, and nothing else: 22
// characters of base64url in the one form that names 16 bytes, a dot, and a
// number of one to ten digits with no leading zero, from 1 to
// PollRequestMax. Whether the id is a session's is the server's to look up.
func ParsePollQuery(q string) (id [PollIDLen]byte, n uint32, ok bool) {
	if len(q) < pollIDText+2 || len(q) > pollIDText+11 || q[pollIDText] != '.' {
		return id, 0, false
	}
	text, digits := q[:pollIDText], q[pollIDText+1:]
	raw, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil || len(raw) != PollIDLen {
		return id, 0, false
	}
	if digits[0] == '0' {
		return id, 0, false
	}
	var v uint64
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return id, 0, false
		}
		v = v*10 + uint64(c-'0')
	}
	if v > PollRequestMax {
		return id, 0, false
	}
	copy(id[:], raw)
	return id, uint32(v), true
}

// ErrBodyFull is a chunk that might not fit the body with the most padding
// it could draw. Nothing was appended and no number was used: the body goes
// as it is, and the chunk starts the next one.
var ErrBodyFull = errors.New("stealth: the body is full")

// AppendBody appends one chunk holding datagrams, behind its length, to a
// relay body; none makes a padding-only chunk. A chunk that might not fit
// within PollBodyMax is ErrBodyFull.
func (s *StreamSender) AppendBody(body []byte, datagrams ...[]byte) ([]byte, error) {
	return s.AppendBodyWithin(body, PollBodyMax, datagrams...)
}

// AppendBodyWithin is AppendBody for a body that may not grow past limit.
func (s *StreamSender) AppendBodyWithin(body []byte, limit int, datagrams ...[]byte) ([]byte, error) {
	used := 0
	for _, d := range datagrams {
		used += 2 + len(d)
	}
	if len(body)+2+min(used+s.pad.wide, MaxPlain)+Overhead > min(limit, PollBodyMax) {
		return body, ErrBodyFull
	}
	start := len(body)
	body = append(body, 0, 0)
	body, err := s.Seal(body, datagrams...)
	if err != nil {
		return body[:start], err
	}
	binary.BigEndian.PutUint16(body[start:], uint16(len(body)-start-2))
	return body, nil
}

// Why a body was not taken.
var (
	// ErrBodyRefused is a body that is not the session's next: it is the
	// wrong size, or a chunk in it does not open at the next numbers.
	// Nothing changed.
	ErrBodyRefused = errors.New("stealth: not the session's next body")
	// ErrBodyEnded is a body whose chunks all opened and one did not parse:
	// the device's own fault, and the end of the session. Every later body
	// is refused the same way.
	ErrBodyEnded = errors.New("stealth: the session's peer sent a bad chunk")
)

// PollReceiver opens one direction of a relay session: the server's for
// request bodies, the client's for answers. It is not safe for concurrent
// use.
type PollReceiver struct {
	aead  cipher.AEAD
	n     uint64
	ended bool
	plain []byte
}

// NewPollReceiver starts reading a direction with its key.
func NewPollReceiver(key []byte) (*PollReceiver, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, errors.New("stealth: stream key: " + err.Error())
	}
	return &PollReceiver{aead: a}, nil
}

// PollBody is a body whose chunks all opened and parsed, not yet taken.
type PollBody struct {
	r    *PollReceiver
	next uint64
	// Chunks are the datagrams of each chunk, in order. They lie in the
	// receiver's own buffer and are good until its next Open.
	Chunks [][][]byte
}

// Open opens every chunk of body at the numbers after the last body taken,
// and moves nothing: Commit does. body itself is left as it came, since a
// request that is refused goes on to the decoy as it arrived.
func (r *PollReceiver) Open(body []byte) (*PollBody, error) {
	if r.ended {
		return nil, ErrBodyEnded
	}
	if len(body) < PollBodyMin || len(body) > PollBodyMax {
		return nil, ErrBodyRefused
	}
	// Every chunk is opened before any is parsed, so a body with a chunk
	// that does not open is refused whatever the others hold: only a body
	// that is wholly the device's can end its session.
	type span struct{ at, end int }
	var (
		spans []span
		plain = r.plain[:0]
		n     = r.n
	)
	for p := body; len(p) > 0; {
		if len(p) < 2 {
			return nil, ErrBodyRefused
		}
		size := int(binary.BigEndian.Uint16(p))
		if size < Overhead || size > MaxSealed || 2+size > len(p) || n >= maxCounter {
			return nil, ErrBodyRefused
		}
		at := len(plain)
		var err error
		if plain, err = r.aead.Open(plain, streamNonce(n), p[2:2+size], nil); err != nil {
			r.plain = plain[:0]
			return nil, ErrBodyRefused
		}
		spans = append(spans, span{at, len(plain)})
		p, n = p[2+size:], n+1
	}
	r.plain = plain
	b := &PollBody{r: r, next: n, Chunks: make([][][]byte, 0, len(spans))}
	for _, s := range spans {
		var items [][]byte
		if err := Items(plain[s.at:s.end], func(d []byte) { items = append(items, d) }); err != nil {
			r.ended = true
			return nil, ErrBodyEnded
		}
		b.Chunks = append(b.Chunks, items)
	}
	return b, nil
}

// Commit takes the body: the numbers move past it, and the same body no
// longer opens.
func (b *PollBody) Commit() { b.r.n = b.next }

// Datagrams are every datagram of the body, in order.
func (b *PollBody) Datagrams() [][]byte {
	var out [][]byte
	for _, c := range b.Chunks {
		out = append(out, c...)
	}
	return out
}
