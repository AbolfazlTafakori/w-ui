package stealth

import "fmt"

// The stream sessions.
//
// On a stream transport a session's chunks travel in order, so the nonce is
// the chunk's number (see Sealer) and nothing goes on the wire beside the
// sealed chunk but the transport's own framing: on wui-ws, one binary
// WebSocket message per chunk. A chunk carries as many datagrams as are
// waiting and fit, or none: a padding-only chunk is how a side answers
// without saying anything.

// StreamOverhead is what a full-size inner IP packet grows by inside a
// stream chunk that carries it alone: WireGuard's 32 bytes, the length and
// the seal. The transport's framing comes on top.
const StreamOverhead = 32 + 2 + Overhead

// StreamSender seals one direction of a stream session. It is not safe for
// concurrent use.
type StreamSender struct {
	sealer *Sealer
	pad    Padder
	plain  []byte
}

// NewStreamSender starts a direction with its key and its padder.
func NewStreamSender(key []byte, pad Padder) (*StreamSender, error) {
	s, err := NewSealer(key)
	if err != nil {
		return nil, err
	}
	return &StreamSender{sealer: s, pad: pad, plain: make([]byte, 0, MaxPlain)}, nil
}

// Seal appends one sealed chunk holding datagrams, in order, to dst; none
// makes a padding-only chunk. The chunk is padded wide if any of them is a
// WireGuard handshake, or it is among the direction's first chunks.
// datagrams must fit one chunk: Fits says how many do.
func (s *StreamSender) Seal(dst []byte, datagrams ...[]byte) ([]byte, error) {
	plain := s.plain[:0]
	handshake := false
	for _, d := range datagrams {
		var err error
		if plain, err = AppendItem(plain, d); err != nil {
			return dst, err
		}
		handshake = handshake || IsHandshake(d)
	}
	plain = AppendPadding(plain, s.pad.Next(handshake))
	s.plain = plain
	return s.sealer.Seal(dst, plain)
}

// StreamFits is how many of datagrams, from the first, one chunk holds.
func StreamFits(datagrams [][]byte) int {
	used := 0
	for i, d := range datagrams {
		if !Fits(used, len(d)) {
			return i
		}
		used += 2 + len(d)
	}
	return len(datagrams)
}

// StreamReceiver opens one direction of a stream session. It is not safe for
// concurrent use. The first chunk that does not open ends it.
type StreamReceiver struct {
	opener *Opener
	plain  []byte
}

// NewStreamReceiver starts reading a direction with its key.
func NewStreamReceiver(key []byte) (*StreamReceiver, error) {
	o, err := NewOpener(key)
	if err != nil {
		return nil, err
	}
	return &StreamReceiver{opener: o, plain: make([]byte, 0, MaxPlain)}, nil
}

// Open opens the next sealed chunk and calls fn with each datagram in it.
// The slices fn sees are good until the next call.
func (r *StreamReceiver) Open(sealed []byte, fn func(datagram []byte)) error {
	plain, err := r.opener.Open(r.plain[:0], sealed)
	if err != nil {
		return err
	}
	r.plain = plain
	if err := Items(plain, fn); err != nil {
		return fmt.Errorf("stealth: %w", err) // cannot happen: Open checked it
	}
	return nil
}
