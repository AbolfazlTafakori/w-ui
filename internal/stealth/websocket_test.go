package stealth

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"testing"
)

func TestTheAcceptOfRFC6455sExample(t *testing.T) {
	if got := WebSocketAccept("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept is %q", got)
	}
}

func TestAFrameReadsBackOnTheOtherSide(t *testing.T) {
	buf := make([]byte, MaxSealed)
	for _, n := range []int{0, 1, 125, 126, 127, 1500, MaxSealed} {
		payload := bytes.Repeat([]byte{0xA5}, n)
		for _, mask := range []bool{true, false} {
			f := AppendFrame(nil, OpBinary, payload, mask)
			op, got, err := ReadFrame(bytes.NewReader(f), buf, mask)
			if err != nil || op != OpBinary || !bytes.Equal(got, payload) {
				t.Fatalf("%d bytes, masked %v: op %d, %v", n, mask, op, err)
			}
			// The payload's own bytes, after the head and the key, and four of
			// them at least: one byte may come out as it went in by chance,
			// under a key byte of zero; four do once in 2^32.
			if mask && n >= 4 && bytes.Equal(f[len(f)-n:], payload) {
				t.Fatalf("%d bytes went out unmasked", n)
			}
		}
	}
}

func TestOnlyWhatASessionTakesIsRead(t *testing.T) {
	buf := make([]byte, MaxSealed)
	long := make([]byte, 0, 10)
	long = append(long, 0x82, 0x80|127)
	long = binary.BigEndian.AppendUint64(long, MaxSealed+1)
	for name, f := range map[string][]byte{
		"text":                 AppendFrame(nil, 0x1, []byte("hi"), true),
		"a continuation":       AppendFrame(nil, 0x0, []byte("hi"), true),
		"a reserved opcode":    AppendFrame(nil, 0x3, nil, true),
		"a fragment":           func() []byte { f := AppendFrame(nil, OpBinary, []byte("hi"), true); f[0] &^= 0x80; return f }(),
		"a reserved bit":       func() []byte { f := AppendFrame(nil, OpBinary, []byte("hi"), true); f[0] |= 0x40; return f }(),
		"unmasked from client": AppendFrame(nil, OpBinary, []byte("hi"), false),
		"a long ping":          AppendFrame(nil, OpPing, make([]byte, 126), true),
		"longer than a chunk":  long,
	} {
		if _, _, err := ReadFrame(bytes.NewReader(f), buf, true); err != ErrFrame {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := ReadFrame(bytes.NewReader(AppendFrame(nil, OpBinary, []byte("hi"), true)), buf, false); err != ErrFrame {
		t.Error("a masked frame from a server was read")
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{0x82, 0x85, 1}), buf, true); err != io.ErrUnexpectedEOF {
		t.Errorf("a cut frame: %v", err)
	}
}

func TestAnUpgradeIsAskedForTheWayRFC6455Says(t *testing.T) {
	good := func() *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "http://x/p", nil)
		r.Header.Set("Upgrade", "WebSocket")
		r.Header.Set("Connection", "keep-alive, Upgrade")
		r.Header.Set("Sec-WebSocket-Version", "13")
		r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		return r
	}
	if k, ok := WebSocketKey(good()); !ok || k != "dGhlIHNhbXBsZSBub25jZQ==" {
		t.Fatal("a good upgrade was refused")
	}
	for name, spoil := range map[string]func(*http.Request){
		"POST":          func(r *http.Request) { r.Method = http.MethodPost },
		"no upgrade":    func(r *http.Request) { r.Header.Del("Upgrade") },
		"h2c":           func(r *http.Request) { r.Header.Set("Upgrade", "h2c") },
		"no connection": func(r *http.Request) { r.Header.Set("Connection", "keep-alive") },
		"version 8":     func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "8") },
		"short key":     func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "c2hvcnQ=") },
	} {
		r := good()
		spoil(r)
		if _, ok := WebSocketKey(r); ok {
			t.Errorf("%s was taken", name)
		}
	}
}

func TestAStreamCarriesDatagramsInOrderAndPaddingAlone(t *testing.T) {
	k := key(7)
	s, _ := NewStreamSender(k, NewPadder(0, 0))
	r, _ := NewStreamReceiver(k)
	init := append([]byte{1, 0, 0, 0}, datagram(144, 1)...)
	data := append([]byte{4, 0, 0, 0}, datagram(1200, 2)...)
	var got [][]byte
	collect := func(d []byte) { got = append(got, bytes.Clone(d)) }
	for _, batch := range [][][]byte{{init}, {data, keepalive()}, nil} {
		sealed, err := s.Seal(nil, batch...)
		if err != nil {
			t.Fatal(err)
		}
		got = nil
		if err := r.Open(sealed, collect); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(batch) {
			t.Fatalf("%d datagrams came out of %d", len(got), len(batch))
		}
		for i := range got {
			if !bytes.Equal(got[i], batch[i]) {
				t.Fatal("a datagram came out changed")
			}
		}
	}
	sealed, _ := s.Seal(nil, keepalive())
	if err := r.Open(sealed[:len(sealed)-1], collect); err == nil {
		t.Fatal("a cut chunk opened")
	}
	if err := r.Open(sealed, collect); err == nil {
		t.Fatal("a stream read on after a chunk that did not open")
	}
}

func TestAStreamChunkHoldsWhatFits(t *testing.T) {
	big := datagram(MaxDatagram, 3)
	batch := make([][]byte, 20)
	for i := range batch {
		batch[i] = big
	}
	n := StreamFits(batch)
	if n != MaxPlain/(2+MaxDatagram) {
		t.Fatalf("%d of the datagrams fit", n)
	}
	s, _ := NewStreamSender(key(1), NewPadder(0, 0))
	sealed, err := s.Seal(nil, batch[:n]...)
	if err != nil || len(sealed) > MaxSealed {
		t.Fatalf("a full chunk of %d bytes: %v", len(sealed), err)
	}
	if _, err := s.Seal(nil, batch[:n+1]...); err == nil {
		t.Fatal("more than fits was sealed")
	}
}
