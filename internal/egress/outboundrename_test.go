package egress

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/listfield"
)

// An outbound renamed takes everything that points at it along: its rules,
// and the balancers that have it as a member or as their fallback. Found by
// a test of the configuration template: the balancers were left pointing
// at a name no outbound has any longer, and refused every change after.
func TestARenamedOutboundTakesItsBalancersAlong(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	tuning := NewTuning()
	obs, bals := NewOutbounds(db, tuning, quietLog()), NewBalancers(db, quietLog())
	on := true
	a, err := obs.Create(ctx, OutboundInput{Tag: "socks-a", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.51.100.7:1080"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := obs.Create(ctx, OutboundInput{Tag: "socks-b", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.51.100.8:1080"}); err != nil {
		t.Fatal(err)
	}
	// "socks-ab" shares a prefix: only whole names may move.
	if _, err := obs.Create(ctx, OutboundInput{Tag: "socks-ab", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.51.100.9:1080"}); err != nil {
		t.Fatal(err)
	}
	if _, err := bals.Create(ctx, BalancerInput{Tag: "pair", Enabled: &on, Members: []string{"socks-a", "socks-b", "socks-ab"}, Fallback: "socks-a"}); err != nil {
		t.Fatal(err)
	}

	if _, err := obs.Update(ctx, a.ID, OutboundInput{Tag: "socks-renamed", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.51.100.7:1080"}); err != nil {
		t.Fatal(err)
	}
	var b model.Balancer
	if err := db.Where("tag = ?", "pair").First(&b).Error; err != nil {
		t.Fatal(err)
	}
	members := listfield.Split(b.Members)
	if len(members) != 3 || members[0] != "socks-renamed" || members[1] != "socks-b" || members[2] != "socks-ab" {
		t.Fatalf("members after the rename: %q; want socks-renamed, socks-b, socks-ab", members)
	}
	if b.Fallback != "socks-renamed" {
		t.Fatalf("fallback after the rename: %q, want socks-renamed", b.Fallback)
	}
	// And the balancer can still be changed: nothing it names is missing.
	views, err := bals.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in := BalancerInput{Tag: "pair", Enabled: &on, Members: members, Fallback: b.Fallback}
	if _, err := bals.Update(ctx, views[0].ID, in); err != nil {
		t.Fatalf("the balancer refuses a change after the rename: %v", err)
	}
}

// The split tunnel's exit follows an outbound's rename too: it is stored by
// name, and left behind it named an exit nothing had while the split was on.
// (The default exit is not stored -- it is the first outbound in the list --
// so it cannot be left behind.)
func TestTheSplitTunnelsExitFollowsARename(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	tuning := NewTuning()
	obs, rt := NewOutbounds(db, tuning, quietLog()), NewRouting(db, tuning, quietLog())
	on := true
	a, err := obs.Create(ctx, OutboundInput{Tag: "tunnel-a", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.51.100.7:1080"})
	if err != nil {
		t.Fatal(err)
	}
	basic, err := rt.Basic(ctx)
	if err != nil {
		t.Fatal(err)
	}
	basic.SplitMode, basic.SplitList, basic.SplitOutbound = SplitInclude, []string{"example.com"}, "tunnel-a"
	if _, err := rt.SaveBasic(ctx, basic); err != nil {
		t.Fatal(err)
	}
	if _, err := obs.Update(ctx, a.ID, OutboundInput{Tag: "tunnel-b", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.51.100.7:1080"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := rt.Basic(ctx); got.SplitOutbound != "tunnel-b" {
		t.Fatalf("the split tunnel's exit after the rename: %q, want tunnel-b", got.SplitOutbound)
	}
}
