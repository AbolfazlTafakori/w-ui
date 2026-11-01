package stealth

import (
	"bytes"
	"testing"
)

func TestFakeFramesRoundTrip(t *testing.T) {
	d1 := bytes.Repeat([]byte{0x41}, 40)
	d2 := bytes.Repeat([]byte{0x42}, 100)
	// Two frames in one record plaintext: one with two datagrams, one empty
	// (padding only).
	var plain []byte
	var ok bool
	if plain, ok = AppendFakeFrame(plain, 7, d1, d2); !ok {
		t.Fatal("frame 1 did not fit")
	}
	if plain, ok = AppendFakeFrame(plain, 3); !ok {
		t.Fatal("frame 2 did not fit")
	}
	var got [][]byte
	n, err := ReadFakeFrames(plain, func(d []byte) { got = append(got, append([]byte(nil), d...)) })
	if err != nil {
		t.Fatal(err)
	}
	if n != len(plain) {
		t.Fatalf("consumed %d of %d", n, len(plain))
	}
	if len(got) != 2 || !bytes.Equal(got[0], d1) || !bytes.Equal(got[1], d2) {
		t.Fatalf("datagrams differ: %d", len(got))
	}
}

func TestFakeFramesSplitAcrossRecords(t *testing.T) {
	d := bytes.Repeat([]byte{0x43}, 200)
	full, _ := AppendFakeFrame(nil, 0, d)
	// Feed all but the last 5 bytes: no whole frame yet, nothing consumed.
	n, err := ReadFakeFrames(full[:len(full)-5], func([]byte) { t.Fatal("a partial frame yielded a datagram") })
	if err != nil || n != 0 {
		t.Fatalf("partial frame: consumed %d, err %v", n, err)
	}
	// The whole thing reads.
	var got [][]byte
	n, err = ReadFakeFrames(full, func(x []byte) { got = append(got, x) })
	if err != nil || n != len(full) || len(got) != 1 || !bytes.Equal(got[0], d) {
		t.Fatalf("whole frame: %d %v %d", n, err, len(got))
	}
}
