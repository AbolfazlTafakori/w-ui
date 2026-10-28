package stealth

import (
	"bytes"
	"testing"
)

func shapeKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	up, down := make([]byte, 32), make([]byte, 32)
	for i := range up {
		up[i], down[i] = byte(i), byte(128+i)
	}
	return up, down
}

func TestShapeNumbering(t *testing.T) {
	if n, ok := ShapeNumber(0, 0); !ok || n != 0 {
		t.Error("request 0 index 0 is number 0")
	}
	if n, ok := ShapeNumber(1, 0); !ok || n != 4096 {
		t.Error("request 1 index 0 is 4096")
	}
	if n, ok := ShapeNumber(1, 4094); !ok || n != 4096+4094 {
		t.Error("last data index")
	}
	if _, ok := ShapeNumber(1, 4095); ok {
		t.Error("the late index is no data chunk")
	}
	if _, ok := ShapeNumber(1, -1); ok {
		t.Error("a negative index")
	}
	if ShapeLateNumber(1) != 4096+4095 {
		t.Error("late number")
	}
}

func TestShapeListRoundTrip(t *testing.T) {
	up, _ := shapeKeys(t)
	s, err := NewShapeSender(up, NewPadder(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	d1, d2 := keepalive(), keepalive()
	list, err := s.AppendChunk(nil, 7, 0, false, ShapeDataMax, d1, d2)
	if err != nil {
		t.Fatal(err)
	}
	if list, err = s.AppendChunk(list, 7, 1, false, ShapeDataMax); err != nil {
		t.Fatal(err)
	}
	r, err := NewShapeReceiver(up)
	if err != nil {
		t.Fatal(err)
	}
	// It opens at request 7's numbers, and at no other.
	if _, err := r.OpenList(6, list); err == nil {
		t.Error("the list opened as request 6")
	}
	chunks, err := r.OpenList(7, list)
	if err != nil {
		t.Fatalf("open at 7: %v", err)
	}
	if len(chunks) != 2 || len(chunks[0]) != 2 || len(chunks[1]) != 0 {
		t.Fatalf("got %d chunks: %+v", len(chunks), chunks)
	}
	if !bytes.Equal(chunks[0][0], d1) || !bytes.Equal(chunks[0][1], d2) {
		t.Error("the datagrams differ")
	}
}

func TestShapeLateAnswer(t *testing.T) {
	_, down := shapeKeys(t)
	s, _ := NewShapeSender(down, NewPadder(0, 0))
	r, _ := NewShapeReceiver(down)
	late := s.LateAnswer(3)
	if len(late) != ShapeLateSize {
		t.Fatalf("late size %d", len(late))
	}
	if !bytes.Equal(late, s.LateAnswer(3)) {
		t.Error("the late answer is not stable")
	}
	if !r.IsLate(3, late) {
		t.Error("the late answer of 3 is not recognised")
	}
	if r.IsLate(4, late) {
		t.Error("the late answer of 3 passed as 4's")
	}
	if r.IsLate(3, late[:len(late)-1]) {
		t.Error("a short late answer passed")
	}
}

func TestShapeEventRoundTrip(t *testing.T) {
	_, down := shapeKeys(t)
	s, _ := NewShapeSender(down, NewPadder(0, 0))
	d := keepalive()
	ev, err := s.SealEvent(nil, 3, 2, false, d)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := NewShapeReceiver(down)
	number, _ := ShapeNumber(3, 2)
	var got [][]byte
	if err := r.OpenEvent(number, ev, func(dg []byte) { got = append(got, dg) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], d) {
		t.Errorf("the event datagram differs: %+v", got)
	}
}

func TestShapeTargetQueryRoundTrip(t *testing.T) {
	up, _ := shapeKeys(t)
	s, _ := NewShapeSender(up, NewPadder(0, 0))
	d := keepalive()
	list, _ := s.AppendChunk(nil, 1, 0, false, ShapeDataMax, d)
	shape, _ := Preset("get-poll")
	const path = "/x/y"
	var id [PollIDLen]byte
	for i := range id {
		id[i] = byte(i)
	}
	parts, ok := BuildParts(shape, path, ShapeQuery(id, 1), list)
	if !ok {
		t.Fatal("over limit")
	}
	line, ok := ParseLine([]byte("GET " + parts.Target + " HTTP/1.1\r\n"))
	if !ok {
		t.Fatal("the target is not a valid line")
	}
	tgt, ok := ReadTarget(shape, []byte(path), line, nil)
	if !ok {
		t.Fatal("the target did not read")
	}
	if tgt.Who.IsOpening || tgt.Who.ID != id || tgt.Who.N != 1 {
		t.Errorf("who wrong: %+v", tgt.Who)
	}
	if !bytes.Equal(tgt.Data(), list) {
		t.Error("the data read back differs from the list")
	}
	// A wrong path, or an extra parameter, does not read.
	bad, _ := ParseLine([]byte("GET /x/z?" + ShapeQuery(id, 1) + "." + shapeEncode(list) + " HTTP/1.1\r\n"))
	if _, ok := ReadTarget(shape, []byte(path), bad, nil); ok {
		t.Error("a wrong path read")
	}
}

func TestShapeTargetPathSegments(t *testing.T) {
	up, _ := shapeKeys(t)
	s, _ := NewShapeSender(up, NewPadder(0, 0))
	big := make([]byte, 0)
	for i := 0; i < 3; i++ {
		var err error
		if big, err = s.AppendChunk(big, 2, i, false, ShapeDataMax, keepalive()); err != nil {
			t.Fatal(err)
		}
	}
	limit := uint64(8000)
	shape, _ := Resolve(Given{Method: "GET", Place: "path", Mode: "poll", Limit: &limit})
	const path = "/assets"
	var id [PollIDLen]byte
	parts, ok := BuildParts(shape, path, ShapeQuery(id, 2), big)
	if !ok {
		t.Fatal("over limit")
	}
	line, ok := ParseLine([]byte("GET " + parts.Target + " HTTP/1.1\r\n"))
	if !ok {
		t.Fatalf("path line not valid: %q", parts.Target)
	}
	tgt, ok := ReadTarget(shape, []byte(path), line, nil)
	if !ok {
		t.Fatal("path target did not read")
	}
	if !bytes.Equal(tgt.Data(), big) {
		t.Error("path data differs")
	}
}

func TestShapeFramed(t *testing.T) {
	up, _ := shapeKeys(t)
	s, _ := NewShapeSender(up, NewPadder(0, 0))
	list, _ := s.AppendChunk(nil, 1, 0, false, ShapeDataMax, keepalive())
	if n, ok := ShapeFramed(list); !ok || n != 1 {
		t.Errorf("framed %d %v", n, ok)
	}
	if _, ok := ShapeFramed(nil); ok {
		t.Error("an empty list is not framed")
	}
	if _, ok := ShapeFramed([]byte{0, 5}); ok {
		t.Error("a length past the end is not framed")
	}
}

func TestShapeListEndsSession(t *testing.T) {
	up, _ := shapeKeys(t)
	// A chunk that opens but holds a bad item ends the session. Build it by
	// sealing a plaintext whose framing is invalid.
	s, _ := NewShapeSender(up, NewPadder(0, 0))
	bad := []byte{0, 0}                    // a zero-length item mid-plaintext
	bad = append(bad, make([]byte, 40)...) // then non-zero padding region
	bad[5] = 1                             // a non-zero byte after a zero length
	number, _ := ShapeNumber(1, 0)
	sealed := s.aead.Seal(nil, streamNonce(number), bad, nil)
	framed := append([]byte{byte(len(sealed) >> 8), byte(len(sealed))}, sealed...)
	r, _ := NewShapeReceiver(up)
	if _, err := r.OpenList(1, framed); err != ErrBodyEnded {
		t.Fatalf("want ErrBodyEnded, got %v", err)
	}
	if !r.Ended() {
		t.Error("the receiver is not ended")
	}
	if _, err := r.OpenList(2, framed); err != ErrBodyEnded {
		t.Error("a later list is not refused")
	}
}
