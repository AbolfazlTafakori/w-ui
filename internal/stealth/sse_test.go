package stealth

import (
	"bytes"
	"testing"
)

// sseBody writes each event text as a transfer-coding chunk of its own, and
// the last chunk when last.
func sseBody(texts [][]byte, last bool) []byte {
	var out []byte
	for _, t := range texts {
		out = SseChunked(out, t)
	}
	if last {
		out = append(out, SseEnd...)
	}
	return out
}

func TestSseReaderReadsEvents(t *testing.T) {
	_, down := shapeKeys(t)
	s, _ := NewShapeSender(down, NewPadder(0, 0))
	d := keepalive()
	var texts [][]byte
	var want [][]byte
	for i := 0; i < 3; i++ {
		ev, err := s.SealEvent(nil, 5, i, false, d)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, ev)
		texts = append(texts, SseEvent(ev))
		if i == 1 {
			texts = append(texts, SseBeat()) // a heartbeat between events
		}
	}
	body := sseBody(texts, true)

	// Whole, then in one-byte pieces: the result is the same either way.
	for _, size := range []int{len(body), 1, 7} {
		r := NewSseReader()
		var out [][]byte
		var done bool
		var err error
		for at := 0; at < len(body); at += size {
			end := at + size
			if end > len(body) {
				end = len(body)
			}
			done, err = r.Feed(body[at:end], &out)
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
		}
		if !done {
			t.Fatalf("size %d: not done", size)
		}
		if len(out) != len(want) {
			t.Fatalf("size %d: %d events, want %d", size, len(out), len(want))
		}
		for i := range want {
			if !bytes.Equal(out[i], want[i]) {
				t.Errorf("size %d: event %d differs", size, i)
			}
		}
		// Every sealed chunk opens at its event number under the down key.
		rec, _ := NewShapeReceiver(down)
		for i, sealed := range out {
			number, _ := ShapeNumber(5, i)
			var got [][]byte
			if err := rec.OpenEvent(number, sealed, func(dg []byte) { got = append(got, dg) }); err != nil {
				t.Errorf("event %d does not open: %v", i, err)
			} else if len(got) != 1 || !bytes.Equal(got[0], d) {
				t.Errorf("event %d datagram differs", i)
			}
		}
	}
}

func TestSseReaderFaults(t *testing.T) {
	bad := map[string][]byte{
		"a byte behind the last chunk": append(sseBody(nil, true), 'x'),
		"a bare line that is no event": sseBody([][]byte{[]byte("hello\n\n")}, false),
		"an event with bad base64":     sseBody([][]byte{[]byte("data: not*base64==\n\n")}, false),
	}
	for name, body := range bad {
		r := NewSseReader()
		var out [][]byte
		_, err := r.Feed(body, &out)
		if err == nil {
			t.Errorf("%s: no fault", name)
			continue
		}
		// A fault is final.
		if _, err2 := r.Feed([]byte{0}, &out); err2 != err {
			t.Errorf("%s: fault not final", name)
		}
	}
}

func TestSseEventTextForm(t *testing.T) {
	ev := SseEvent([]byte("0123456789abcdef"))
	if !bytes.HasPrefix(ev, []byte("data: ")) || !bytes.HasSuffix(ev, []byte("\n\n")) {
		t.Error("event form wrong")
	}
}
