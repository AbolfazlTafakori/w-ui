package routing

import (
	"net/netip"
	"strings"
	"testing"
)

// With the default outbound configured but down, customers' unmarked
// traffic is dropped rather than sent from the server's own address; what a
// rule marked for a working hop still passes.
func TestFailClosedDropsWhatNothingClaimed(t *testing.T) {
	p := Policy{
		CustomerNets:  []netip.Prefix{netip.MustParsePrefix("10.66.0.0/24")},
		DropUnmatched: true,
	}
	prog, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "ip saddr @customers4 meta mark and 0x00ff0000 != 0x00a70000 counter drop"
	if !strings.Contains(prog, want) {
		t.Fatalf("expected the fail-closed drop in the block chain:\n%s", prog)
	}
	p.DropUnmatched = false
	prog, _ = BuildRuleset(p)
	if strings.Contains(prog, "!= 0x00a70000 counter drop") {
		t.Fatal("nothing should be dropped when failing closed is off")
	}
}
