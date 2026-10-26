package egress

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A tunnel chosen on an outbound's or a balancer's form leaves through it,
// through a routing rule; moved to another, it leaves the first; and the
// operator's own rules are never touched.
func TestTunnelsChosenOnAForm(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // one in-memory database, not one per connection
	outs := NewOutbounds(db, nil, quietLog())
	if err := outs.EnsureBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	rt := NewRouting(db, nil, quietLog())
	for _, ob := range []OutboundInput{
		{Tag: "de", Kind: "socks", Address: "1.2.3.4:1080"},
		{Tag: "nl", Kind: "socks", Address: "1.2.3.5:1080"},
	} {
		if _, err := outs.Create(ctx, ob); err != nil {
			t.Fatal(err)
		}
	}
	for i, name := range []string{"wg0", "wg1", "wg2"} {
		db.Create(&model.Interface{ID: uint(i + 1), NodeID: 1, Name: name, Protocol: model.ProtocolWireGuard,
			Enabled: true, ListenPort: 51820 + i, Subnet: "10.6" + string(rune('0'+i)) + ".0.0/24"})
	}
	// The operator's own rule naming a tunnel, with another criterion.
	if _, err := rt.CreateRule(ctx, RuleInput{Name: "mine", Interfaces: "1", Domains: "example.com", OutboundTag: "nl"}); err != nil {
		t.Fatal(err)
	}

	tunnels := func() map[string][]uint {
		t.Helper()
		m, err := rt.TunnelsByTarget(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	same := func(got, want []uint) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if err := rt.SetTunnels(ctx, "de", []uint{1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := tunnels(); !same(got["de"], []uint{1, 2}) || len(got["nl"]) != 0 {
		t.Fatalf("after de=1,2: %v", got)
	}
	if err := rt.SetTunnels(ctx, "nl", []uint{2, 3}); err != nil {
		t.Fatal(err)
	}
	if got := tunnels(); !same(got["de"], []uint{1}) || !same(got["nl"], []uint{2, 3}) {
		t.Fatalf("a tunnel moved to nl stayed on de: %v", got)
	}
	if err := rt.SetTunnels(ctx, "de", nil); err != nil {
		t.Fatal(err)
	}
	if got := tunnels(); len(got["de"]) != 0 {
		t.Fatalf("de still has tunnels: %v", got)
	}

	var rules []model.RoutingRule
	db.Order("position, id").Find(&rules)
	var names []string
	for _, r := range rules {
		names = append(names, r.Name+"->"+r.OutboundTag+"["+r.Interfaces+"]")
	}
	if len(rules) != 2 || rules[0].Name != "mine" || rules[0].Interfaces != "1" || rules[0].Domains != "example.com" {
		t.Fatalf("rules: %v", names)
	}

	if err := rt.SetTunnels(ctx, "de", []uint{99}); err == nil {
		t.Fatal("a tunnel that does not exist was accepted")
	}
}
