package egress

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// An outbound, a balancer and a rule made switched off stay off. The
// schema defaults enabled to true and a create left a false out: each came
// up on, and a rule made off routed traffic. Found by the routing policy's
// tests.
func TestWhatIsMadeSwitchedOffStaysOff(t *testing.T) {
	db := testDB(t)
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	ctx := context.Background()
	tuning := NewTuning()
	obs, bals, rt := NewOutbounds(db, tuning, quietLog()), NewBalancers(db, quietLog()), NewRouting(db, tuning, quietLog())
	on, off := true, false
	if _, err := obs.Create(ctx, OutboundInput{Tag: "on", Kind: model.OutboundSOCKS, Enabled: &on, Address: "198.18.0.9:1080"}); err != nil {
		t.Fatal(err)
	}
	ob, err := obs.Create(ctx, OutboundInput{Tag: "off", Kind: model.OutboundSOCKS, Enabled: &off, Address: "198.18.0.10:1080"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := bals.Create(ctx, BalancerInput{Tag: "lb", Enabled: &off, Members: []string{"on"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := rt.CreateRule(ctx, RuleInput{Name: "r", Enabled: &off, DestIPs: "203.0.113.0/24", OutboundTag: "on"})
	if err != nil {
		t.Fatal(err)
	}
	for what, enabled := range map[string]func() bool{
		"outbound": func() bool { var o model.Outbound; db.First(&o, ob.ID); return o.Enabled },
		"balancer": func() bool { var x model.Balancer; db.First(&x, b.ID); return x.Enabled },
		"rule":     func() bool { var x model.RoutingRule; db.First(&x, r.ID); return x.Enabled },
	} {
		if enabled() {
			t.Errorf("the %s made switched off is stored on", what)
		}
	}
}
