package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/ipam"
)

// A profile is sized for the server's way out: on a smaller link the tunnel
// MTU is smaller, and every packet -- data with its padding, the handshake
// and the packet before it -- still fits it whole.
func TestAProfileFitsTheServersLink(t *testing.T) {
	for _, link := range []int{1500, 1450, 1400, 1280} {
		mtu := awgMTUFor(link)
		if mtu+awgDataOverhead+20+8 > link-20 {
			t.Fatalf("link %d: MTU %d leaves no room for an IPv6 header", link, mtu)
		}
		for range 200 {
			p := drawAWG(mtu, link, AWGModern, awgTaken{pads: map[[2]int]bool{}, specials: map[string]bool{}})
			if err := checkAWG(p, mtu); err != nil {
				t.Fatalf("link %d: %v", link, err)
			}
			if mtu+awgDataOverhead+p.S4+20+8 > link {
				t.Fatalf("link %d, MTU %d: S4 %d splits every data packet", link, mtu, p.S4)
			}
			if p.I1 != "" && specialSize(p.I1)+20+8 > link {
				t.Fatalf("link %d: I1 of %d bytes does not fit", link, specialSize(p.I1))
			}
		}
	}
	if awgMTUFor(1500) != 1420 {
		t.Fatalf("a 1500-byte link: MTU %d, want WireGuard's own 1420", awgMTUFor(1500))
	}
}

func TestThePresetsAreTheSizeTheyClaim(t *testing.T) {
	want := map[string]int{"quic": 1250, "dns": 32, "stun": 20}
	for _, p := range AWGPresets() {
		if got := specialSize(p.Value); got != want[p.ID] {
			t.Errorf("%s: %d bytes, want %d", p.ID, got, want[p.ID])
		}
	}
}

// The form asks with no MTU and is told the one that fits.
func TestTheFormIsToldTheMTUThatFits(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	d, err := svc.DrawAWGFor(context.Background(), 0, 0, 0, AWGModern)
	if err != nil {
		t.Fatal(err)
	}
	if d.LinkMTU < 1280 || d.MTU != awgMTUFor(d.LinkMTU) {
		t.Fatalf("drawn for link %d with MTU %d", d.LinkMTU, d.MTU)
	}
	if err := checkAWG(d.AWGParams, d.MTU); err != nil {
		t.Fatal(err)
	}
}
