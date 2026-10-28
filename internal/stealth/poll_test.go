package stealth

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

func pollSender(t *testing.T) *StreamSender {
	t.Helper()
	s, err := NewStreamSender(key(5), NewPadder(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pollReceiver(t *testing.T) *PollReceiver {
	t.Helper()
	r, err := NewPollReceiver(key(5))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// body is one chunk for each list of datagrams.
func body(t *testing.T, s *StreamSender, chunks ...[][]byte) []byte {
	t.Helper()
	var b []byte
	for _, c := range chunks {
		var err error
		if b, err = s.AppendBody(b, c...); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// forged is a chunk the session's key seals at number n around any
// plaintext, behind its length: what only the session's own peer can make.
// The AEAD is used as it is, so a number the sealer would refuse is sealed
// too.
func forged(n uint64, plain []byte) []byte {
	a, _ := chacha20poly1305.New(key(5))
	sealed := a.Seal(nil, streamNonce(n), plain, nil)
	return append(binary.BigEndian.AppendUint16(nil, uint16(len(sealed))), sealed...)
}

func items(b *PollBody) [][][]byte {
	var out [][][]byte
	for _, c := range b.Chunks {
		var cc [][]byte
		for _, d := range c {
			cc = append(cc, bytes.Clone(d))
		}
		out = append(out, cc)
	}
	return out
}

func TestThePollQuery(t *testing.T) {
	var id [PollIDLen]byte
	for i := range id {
		id[i] = byte(i)
	}
	if q := PollQuery(id, 7); q != "AAECAwQFBgcICQoLDA0ODw.7" {
		t.Fatalf("query %q", q)
	}
	for q, n := range map[string]uint32{"AAECAwQFBgcICQoLDA0ODw.7": 7, "AAECAwQFBgcICQoLDA0ODw.4294967295": PollRequestMax, "AAECAwQFBgcICQoLDA0ODw.10": 10} {
		got, gn, ok := ParsePollQuery(q)
		if !ok || got != id || gn != n {
			t.Fatalf("%q: %v %d %v", q, got, gn, ok)
		}
	}
	if got, _, ok := ParsePollQuery("_-ECAwQFBgcICQoLDA0ODw.1"); !ok || got[0] != 0xff {
		t.Fatalf("base64url's own characters: %v %v", got, ok)
	}
	// One to ten digits, no leading zero, at most 2^32 - 1, and an id of
	// exactly 16 bytes in the one text that names them.
	for _, q := range []string{
		"", "AAECAwQFBgcICQoLDA0ODw", "AAECAwQFBgcICQoLDA0ODw.", "AAECAwQFBgcICQoLDA0ODw.0",
		"AAECAwQFBgcICQoLDA0ODw.07", "AAECAwQFBgcICQoLDA0ODw.4294967296", "AAECAwQFBgcICQoLDA0ODw.12345678901",
		"AAECAwQFBgcICQoLDA0ODw.+7", "AAECAwQFBgcICQoLDA0ODw.7 ", "AAECAwQFBgcICQoLDA0ODw.7.1",
		"AAECAwQFBgcICQoLDA0ODw-7", "AAECAwQFBgcICQoLDA0OD.7", "AAECAwQFBgcICQoLDA0ODwA.7",
		"AAECAwQFBgcICQoLDA0ODx.7", "AAECAwQFBgcICQoLDA0OD=.7", "AAECAwQFBgcICQoLDA0OD+.7",
		"AAECAwQFBgcICQoLDA0ODw==.7", "AAECAwQFBgcICQoLDA0ODw.-1", "AAECAwQFBgcICQoLDA0ODw.1e3",
	} {
		if _, _, ok := ParsePollQuery(q); ok {
			t.Errorf("%q was read as a later request", q)
		}
	}
}

// "The numbers move on only when the body is taken."
func TestABodyIsTakenWholeOrNotAtAll(t *testing.T) {
	s, r := pollSender(t), pollReceiver(t)
	a, b, c := datagram(32, 1), datagram(148, 2), datagram(92, 3)
	first := body(t, s, [][]byte{a}, [][]byte{b, c})
	second := body(t, s, nil, [][]byte{a})
	for range 3 {
		got, err := r.Open(first)
		if err != nil {
			t.Fatal(err)
		}
		if g := items(got); len(g) != 2 || !bytes.Equal(g[0][0], a) || !bytes.Equal(g[1][1], c) {
			t.Fatalf("the first body read %v", g)
		}
	}
	if r.n != 0 {
		t.Fatal("opening moved the numbers")
	}
	if _, err := r.Open(second); !errors.Is(err, ErrBodyRefused) {
		t.Fatalf("the body after next: %v", err)
	}
	got, _ := r.Open(first)
	got.Commit()
	if r.n != 2 {
		t.Fatalf("taken, the numbers are at %d", r.n)
	}
	// A repeat of a body taken no longer opens: the server answers it from
	// the answer it kept, by its bytes.
	if _, err := r.Open(first); !errors.Is(err, ErrBodyRefused) {
		t.Fatalf("a repeat: %v", err)
	}
	got, err := r.Open(second)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Datagrams(); len(d) != 1 || !bytes.Equal(d[0], a) {
		t.Fatalf("the second body read %v", d)
	}
	got.Commit()
	if r.n != 4 {
		t.Fatalf("the numbers are at %d", r.n)
	}
}

// Whoever learns a session's id sends what they like: none of it changes
// the session, and the real body opens after all of it.
func TestNothingAStrangerSendsDisturbsASession(t *testing.T) {
	s, r := pollSender(t), pollReceiver(t)
	good := body(t, s, [][]byte{datagram(32, 1)}, [][]byte{datagram(32, 2)})
	strangers := [][]byte{
		nil, make([]byte, PollBodyMin-1), make([]byte, PollBodyMin), bytes.Repeat([]byte{0xff}, 600),
		make([]byte, PollBodyMax), make([]byte, PollBodyMax+1),
		append(bytes.Clone(good), 0), append(bytes.Clone(good), 0, 16), append(append(bytes.Clone(good), 0, 15), make([]byte, 15)...),
		append(append(bytes.Clone(good), 0, 16), make([]byte, 16)...), good[:len(good)-1], good[:51],
		append([]byte{0x3e, 0x91}, make([]byte, 16017)...), append(bytes.Clone(good[52:]), good[:52]...),
		// A chunk sealed for another place in the stream, and one that does
		// not open behind one that opens and holds a bad item.
		append(bytes.Clone(good), forged(3, make([]byte, 4))...),
		append(forged(0, []byte{0, 9, 1}), append([]byte{0, 16}, make([]byte, 16)...)...),
	}
	for bit := range len(good) * 8 {
		changed := bytes.Clone(good)
		changed[bit/8] ^= 1 << (bit % 8)
		strangers = append(strangers, changed)
	}
	for i, b := range strangers {
		if _, err := r.Open(b); !errors.Is(err, ErrBodyRefused) {
			t.Fatalf("stranger %d (%d bytes): %v", i, len(b), err)
		}
		if r.n != 0 || r.ended {
			t.Fatalf("stranger %d moved the session", i)
		}
	}
	got, err := r.Open(good)
	if err != nil {
		t.Fatal(err)
	}
	got.Commit()
}

// A body whose chunks all open and one does not parse is the device's own
// fault: the session is over, and stays over.
func TestABadChunkFromThePeerEndsTheSession(t *testing.T) {
	for _, bad := range [][]byte{{0, 9, 1}, {0, 31, 7, 7}, append([]byte{0x06, 0x41}, bytes.Repeat([]byte{7}, 1601)...), {0, 0, 1}} {
		s, r := pollSender(t), pollReceiver(t)
		good := body(t, s, [][]byte{datagram(32, 1)})
		if _, err := r.Open(append(forged(0, make([]byte, 3)), forged(1, bad)...)); !errors.Is(err, ErrBodyEnded) {
			t.Fatalf("%x: %v", bad[:3], err)
		}
		for _, b := range [][]byte{good, nil} {
			if _, err := r.Open(b); !errors.Is(err, ErrBodyEnded) {
				t.Fatalf("after the end: %v", err)
			}
		}
	}
}

func TestABodyStaysWithin64KiB(t *testing.T) {
	s, r := pollSender(t), pollReceiver(t)
	full := make([][]byte, 9)
	for i := range full {
		full[i] = datagram(MaxDatagram, 7)
	}
	var b []byte
	var err error
	for range 4 {
		if b, err = s.AppendBody(b, full...); err != nil {
			t.Fatal(err)
		}
	}
	// A fifth might not fit with the padding it could draw, and is refused
	// before it uses a number.
	size, used := len(b), s.sealer.n
	if _, err := s.AppendBody(b, full...); !errors.Is(err, ErrBodyFull) {
		t.Fatalf("a fifth full chunk: %v", err)
	}
	if s.sealer.n != used {
		t.Fatal("a refused chunk used a number")
	}
	for {
		next, err := s.AppendBody(b, datagram(32, 1))
		if err != nil {
			break
		}
		b = next
	}
	if len(b) > PollBodyMax || len(b) <= size {
		t.Fatalf("a body of %d bytes", len(b))
	}
	got, err := r.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	got.Commit()
	if r.n != s.sealer.n {
		t.Fatalf("the receiver is at %d, the sender at %d", r.n, s.sealer.n)
	}
	// Within a smaller limit too.
	if _, err := s.AppendBodyWithin(nil, 1000, datagram(MaxDatagram, 1)); !errors.Is(err, ErrBodyFull) {
		t.Fatalf("over a limit: %v", err)
	}
}

// A chunk numbered 2^60 is never taken.
func TestThePollNumbersEnd(t *testing.T) {
	r := pollReceiver(t)
	r.n = maxCounter - 1
	if _, err := r.Open(append(forged(maxCounter-1, nil), forged(maxCounter, nil)...)); !errors.Is(err, ErrBodyRefused) {
		t.Fatalf("past the end: %v", err)
	}
	got, err := r.Open(forged(maxCounter-1, nil))
	if err != nil {
		t.Fatal(err)
	}
	got.Commit()
	if _, err := r.Open(forged(maxCounter, nil)); !errors.Is(err, ErrBodyRefused) {
		t.Fatalf("at the end: %v", err)
	}
}
