package egress

import (
	"context"
	"net/netip"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// newSplitFixture builds a routing service with one real exit named "vpn" and
// returns it together with that exit's mark.
func newSplitFixture(t *testing.T) (*Routing, uint32) {
	t.Helper()
	db := testDB(t)
	// A shared in-memory database lives in one connection; a pool that opens a
	// second hands back a connection that never saw the migration. Pin it so
	// the writes and reads below land in the same place.
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	outs := NewOutbounds(db, NewTuning(), quietLog())
	ob, err := outs.Create(context.Background(), OutboundInput{
		Tag: "vpn", Kind: model.OutboundSOCKS, Address: "198.18.0.9:1080",
	})
	if err != nil {
		t.Fatalf("create exit: %v", err)
	}
	if ob.Mark == 0 {
		t.Fatalf("exit got no mark")
	}
	return NewRouting(db, NewTuning(), quietLog()), ob.Mark
}

func TestSplitTunnelValidation(t *testing.T) {
	ctx := context.Background()
	rt, _ := newSplitFixture(t)

	base := func() BasicRouting {
		return BasicRouting{DefaultOutbound: "vpn", SplitMode: SplitInclude, SplitList: []string{"203.0.113.0/24"}, SplitOutbound: "vpn"}
	}

	cases := []struct {
		name string
		in   BasicRouting
		want wantErr
	}{
		{"bad mode", func() BasicRouting { b := base(); b.SplitMode = "sideways"; return b }(),
			wantErr{"splitMode", `"sideways" is not a split-tunnel mode`}},
		{"blocked tunnel", func() BasicRouting { b := base(); b.SplitOutbound = TagBlocked; return b }(),
			wantErr{"splitOutbound", "the tunnel cannot be the blocked outbound"}},
		{"unknown tunnel", func() BasicRouting { b := base(); b.SplitOutbound = "ghost"; return b }(),
			wantErr{"splitOutbound", `there is no outbound or balancer called "ghost"`}},
		{"bad domain in list", func() BasicRouting { b := base(); b.SplitList = []string{"not a domain"}; return b }(),
			wantErr{"splitList", "is not a domain name or an address"}},
	}
	for _, c := range cases {
		_, err := rt.SaveBasic(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

func TestSplitTunnelRoundTrip(t *testing.T) {
	ctx := context.Background()
	rt, _ := newSplitFixture(t)

	in := BasicRouting{
		DefaultOutbound: "vpn",
		SplitMode:       SplitExclude,
		SplitList:       []string{"203.0.113.0/24", "example.com", "geoip:private"},
		SplitOutbound:   "vpn",
	}
	if _, err := rt.SaveBasic(ctx, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := rt.Basic(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SplitMode != SplitExclude {
		t.Errorf("mode %q, want exclude", got.SplitMode)
	}
	if got.SplitOutbound != "vpn" {
		t.Errorf("tunnel %q, want vpn", got.SplitOutbound)
	}
	if len(got.SplitList) != 3 {
		t.Errorf("list %v, want three entries", got.SplitList)
	}

	// An unknown or empty mode is read back as off, not an error.
	if _, err := rt.SaveBasic(ctx, BasicRouting{DefaultOutbound: "vpn", SplitMode: SplitOff}); err != nil {
		t.Fatalf("save off: %v", err)
	}
	if got, _ := rt.Basic(ctx); got.SplitMode != SplitOff {
		t.Errorf("mode %q, want off", got.SplitMode)
	}
}

func TestSplitTunnelPolicy(t *testing.T) {
	ctx := context.Background()
	rt, mark := newSplitFixture(t)

	listed := netip.MustParsePrefix("203.0.113.5/32")
	covers := func(set []netip.Prefix, addr netip.Addr) bool {
		for _, p := range set {
			if p.Contains(addr) {
				return true
			}
		}
		return false
	}

	// Exclude: the list is pinned direct and the tunnel carries the rest.
	if _, err := rt.SaveBasic(ctx, BasicRouting{
		DefaultOutbound: "vpn", SplitMode: SplitExclude,
		SplitList: []string{"203.0.113.0/24"}, SplitOutbound: "vpn",
	}); err != nil {
		t.Fatalf("save exclude: %v", err)
	}
	p, err := rt.Policy(ctx)
	if err != nil {
		t.Fatalf("policy exclude: %v", err)
	}
	if !covers(p.DirectAddrs, listed.Addr()) {
		t.Errorf("exclude: %v not pinned direct", listed)
	}
	if p.DefaultMark != mark {
		t.Errorf("exclude: default mark 0x%x, want the tunnel 0x%x", p.DefaultMark, mark)
	}
	if p.DropUnmatched {
		t.Errorf("exclude: nothing should be dropped wholesale")
	}

	// Include: the list goes to the tunnel ahead of everything, the rest drops.
	if _, err := rt.SaveBasic(ctx, BasicRouting{
		DefaultOutbound: "vpn", SplitMode: SplitInclude,
		SplitList: []string{"203.0.113.0/24"}, SplitOutbound: "vpn",
	}); err != nil {
		t.Fatalf("save include: %v", err)
	}
	p, err = rt.Policy(ctx)
	if err != nil {
		t.Fatalf("policy include: %v", err)
	}
	if !p.DropUnmatched {
		t.Errorf("include: everything off the list should drop")
	}
	if p.DefaultMark != 0 {
		t.Errorf("include: default mark 0x%x, want none", p.DefaultMark)
	}
	if len(p.Rules) == 0 {
		t.Fatalf("include: no rule generated for the list")
	}
	first := p.Rules[0]
	if first.Mark != mark || first.Drop {
		t.Errorf("include: first rule mark 0x%x drop %v, want the tunnel mark", first.Mark, first.Drop)
	}
	if !covers(first.Addrs, listed.Addr()) {
		t.Errorf("include: first rule does not cover %v", listed)
	}
}

func TestSplitTunnelTestRoute(t *testing.T) {
	ctx := context.Background()
	rt, _ := newSplitFixture(t)

	save := func(mode string) {
		if _, err := rt.SaveBasic(ctx, BasicRouting{
			DefaultOutbound: "vpn", SplitMode: mode,
			SplitList: []string{"203.0.113.0/24"}, SplitOutbound: "vpn",
		}); err != nil {
			t.Fatalf("save %s: %v", mode, err)
		}
	}

	cases := []struct {
		name        string
		mode        string
		target      string
		wantOut     string
		wantBlocked bool
	}{
		{"exclude listed goes direct", SplitExclude, "203.0.113.5", TagDirect, false},
		{"exclude other goes tunnel", SplitExclude, "198.51.100.7", "vpn", false},
		{"include listed goes tunnel", SplitInclude, "203.0.113.5", "vpn", false},
		{"include other is dropped", SplitInclude, "198.51.100.7", TagBlocked, true},
	}
	for _, c := range cases {
		save(c.mode)
		ans, err := rt.TestRoute(ctx, RouteTest{Target: c.target})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if ans.Outbound != c.wantOut || ans.Blocked != c.wantBlocked {
			t.Errorf("%s: got %s (blocked %v), want %s (blocked %v)",
				c.name, ans.Outbound, ans.Blocked, c.wantOut, c.wantBlocked)
		}
	}
}
