package egress

import (
	"context"
	"net/netip"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/routing"
)

// The parts of Policy that had no test: which tunnels' traffic is the
// panel's to route, where each rule sends it, balancers, an exit that is
// down, and the include mode's allowlist. Held before Policy is split.

type policyFixture struct {
	rt        *Routing
	obs       *Outbounds
	bals      *Balancers
	vpn, vpn2 *model.Outbound
}

func newPolicyFixture(t *testing.T) policyFixture {
	t.Helper()
	db := testDB(t)
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1) // one in-memory database, not one per connection
	}
	ctx := context.Background()
	local := model.Node{Name: "local", Kind: model.KindLocal}
	if err := db.Create(&local).Error; err != nil {
		t.Fatal(err)
	}
	for _, i := range []model.Interface{
		{Name: "wg-on", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51820, Subnet: "10.150.0.0/24", NodeID: local.ID, EndpointHost: "x"},
		{Name: "x-in", Protocol: model.ProtocolXray, Enabled: true, ListenPort: 443, Subnet: "10.151.0.0/24", NodeID: local.ID, EndpointHost: "x"},
		{Name: "wg-off", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51821, Subnet: "10.152.0.0/24", NodeID: local.ID, EndpointHost: "x"},
	} {
		if err := db.Create(&i).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Switched off after the insert: the schema's default is on.
	if err := db.Model(&model.Interface{}).Where("name = ?", "wg-off").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	tuning := NewTuning()
	obs := NewOutbounds(db, tuning, quietLog())
	if err := obs.EnsureBuiltins(ctx); err != nil { // as the panel does at start
		t.Fatal(err)
	}
	on := true
	vpn, err := obs.Create(ctx, OutboundInput{Tag: "vpn", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.18.0.9:1080"})
	if err != nil {
		t.Fatal(err)
	}
	vpn2, err := obs.Create(ctx, OutboundInput{Tag: "vpn2", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.18.0.10:1080"})
	if err != nil {
		t.Fatal(err)
	}
	return policyFixture{rt: NewRouting(db, tuning, quietLog()), obs: obs, bals: NewBalancers(db, quietLog()), vpn: vpn, vpn2: vpn2}
}

func (f policyFixture) policy(t *testing.T) routing.Policy {
	t.Helper()
	got, err := f.rt.Policy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f policyFixture) rule(t *testing.T, name, to string, enabled bool) {
	t.Helper()
	if _, err := f.rt.CreateRule(context.Background(), RuleInput{Name: name, Enabled: &enabled, DestIPs: "203.0.113.0/24", OutboundTag: to}); err != nil {
		t.Fatalf("rule %s: %v", name, err)
	}
}

func (f policyFixture) basic(t *testing.T, change func(*BasicRouting)) {
	t.Helper()
	b, err := f.rt.Basic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	change(&b)
	if _, err := f.rt.SaveBasic(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

// Only a tunnel of this panel's, switched on, has its customers' traffic
// routed here; an Xray inbound carries none through this machine.
func TestPolicyRoutesTheTunnelsThatCarryCustomersHere(t *testing.T) {
	f := newPolicyFixture(t)
	p := f.policy(t)
	if len(p.CustomerNets) != 1 || p.CustomerNets[0] != netip.MustParsePrefix("10.150.0.0/24") ||
		len(p.TunnelDevices) != 1 || p.TunnelDevices[0] != "wg-on" {
		t.Fatalf("nets %v devices %v; want the one tunnel switched on, no Xray inbound, no tunnel switched off",
			p.CustomerNets, p.TunnelDevices)
	}
}

// A rule sends its traffic to its outbound by that outbound's mark; one
// pointing at the blocked outbound drops it; a rule switched off, or one
// pointing at an outbound switched off, is left out -- never applied as a
// drop of the customer's traffic.
func TestPolicyRulesGoWhereTheyPoint(t *testing.T) {
	f := newPolicyFixture(t)
	ctx := context.Background()
	f.rule(t, "to-vpn", "vpn", true)
	f.rule(t, "to-block", TagBlocked, true)
	f.rule(t, "switched-off", "vpn", false)
	f.rule(t, "to-vpn2", "vpn2", true)
	off := false
	if _, err := f.obs.Update(ctx, f.vpn2.ID, OutboundInput{Tag: "vpn2", Kind: model.OutboundSOCKS, Enabled: &off, Address: "198.18.0.10:1080"}); err != nil {
		t.Fatal(err)
	}
	p := f.policy(t)
	if len(p.Rules) != 2 {
		t.Fatalf("rules %+v; want the vpn rule and the block rule alone", p.Rules)
	}
	if p.Rules[0].Mark != f.vpn.Mark || p.Rules[0].Drop || len(p.Rules[0].Addrs) != 1 {
		t.Fatalf("the vpn rule: %+v, want mark %d to 203.0.113.0/24", p.Rules[0], f.vpn.Mark)
	}
	if !p.Rules[1].Drop {
		t.Fatalf("the block rule: %+v, want a drop", p.Rules[1])
	}
}

// A rule pointing at a balancer sends its traffic by the balancer's mark,
// over the balancer's members.
func TestPolicyARuleToABalancerUsesItsMembers(t *testing.T) {
	f := newPolicyFixture(t)
	on := true
	b, err := f.bals.Create(context.Background(), BalancerInput{Tag: "lb", Enabled: &on, Members: []string{"vpn", "vpn2"}})
	if err != nil {
		t.Fatal(err)
	}
	f.rule(t, "to-lb", "lb", true)
	p := f.policy(t)
	var hop *struct{ n int }
	for _, h := range p.Hops {
		if h.Tag == "lb" {
			hop = &struct{ n int }{len(h.Nexthops)}
		}
	}
	if hop == nil || hop.n != 2 {
		t.Fatalf("hops %+v; want the balancer over its two members", p.Hops)
	}
	if len(p.Rules) != 1 || p.Rules[0].Mark != b.Mark {
		t.Fatalf("rules %+v; want the one rule by the balancer's mark %d", p.Rules, b.Mark)
	}
}

// The chosen exit switched off: with fail-closed nothing unmatched leaves
// -- better than every customer suddenly showing the server's own address
// -- and without it the unmatched traffic goes direct.
func TestPolicyAnExitThatIsDownFailsClosedOnlyWhenAsked(t *testing.T) {
	for _, failClosed := range []bool{true, false} {
		f := newPolicyFixture(t)
		ctx := context.Background()
		f.basic(t, func(b *BasicRouting) { b.DefaultOutbound, b.FailClosed = "vpn", failClosed })
		if p := f.policy(t); p.DefaultMark != f.vpn.Mark || p.DropUnmatched {
			t.Fatalf("fail-closed %v, exit up: default %d drop %v; want the exit's mark", failClosed, p.DefaultMark, p.DropUnmatched)
		}
		off := false
		if _, err := f.obs.Update(ctx, f.vpn.ID, OutboundInput{Tag: "vpn", Kind: model.OutboundSOCKS, Enabled: &off, Address: "198.18.0.9:1080"}); err != nil {
			t.Fatal(err)
		}
		p := f.policy(t)
		if p.DefaultMark != 0 || p.DropUnmatched != failClosed {
			t.Fatalf("fail-closed %v, exit down: default %d drop %v", failClosed, p.DefaultMark, p.DropUnmatched)
		}
	}
}

// In include mode only the list goes out, ahead of every rule, and
// everything else is dropped.
func TestPolicyIncludeModeSendsTheListFirstAndDropsTheRest(t *testing.T) {
	f := newPolicyFixture(t)
	f.rule(t, "to-vpn2", "vpn2", true)
	f.basic(t, func(b *BasicRouting) {
		b.SplitMode, b.SplitList, b.SplitOutbound = SplitInclude, []string{"198.51.100.0/24"}, "vpn"
	})
	p := f.policy(t)
	if !p.DropUnmatched {
		t.Fatal("include mode does not drop what the list does not name")
	}
	if len(p.Rules) != 2 || p.Rules[0].Mark != f.vpn.Mark || len(p.Rules[0].Addrs) != 1 ||
		p.Rules[0].Addrs[0] != netip.MustParsePrefix("198.51.100.0/24") {
		t.Fatalf("rules %+v; want the list first, by the tunnel's mark, then the rule", p.Rules)
	}
}
