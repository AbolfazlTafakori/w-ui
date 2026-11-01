package stealth

import "encoding/binary"

// The wui-faketls session frames.
//
// Inside the session the application records carry the tunnel's datagrams as
//
//	[len u16 | chunk plaintext]  ...
//
// where the chunk plaintext is the items of a stealth chunk and its padding,
// exactly as a stealth chunk's plaintext is framed -- but it is not sealed: the
// TLS 1.3 record the gateway writes is itself encrypted, so a second AEAD would
// add nothing. One record may hold several frames, and the record padding of
// TLS 1.3 (TLSKeys.Seal) shapes the record after the site's.

// FakeFrameOverhead is what one frame adds to a chunk plaintext: its length.
const FakeFrameOverhead = 2

// AppendFakeFrame appends one frame holding datagrams, with pad zero bytes of
// chunk padding after them, to the record plaintext dst. ok is false when the
// datagrams and padding do not fit a chunk; then nothing is appended.
func AppendFakeFrame(dst []byte, pad int, datagrams ...[]byte) ([]byte, bool) {
	plain := make([]byte, 0, MaxPlain)
	for _, d := range datagrams {
		var err error
		if plain, err = AppendItem(plain, d); err != nil {
			return dst, false
		}
	}
	if len(plain)+pad > MaxPlain {
		return dst, false
	}
	plain = AppendPadding(plain, pad)
	start := len(dst)
	dst = append(dst, 0, 0)
	binary.BigEndian.PutUint16(dst[start:], uint16(len(plain)))
	return append(dst, plain...), true
}

// ReadFakeFrames reads every whole frame in a record's plaintext and calls fn
// with each datagram, in order. It returns how many bytes it consumed; bytes
// left over are the start of a frame split across records, which the caller
// keeps and prepends to the next record. A frame whose length is impossible,
// or whose chunk plaintext does not parse, is an error: the session is over.
func ReadFakeFrames(plain []byte, fn func(datagram []byte)) (int, error) {
	at := 0
	for at+2 <= len(plain) {
		size := int(binary.BigEndian.Uint16(plain[at:]))
		if size > MaxPlain {
			return at, ErrChunk
		}
		if at+2+size > len(plain) {
			break // a frame split across records
		}
		if err := Items(plain[at+2:at+2+size], fn); err != nil {
			return at, err
		}
		at += 2 + size
	}
	return at, nil
}
