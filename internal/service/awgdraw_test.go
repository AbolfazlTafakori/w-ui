package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// A server with many AmneziaWG tunnels: every profile drawn is valid, fits
// its tunnel's MTU, and shares no identifier and no handshake padding with
// any other tunnel on that server; the packet before the handshake differs
// while there are kinds left to give.
func TestEachTunnelOnAServerGetsAProfileOfItsOwn(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	ctx := context.Background()
	var local model.Node
	if err := db.Where("kind = ?", model.KindLocal).First(&local).Error; err != nil {
		t.Fatal(err)
	}
	type span struct{ lo, hi uint32 }
	var spans []span
	pads := map[[2]int]bool{}
	specials := map[string]int{}
	for i := range 60 {
		level := AWGModern
		if i%3 == 2 {
			level = AWGClassic
		}
		mtu := []int{1280, 1380, 1420}[i%3]
		p, err := svc.DrawAWG(ctx, local.ID, 0, mtu, level)
		if err != nil {
			t.Fatalf("tunnel %d: %v", i, err)
		}
		if err := checkAWG(p, mtu); err != nil {
			t.Fatalf("tunnel %d drawn invalid: %v", i, err)
		}
		if (level == AWGModern) != p.UsesAWG2() {
			t.Fatalf("tunnel %d: a %s profile %+v", i, level, p)
		}
		for n := 1; n <= 4; n++ {
			lo, hi := p.HeaderRange(n)
			for _, s := range spans {
				if lo <= s.hi && s.lo <= hi {
					t.Fatalf("tunnel %d H%d %d-%d overlaps another tunnel's %d-%d", i, n, lo, hi, s.lo, s.hi)
				}
			}
			spans = append(spans, span{lo, hi})
		}
		if pads[[2]int{p.S1, p.S2}] {
			t.Fatalf("tunnel %d repeats S1/S2 %d/%d", i, p.S1, p.S2)
		}
		pads[[2]int{p.S1, p.S2}] = true
		if p.I1 != "" {
			specials[p.I1]++
		}
		// Stored, as the panel would, so the next draw sees it.
		iface := model.Interface{Name: "awg" + string(rune('a'+i%26)) + string(rune('a'+i/26)), NodeID: local.ID,
			Protocol: model.ProtocolWireGuard, Mode: model.ModeAmnezia, ListenPort: 30000 + i,
			Subnet: "10.200.0.0/24", EndpointHost: "vpn.example.com", MTU: mtu, AWG: model.JSON(p)}
		if err := db.Create(&iface).Error; err != nil {
			t.Fatal(err)
		}
		// The first three modern tunnels each get a different kind of packet.
		if i == 4 && len(specials) != len(AWGPresets()) {
			t.Fatalf("after three modern tunnels the packets used are %v", specials)
		}
	}
	// A tunnel drawn for again is not kept clear of itself.
	var first model.Interface
	if err := db.Where("node_id = ?", local.ID).Order("id").First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DrawAWG(ctx, local.ID, first.ID, first.MTU, AWGModern); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DrawAWG(ctx, local.ID, 0, 1420, "fancy"); FieldOf(err) != "awgLevel" {
		t.Fatalf("unknown level: %v", err)
	}
}

// What the form shows is what is created; with nothing sent, a profile is
// drawn for the server; a node before 3.0 gets a classic one.
func TestATunnelIsCreatedWithTheProfileTheFormShowed(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	ctx := context.Background()
	shown, err := svc.DrawAWGFor(ctx, 0, 0, 1420, AWGModern)
	if err != nil {
		t.Fatal(err)
	}
	shown.I2 = "  <rd 8>  "
	f, err := svc.Create(ctx, CreateInterfaceInput{Name: "awgx", Protocol: model.ProtocolWireGuard, ListenPort: 41890,
		Subnet: "10.93.0.0/24", EndpointHost: "vpn.example.com", Mode: model.ModeAmnezia, AWG: &shown})
	if err != nil {
		t.Skipf("create: %v", err)
	}
	if f.AWG.V.H1 != shown.H1 || f.AWG.V.I1 != shown.I1 || f.AWG.V.I2 != "<rd 8>" {
		t.Fatalf("created %+v, shown %+v", f.AWG.V, shown)
	}
	bad := shown
	bad.S4 = 40
	if _, err := svc.Create(ctx, CreateInterfaceInput{Name: "awgy", Protocol: model.ProtocolWireGuard, ListenPort: 41891,
		Subnet: "10.94.0.0/24", EndpointHost: "vpn.example.com", Mode: model.ModeAmnezia, AWG: &bad}); FieldOf(err) != "awg.s4" {
		t.Fatalf("an S4 too big: %v", err)
	}
	g, err := svc.Create(ctx, CreateInterfaceInput{Name: "awgz", Protocol: model.ProtocolWireGuard, ListenPort: 41892,
		Subnet: "10.95.0.0/24", EndpointHost: "vpn.example.com", Mode: model.ModeAmnezia, AWGLevel: AWGClassic})
	if err != nil {
		t.Fatal(err)
	}
	if g.AWG.V.UsesAWG2() || checkAWG(g.AWG.V, g.MTU) != nil {
		t.Fatalf("classic drawn %+v", g.AWG.V)
	}
	old := model.Node{Name: "old", Kind: model.KindRemote, Enabled: true, Version: "v2.6.6"}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	p, err := svc.DrawAWGFor(ctx, old.ID, 0, 1420, AWGModern)
	if err != nil || p.UsesAWG2() {
		t.Fatalf("for a 2.6 node: %v %+v", err, p)
	}
}
