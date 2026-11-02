package stealth

import (
	"crypto/rand"
	"math/big"
)

// The padding.
//
// How much padding a chunk gets is drawn afresh for every chunk, from a range
// that is itself drawn once per session, so no two sessions share a size
// profile. A session's first chunks, and any chunk carrying a WireGuard
// handshake, are padded wide: those are the packets whose sizes would
// otherwise give the tunnel away. Everything after is padded lightly, since
// bulk traffic's sizes are the application's and say little.

// Padding ranges, in bytes.
const (
	// baseMax bounds a session's floor, the least any of its chunks gets.
	baseMax = 63
	// WideMin and WideMax bound a session's wide ceiling.
	WideMin, WideMax = 512, 1023
	// NormalMin and NormalMax bound a session's normal ceiling.
	NormalMin, NormalMax = 64, 255
	// wideChunks is how many of a direction's first chunks are padded wide.
	wideChunks = 16
)

// Padder decides each chunk's padding for one direction of one session. It is
// not safe for concurrent use; each direction has its own.
type Padder struct {
	base, wide, normal int
	sent               int
}

// NewPadder draws a direction's ranges. wide and normal are the ceilings a
// profile sets, clamped into their ranges; zero means the largest.
func NewPadder(wide, normal int) Padder {
	wide = clampOr(wide, WideMin, WideMax)
	normal = clampOr(normal, NormalMin, NormalMax)
	return Padder{base: uniform(0, baseMax), wide: wide, normal: normal}
}

// Next is the padding for the next chunk. handshake says the chunk carries a
// WireGuard handshake message.
func (p *Padder) Next(handshake bool) int {
	wide := handshake || p.sent < wideChunks
	p.sent++
	if wide {
		return uniform(p.base, p.wide)
	}
	return uniform(min(p.base, p.normal), p.normal)
}

// DrawWithin is the padding for the next chunk, clamped so a chunk whose
// items use used bytes still fits room bytes of plaintext: mold draws
// within what is left of a request's limit, so a chunk whose items fit always
// fits. handshake forces wide, as Next does. room must be at least used.
func (p *Padder) DrawWithin(handshake bool, used, room int) int {
	pad := p.Next(handshake)
	if used+pad > room {
		pad = room - used
	}
	if pad < 0 {
		pad = 0
	}
	return pad
}

// Wide is a wide draw that is not counted among the direction's first
// chunks: the padding of a UDP opening, which is always wide and is not one of
// the data packets the first sixteen are counted over.
func (p *Padder) Wide() int { return uniform(p.base, p.wide) }

// IsHandshake says whether a WireGuard datagram is a handshake message:
// an initiation, a response or a cookie reply. The type is its first byte.
func IsHandshake(datagram []byte) bool {
	return len(datagram) > 0 && datagram[0] >= 1 && datagram[0] <= 3
}

func clampOr(v, lo, hi int) int {
	switch {
	case v == 0 || v > hi:
		return hi
	case v < lo:
		return lo
	}
	return v
}

// uniform is a number drawn evenly from [lo, hi].
func uniform(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo+1)))
	if err != nil {
		panic(err) // crypto/rand does not fail on a supported platform
	}
	return lo + int(n.Int64())
}
