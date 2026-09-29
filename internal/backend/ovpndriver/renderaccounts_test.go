package ovpndriver

import (
	"net/netip"
	"testing"
)

// The accounts written into the server's files come out in username order,
// whatever order the set was built in: the same customers always make the same
// file, so an unchanged panel does not rewrite it and a changed one differs by
// exactly what changed.
func TestAccountsAreRenderedInAStableOrder(t *testing.T) {
	set := accountSet{
		"zara": {Username: "zara", Secret: "s3", IP: netip.MustParseAddr("10.8.0.4")},
		"ali":  {Username: "ali", Secret: "s1", IP: netip.MustParseAddr("10.8.0.2")},
		"mina": {Username: "mina", Secret: "s2", IP: netip.MustParseAddr("10.8.0.3")},
	}
	for range 5 {
		got := toRenderAccounts(set)
		if len(got) != 3 || got[0].Username != "ali" || got[1].Username != "mina" || got[2].Username != "zara" {
			t.Fatalf("rendered %+v, want ali, mina, zara", got)
		}
		if got[0].IP != "10.8.0.2" || got[0].Secret != "s1" {
			t.Errorf("ali rendered as %+v", got[0])
		}
	}
}
