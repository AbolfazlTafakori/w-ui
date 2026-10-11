package service

import (
	"context"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

func goodAWG() model.AWGParams {
	return model.AWGParams{Jc: 4, Jmin: 40, Jmax: 90, S1: 30, S2: 40, S3: 20, S4: 15,
		H1: 1000, H2: 2000, H3: 3000, H4: 4000}
}

// Every value an operator can type is checked against AmneziaWG's own
// rules, and a refusal names the field it is about.
func TestAWGProfilesAreCheckedAsAmneziaWGChecksThem(t *testing.T) {
	if err := checkAWG(goodAWG(), 1420); err != nil {
		t.Fatalf("a good profile: %v", err)
	}
	cases := []struct {
		name  string
		edit  func(*model.AWGParams)
		mtu   int
		field string
	}{
		{"too many junk packets", func(p *model.AWGParams) { p.Jc = 129 }, 1420, "awg.jc"},
		{"Jmin not below Jmax", func(p *model.AWGParams) { p.Jmin, p.Jmax = 90, 90 }, 1420, "awg.jmin"},
		{"junk past 1280", func(p *model.AWGParams) { p.Jmax = 1300 }, 1420, "awg.jmin"},
		{"handshakes the same size", func(p *model.AWGParams) { p.S2 = p.S1 + 56 }, 1420, "awg.s2"},
		{"S1 past the link", func(p *model.AWGParams) { p.S1 = 1400 }, 1420, "awg.s1"},
		{"S4 too big for MTU 1420", func(p *model.AWGParams) { p.S4 = 21 }, 1420, "awg.s4"},
		{"H at a WireGuard type", func(p *model.AWGParams) { p.H2 = 3 }, 1420, "awg.h2"},
		{"H ranges overlap", func(p *model.AWGParams) { p.H1, p.H1Max = 1000, 2500 }, 1420, "awg.h2"},
		{"a header key that is not a key", func(p *model.AWGParams) { p.HeaderProtectionKey = "nope" }, 1420, "awg.headerProtectionKey"},
		{"header protection with short padding", func(p *model.AWGParams) {
			p.HeaderProtectionKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
			p.S3 = 8
		}, 1420, "awg.s3"},
		{"I1 with text outside a tag", func(p *model.AWGParams) { p.I1 = "hello <r 4>" }, 1420, "awg.i1"},
		{"I2 with a tag one side lacks", func(p *model.AWGParams) { p.I2 = "<c>" }, 1420, "awg.i2"},
		{"I3 with odd hex", func(p *model.AWGParams) { p.I3 = "<b 0xabc>" }, 1420, "awg.i3"},
		{"I4 without 0x", func(p *model.AWGParams) { p.I4 = "<b abcd>" }, 1420, "awg.i4"},
		{"I5 too big for the link", func(p *model.AWGParams) { p.I5 = "<r 1000><r 1000>" }, 1420, "awg.i5"},
		{"I1 unclosed", func(p *model.AWGParams) { p.I1 = "<r 4" }, 1420, "awg.i1"},
		{"I1 with a comment sign", func(p *model.AWGParams) { p.I1 = "<r 4>#x" }, 1420, "awg.i1"},
	}
	for _, c := range cases {
		p := goodAWG()
		c.edit(&p)
		err := checkAWG(p, c.mtu)
		if err == nil || FieldOf(err) != c.field {
			t.Errorf("%s: field %q, want %q (%v)", c.name, FieldOf(err), c.field, err)
		}
	}
	// What fits: S4 at its limit, a range, every tag both sides know.
	p := goodAWG()
	p.S4 = 20
	p.H4, p.H4Max = 4000, 9000
	p.I1 = " <b 0x0102><t> <r 16><rc 8><rd 4> "
	p.HeaderProtectionKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := checkAWG(p, 1420); err != nil {
		t.Fatalf("a profile at its limits: %v", err)
	}
	// A smaller MTU leaves more room for S4.
	p.S4 = 60
	if err := checkAWG(p, 1380); err != nil {
		t.Fatalf("S4 60 at MTU 1380: %v", err)
	}
}

func TestEveryPresetIsAValidPacket(t *testing.T) {
	for _, pr := range AWGPresets() {
		if err := checkSpecial(pr.Value); err != nil {
			t.Errorf("%s: %v", pr.ID, err)
		}
	}
}

func TestDrawnProfilesFitTheDefaultMTU(t *testing.T) {
	for range 5000 {
		if p := NewAWGParams(); checkAWG(p, 1420) != nil {
			t.Fatalf("a drawn profile is refused: %+v: %v", p, checkAWG(p, 1420))
		}
	}
}

// The form edits a tunnel's profile: what is typed is stored as typed,
// trimmed; a new draw keeps the packets the operator chose; a refusal
// changes nothing; ranges and I1-I5 are refused for a node too old.
func TestAnAWGProfileIsEditedFromTheForm(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	ctx := context.Background()
	iface, err := svc.Create(ctx, CreateInterfaceInput{Name: "awg9", Protocol: model.ProtocolWireGuard,
		ListenPort: 41999, Subnet: "10.91.0.0/24", EndpointHost: "vpn.example.com", Mode: model.ModeAmnezia})
	if err != nil {
		t.Skipf("create: %v", err) // a port Windows keeps for itself
	}

	typed := goodAWG()
	typed.H2, typed.H2Max = 2000, 2999
	typed.I1 = "  " + AWGPresets()[1].Value + "  "
	got, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{AWG: &typed})
	if err != nil {
		t.Fatal(err)
	}
	p := got.AWG.V
	if p.I1 != AWGPresets()[1].Value || p.H2Max != 2999 || p.S1 != 30 {
		t.Fatalf("stored %+v", p)
	}

	// A one-value range is stored as the value.
	typed.H3Max = typed.H3
	got, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{AWG: &typed})
	if err != nil || got.AWG.V.H3Max != 0 {
		t.Fatalf("one-value range: %v %+v", err, got)
	}

	// Refused: nothing changes.
	bad := goodAWG()
	bad.S2 = bad.S1 + 56
	if _, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{AWG: &bad}); FieldOf(err) != "awg.s2" {
		t.Fatalf("refusal: %v", err)
	}
	same, _ := svc.Get(ctx, iface.ID)
	if same.AWG.V.S2 != 40 {
		t.Fatal("a refused profile was stored")
	}

	// Drawn again: new numbers, the operator's packets kept.
	keep := same.AWG.V
	got, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{RegenerateAWG: true, AWG: &keep})
	if err != nil {
		t.Fatal(err)
	}
	if got.AWG.V.I1 != keep.I1 || got.AWG.V.H1 == keep.H1 {
		t.Fatalf("drawn again: %+v", got.AWG.V)
	}

	// An MTU that leaves S4 no room is refused.
	big := 1430
	if _, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{MTU: &big}); err == nil || !strings.Contains(err.Error(), "S4") {
		if got.AWG.V.S4 > awgMaxS4(big) {
			t.Fatalf("MTU past S4's room: %v", err)
		}
	}

	// Not on a plain WireGuard tunnel.
	plain, err := svc.Create(ctx, CreateInterfaceInput{Name: "wg9", Protocol: model.ProtocolWireGuard,
		ListenPort: 41998, Subnet: "10.92.0.0/24", EndpointHost: "vpn.example.com"})
	if err == nil {
		if _, err := svc.Update(ctx, plain.ID, UpdateInterfaceInput{AWG: &typed}); FieldOf(err) != "awg" {
			t.Fatalf("plain WireGuard: %v", err)
		}
	}

	// A node on 2.6 cannot carry ranges or I1-I5.
	old := model.Node{Name: "old", Kind: model.KindRemote, Enabled: true, Version: "v2.6.6"}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Interface{}).Where("id = ?", iface.ID).Update("node_id", old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{AWG: &typed}); err == nil || !strings.Contains(err.Error(), "3.0") {
		t.Fatalf("old node: %v", err)
	}
	classic := goodAWG()
	if _, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{AWG: &classic}); err != nil {
		t.Fatalf("a classic profile on an old node: %v", err)
	}
}
