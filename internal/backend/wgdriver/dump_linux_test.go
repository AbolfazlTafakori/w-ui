//go:build linux

package wgdriver

import "testing"

// A restart keeps what the device holds: its peers are read back from the
// dump, with their preshared keys and addresses, and written with them.
func TestHeldPeersAreReadBackFromTheDump(t *testing.T) {
	raw := "privkey\tpubkey\t51820\toff\n" +
		"AAA=\tPSK=\t203.0.113.9:4444\t10.66.0.2/32\t1700000000\t10\t20\toff\n" +
		"BBB=\t(none)\t(none)\t10.66.0.3/32\t0\t0\t0\toff\n" +
		"CCC=\t(none)\t(none)\t(none)\t0\t0\t0\toff\n"
	got := peersFromDump(raw)
	if len(got) != 2 {
		t.Fatalf("peers = %+v, want the two with an address", got)
	}
	if got[0].PublicKey != "AAA=" || got[0].PresharedKey != "PSK=" || got[0].AllowedIP != "10.66.0.2/32" {
		t.Fatalf("first peer = %+v", got[0])
	}
	if got[1].PublicKey != "BBB=" || got[1].PresharedKey != "" || got[1].AllowedIP != "10.66.0.3/32" {
		t.Fatalf("second peer = %+v", got[1])
	}
}

func TestADiffThatChangesNothingIsEmpty(t *testing.T) {
	if !(diff{}).empty() {
		t.Fatal("an empty diff is not empty")
	}
	if (diff{remove: []string{"x"}}).empty() {
		t.Fatal("a removal is a change")
	}
}
