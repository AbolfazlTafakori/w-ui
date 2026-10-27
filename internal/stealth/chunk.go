package stealth

import (
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// The chunk.
//
// A chunk is what every transport moves: one or more WireGuard datagrams,
// each behind its length, then zero padding, the whole sealed with
// ChaCha20-Poly1305.
//
//	[len u16 BE][datagram] ... [len u16 BE][datagram] [0x00 ...]
//
// A WireGuard datagram is never shorter than MinDatagram, so a length of zero
// can never start a real item: the first zero length is where the padding
// begins, and everything after it must be zero too. That is the whole framing;
// there is no count and no type byte for the network to learn.

// Limits of a chunk's plaintext.
const (
	// MinDatagram is the shortest WireGuard datagram, a keepalive.
	MinDatagram = 32
	// MaxDatagram is the longest datagram a chunk takes. It is above any MTU a
	// tunnel runs with.
	MaxDatagram = 1600
	// MaxPlain is the longest plaintext a chunk may have, padding included.
	MaxPlain = 16000
	// Overhead is what sealing adds to a plaintext.
	Overhead = chacha20poly1305.Overhead
	// MaxSealed is the longest a sealed chunk can be.
	MaxSealed = MaxPlain + Overhead
)

// ErrChunk is any chunk that does not open or does not parse. The transports
// answer it the same way whatever went wrong, so it says nothing more.
var ErrChunk = errors.New("stealth: bad chunk")

// AppendItem appends one datagram, behind its length, to a chunk's plaintext.
func AppendItem(plain, datagram []byte) ([]byte, error) {
	if len(datagram) < MinDatagram || len(datagram) > MaxDatagram {
		return plain, fmt.Errorf("stealth: datagram of %d bytes", len(datagram))
	}
	if len(plain)+2+len(datagram) > MaxPlain {
		return plain, errors.New("stealth: chunk is full")
	}
	plain = binary.BigEndian.AppendUint16(plain, uint16(len(datagram)))
	return append(plain, datagram...), nil
}

// Fits says whether a datagram of n bytes still fits in a plaintext of used
// bytes.
func Fits(used, n int) bool { return used+2+n <= MaxPlain }

// AppendPadding appends n zero bytes, clipped so the plaintext stays within
// MaxPlain.
func AppendPadding(plain []byte, n int) []byte {
	if room := MaxPlain - len(plain); n > room {
		n = room
	}
	for ; n > 0; n-- {
		plain = append(plain, 0)
	}
	return plain
}

// Items calls fn with each datagram in an opened plaintext, in order. The
// slices fn sees alias plain. A plaintext that is malformed anywhere -- an
// item too short or too long, an item that runs past the end, padding that is
// not all zero -- is refused whole, before fn sees any of it.
func Items(plain []byte, fn func(datagram []byte)) error {
	if len(plain) > MaxPlain {
		return ErrChunk
	}
	end, err := itemsEnd(plain)
	if err != nil {
		return err
	}
	for p := plain[:end]; len(p) > 0; {
		n := int(binary.BigEndian.Uint16(p))
		fn(p[2 : 2+n])
		p = p[2+n:]
	}
	return nil
}

// itemsEnd checks a plaintext and reports where its items stop.
func itemsEnd(plain []byte) (int, error) {
	i := 0
	for i+2 <= len(plain) {
		n := int(binary.BigEndian.Uint16(plain[i:]))
		if n == 0 {
			break
		}
		if n < MinDatagram || n > MaxDatagram || i+2+n > len(plain) {
			return 0, ErrChunk
		}
		i += 2 + n
	}
	for _, b := range plain[i:] {
		if b != 0 {
			return 0, ErrChunk
		}
	}
	return i, nil
}

// maxCounter is where a stream stops. Nothing comes near it -- a session is
// rekeyed long before -- but a nonce must never repeat, so it is enforced.
const maxCounter = 1 << 60

// Sealer seals a stream's chunks in order. The nonce is the chunk's number in
// the stream, so the key must be one this stream alone uses.
type Sealer struct {
	aead cipher.AEAD
	n    uint64
}

// NewSealer starts a stream with a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("stealth: stream key: %w", err)
	}
	return &Sealer{aead: a}, nil
}

// Seal appends the sealed plaintext to dst.
func (s *Sealer) Seal(dst, plain []byte) ([]byte, error) {
	if len(plain) > MaxPlain {
		return dst, errors.New("stealth: chunk too long")
	}
	if s.n >= maxCounter {
		return dst, errors.New("stealth: stream exhausted")
	}
	out := s.aead.Seal(dst, streamNonce(s.n), plain, nil)
	s.n++
	return out, nil
}

// Opener opens a stream's chunks in order. The first chunk it cannot open
// ends it: every later call fails too, since a stream that has lost its place
// can only be dropped.
type Opener struct {
	aead   cipher.AEAD
	n      uint64
	broken bool
}

// NewOpener starts reading a stream with a 32-byte key.
func NewOpener(key []byte) (*Opener, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("stealth: stream key: %w", err)
	}
	return &Opener{aead: a}, nil
}

// Open appends the opened plaintext of the next sealed chunk to dst. The
// plaintext's framing is checked too, so what comes back is safe to hand to
// Items.
func (o *Opener) Open(dst, sealed []byte) ([]byte, error) {
	if o.broken || len(sealed) < Overhead || len(sealed) > MaxSealed || o.n >= maxCounter {
		o.broken = true
		return dst, ErrChunk
	}
	out, err := o.aead.Open(dst, streamNonce(o.n), sealed, nil)
	if err != nil {
		o.broken = true
		return dst, ErrChunk
	}
	if _, err := itemsEnd(out[len(dst):]); err != nil {
		o.broken = true
		return dst, ErrChunk
	}
	o.n++
	return out, nil
}

// streamNonce is four zero bytes, then the chunk's number little-endian.
func streamNonce(n uint64) []byte {
	var nonce [chacha20poly1305.NonceSize]byte
	binary.LittleEndian.PutUint64(nonce[4:], n)
	return nonce[:]
}
