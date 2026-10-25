package service

import (
	"net/netip"
	"testing"
)

// Only a gateway that is up on this machine counts as one the resolver
// failed on; one not up yet is a tunnel still coming up.
func TestOnlyAGatewayUpHereIsJudged(t *testing.T) {
	lo := netip.MustParseAddr("127.0.0.1")
	away := netip.MustParseAddr("10.254.253.1")
	got := upHere([]netip.Addr{lo, away})
	if len(got) != 1 || got[0] != lo {
		t.Fatalf("up here: %v, want only the loopback", got)
	}
}
