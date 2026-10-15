package pptpd

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// A frame framed for pppd comes back the same out of the deframer, however
// the bytes are cut on the way, and every byte that needs it is escaped.
func TestFramesSurviveTheTerminal(t *testing.T) {
	frames := [][]byte{
		{0xff, 0x03, 0xc0, 0x21, 0x01, 0x01, 0x00, 0x04},             // LCP, address and control
		{0x21, 0x45, 0x00, 0x7e, 0x7d, 0x00, 0x11, 0x13, 0x20, 0xff}, // flag, escape, controls
		bytes.Repeat([]byte{0x7e}, 1500),                             // all flags: twice the size on the line
	}
	var line []byte
	for _, f := range frames {
		line = appendFramed(line, f)
	}
	for _, c := range line[1 : len(line)-1] {
		if c < 0x20 && c != hdlcFlag {
			t.Fatalf("control byte %#x left unescaped", c)
		}
	}
	for _, step := range []int{1, 3, 7, 64, len(line)} {
		var d deframer
		var got [][]byte
		for i := 0; i < len(line); i += step {
			d.write(line[i:min(i+step, len(line))], func(f []byte) { got = append(got, bytes.Clone(f)) })
		}
		if len(got) != len(frames) {
			t.Fatalf("in pieces of %d: %d frames, want %d", step, len(got), len(frames))
		}
		for i := range frames {
			if !bytes.Equal(got[i], frames[i]) {
				t.Fatalf("in pieces of %d: frame %d differs", step, i)
			}
		}
	}
}

// The check sequence is the standard one: the RFC 1662 sample, and a frame
// pppd would accept with its check appended.
func TestTheCheckSequenceIsPPPs(t *testing.T) {
	frame := []byte{0xff, 0x03, 0xc0, 0x21, 0x09, 0x01, 0x00, 0x08, 0x12, 0x34, 0x56, 0x78}
	fcs := ^fcs16(fcsInit, frame)
	withFCS := append(bytes.Clone(frame), byte(fcs), byte(fcs>>8))
	if fcs16(fcsInit, withFCS) != fcsGood {
		t.Fatal("a frame and its own check do not check out")
	}
}

// A damaged frame is dropped and the next one still arrives; so does one
// too long to be a frame, and the garbage before the first flag.
func TestDamagedFramesAreDropped(t *testing.T) {
	good := appendFramed(nil, []byte{0xc0, 0x21, 0x05, 0x02, 0x00, 0x04})
	bad := bytes.Clone(good)
	bad[3] ^= 0x01
	long := append([]byte{hdlcFlag}, bytes.Repeat([]byte{0x41}, maxFrame+10)...)
	var line []byte
	line = append(line, 0x41, 0x42) // before any flag
	line = append(line, bad...)
	line = append(line, long...)
	line = append(line, good...)
	var d deframer
	var got int
	d.write(line, func(f []byte) {
		got++
		if !bytes.Equal(f, []byte{0xc0, 0x21, 0x05, 0x02, 0x00, 0x04}) {
			t.Fatalf("wrong frame %x", f)
		}
	})
	if got != 1 {
		t.Fatalf("%d frames, want only the good one", got)
	}
}

// GRE as RFC 2637 lays it out, read back the same; and packets of GRE's
// other uses are not taken for PPTP's.
func TestGREIsPPTPs(t *testing.T) {
	cases := []grePacket{
		{callID: 0x1234, seq: 7, hasSeq: true, payload: []byte{0xff, 0x03, 0x00, 0x21}},
		{callID: 9, seq: 8, hasSeq: true, ack: 3, hasAck: true, payload: []byte{1}},
		{callID: 9, ack: 0xffffffff, hasAck: true},
	}
	for _, p := range cases {
		b := appendGRE(nil, p)
		if b[0]&greFlagKey == 0 || b[1]&0x07 != 1 || binary.BigEndian.Uint16(b[2:]) != greProtoPPP {
			t.Fatalf("header %x", b[:4])
		}
		got, err := parseGRE(b)
		if err != nil {
			t.Fatal(err)
		}
		if got.callID != p.callID || got.hasSeq != p.hasSeq || got.seq != p.seq ||
			got.hasAck != p.hasAck || got.ack != p.ack || !bytes.Equal(got.payload, p.payload) {
			t.Fatalf("got %+v, want %+v", got, p)
		}
	}
	plainGRE := []byte{0x00, 0x00, 0x08, 0x00, 0x45, 0, 0, 0}
	if _, err := parseGRE(plainGRE); err == nil {
		t.Fatal("plain GRE taken for PPTP")
	}
	short := appendGRE(nil, grePacket{callID: 1, seq: 1, hasSeq: true, payload: []byte{1, 2, 3}})
	if _, err := parseGRE(short[:len(short)-1]); err == nil {
		t.Fatal("a payload shorter than its length taken")
	}
}

func TestSequenceNumbersWrap(t *testing.T) {
	if !seqAfter(1, 0xffffffff) || seqAfter(0xffffffff, 1) || seqAfter(5, 5) || !seqAfter(6, 5) {
		t.Fatal("wraparound")
	}
}

// Every message the server writes is the length the RFC gives it, with a
// header that reads back; and headers that are not a control message's, or
// whose length is wrong for their type, are refused.
func TestControlMessages(t *testing.T) {
	for name, b := range map[string][]byte{
		"start reply":     startReply(resultOK, "wui"),
		"stop request":    stopRequest(),
		"stop reply":      stopReply(),
		"echo request":    echoRequest(7),
		"echo reply":      echoReply([]byte{1, 2, 3, 4}),
		"out-call reply":  outCallReply(5, 6, callResultOK, 64),
		"call disconnect": callDisconnect(5, cdnLostCarrier),
	} {
		typ, n, err := parseHeader(b)
		if err != nil || n != len(b) || n != messageLen[typ] {
			t.Fatalf("%s: type %d length %d (%d bytes): %v", name, typ, n, len(b), err)
		}
	}
	r := outCallReply(0xabcd, 0x0102, callResultOK, 64)
	if binary.BigEndian.Uint16(r[12:]) != 0xabcd || binary.BigEndian.Uint16(r[14:]) != 0x0102 || r[16] != 1 ||
		binary.BigEndian.Uint16(r[24:]) != 64 {
		t.Fatalf("out-call reply %x", r[:32])
	}
	e := echoReply([]byte{9, 8, 7, 6})
	if !bytes.Equal(e[12:16], []byte{9, 8, 7, 6}) || e[16] != resultOK {
		t.Fatalf("echo reply %x", e)
	}

	req := make([]byte, 168)
	binary.BigEndian.PutUint16(req[12:], 0x4242) // the device's call id
	binary.BigEndian.PutUint16(req[32:], 16)     // its window
	oc := parseOutCall(req[12:])
	if oc.peerCallID != 0x4242 || oc.window != 16 {
		t.Fatalf("out-call request %+v", oc)
	}

	bad := startReply(resultOK, "x")
	binary.BigEndian.PutUint32(bad[4:], 0xdeadbeef)
	if _, _, err := parseHeader(bad); err == nil {
		t.Fatal("wrong magic cookie taken")
	}
	wrongLen := stopRequest()
	binary.BigEndian.PutUint16(wrongLen[0:], 20)
	if _, _, err := parseHeader(wrongLen); err == nil {
		t.Fatal("a stop request of the wrong length taken")
	}
	unknown := encode(msgEchoReq, nil)
	binary.BigEndian.PutUint16(unknown[8:], 99)
	if _, n, err := parseHeader(unknown); err != nil || n != 16 {
		t.Fatalf("an unknown type of a sane length should be skippable: %v", err)
	}
}
