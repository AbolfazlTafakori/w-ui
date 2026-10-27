package stealth

import (
	"bytes"
	"testing"
)

func datagram(n int, fill byte) []byte { return bytes.Repeat([]byte{fill}, n) }

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestChunkRoundTrip(t *testing.T) {
	in := [][]byte{datagram(MinDatagram, 1), datagram(148, 2), datagram(MaxDatagram, 3)}
	var plain []byte
	for _, d := range in {
		var err error
		if plain, err = AppendItem(plain, d); err != nil {
			t.Fatal(err)
		}
	}
	plain = AppendPadding(plain, 300)

	s, _ := NewSealer(key(7))
	o, _ := NewOpener(key(7))
	for round := range 3 {
		sealed, err := s.Seal(nil, plain)
		if err != nil {
			t.Fatal(err)
		}
		if len(sealed) != len(plain)+Overhead {
			t.Fatalf("sealed %d bytes, want %d", len(sealed), len(plain)+Overhead)
		}
		opened, err := o.Open(nil, sealed)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		var got [][]byte
		if err := Items(opened, func(d []byte) { got = append(got, bytes.Clone(d)) }); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(in) {
			t.Fatalf("got %d datagrams, want %d", len(got), len(in))
		}
		for i := range in {
			if !bytes.Equal(got[i], in[i]) {
				t.Fatalf("datagram %d differs", i)
			}
		}
	}
}

func TestChunkPaddingOnly(t *testing.T) {
	n := 0
	if err := Items(AppendPadding(nil, 64), func([]byte) { n++ }); err != nil || n != 0 {
		t.Fatalf("padding-only chunk: %d items, %v", n, err)
	}
	if err := Items(nil, func([]byte) { n++ }); err != nil || n != 0 {
		t.Fatalf("empty chunk: %d items, %v", n, err)
	}
}

func TestAppendItemLimits(t *testing.T) {
	for _, n := range []int{0, MinDatagram - 1, MaxDatagram + 1} {
		if _, err := AppendItem(nil, datagram(n, 1)); err == nil {
			t.Errorf("datagram of %d bytes accepted", n)
		}
	}
	full := AppendPadding(nil, MaxPlain-2-MinDatagram+1)
	if _, err := AppendItem(full, datagram(MinDatagram, 1)); err == nil {
		t.Error("item past MaxPlain accepted")
	}
	if got := len(AppendPadding(nil, MaxPlain+100)); got != MaxPlain {
		t.Errorf("padding not clipped: %d bytes", got)
	}
}

func TestItemsRefusesMalformed(t *testing.T) {
	good, _ := AppendItem(nil, datagram(64, 9))
	cases := map[string][]byte{
		"short item":        {0, MinDatagram - 1},
		"long item":         {0x06, 0x41}, // 1601
		"runs past end":     append([]byte{0, 64}, datagram(10, 1)...),
		"nonzero padding":   append(append(bytes.Clone(good), 0, 0, 0), 5),
		"odd trailing byte": append(bytes.Clone(good), 7),
	}
	for name, plain := range cases {
		called := false
		if err := Items(plain, func([]byte) { called = true }); err != ErrChunk {
			t.Errorf("%s: err %v, want ErrChunk", name, err)
		}
		if called {
			t.Errorf("%s: fn called on a refused chunk", name)
		}
	}
}

func TestOpenerBreaksOnFirstError(t *testing.T) {
	s, _ := NewSealer(key(1))
	o, _ := NewOpener(key(1))
	plain, _ := AppendItem(nil, datagram(64, 4))
	a, _ := s.Seal(nil, plain)
	b, _ := s.Seal(nil, plain)

	tampered := bytes.Clone(a)
	tampered[3] ^= 1
	if _, err := o.Open(nil, tampered); err != ErrChunk {
		t.Fatalf("tampered chunk: %v", err)
	}
	if _, err := o.Open(nil, a); err != ErrChunk {
		t.Fatal("opener went on after an error")
	}
	_ = b
}

func TestOpenerRefusesReorderAndWrongKey(t *testing.T) {
	s, _ := NewSealer(key(1))
	plain, _ := AppendItem(nil, datagram(64, 4))
	a, _ := s.Seal(nil, plain)
	b, _ := s.Seal(nil, plain)

	o, _ := NewOpener(key(1))
	if _, err := o.Open(nil, b); err != ErrChunk {
		t.Error("second chunk opened first")
	}
	o, _ = NewOpener(key(2))
	if _, err := o.Open(nil, a); err != ErrChunk {
		t.Error("chunk opened under another key")
	}
}

func TestOpenAppendsToDst(t *testing.T) {
	s, _ := NewSealer(key(3))
	o, _ := NewOpener(key(3))
	plain, _ := AppendItem(nil, datagram(40, 8))
	sealed, _ := s.Seal(nil, plain)
	out, err := o.Open([]byte("prefix"), sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("prefix")) || !bytes.Equal(out[6:], plain) {
		t.Fatal("Open did not append after dst")
	}
}

func TestPadderRanges(t *testing.T) {
	p := NewPadder(0, 0)
	for i := range 200 {
		n := p.Next(false)
		if i < wideChunks {
			if n < p.base || n > WideMax {
				t.Fatalf("wide chunk %d padded %d", i, n)
			}
		} else if n > p.normal {
			t.Fatalf("normal chunk %d padded %d over %d", i, n, p.normal)
		}
	}
	if n := p.Next(true); n < p.base || n > p.wide {
		t.Fatalf("handshake padded %d", n)
	}
}

func TestPadderClampsProfile(t *testing.T) {
	p := NewPadder(10, 9999)
	if p.wide != WideMin || p.normal != NormalMax {
		t.Fatalf("wide %d normal %d", p.wide, p.normal)
	}
}

func TestIsHandshake(t *testing.T) {
	for b, want := range map[byte]bool{0: false, 1: true, 2: true, 3: true, 4: false} {
		if IsHandshake([]byte{b, 0, 0, 0}) != want {
			t.Errorf("type %d", b)
		}
	}
	if IsHandshake(nil) {
		t.Error("empty datagram")
	}
}
