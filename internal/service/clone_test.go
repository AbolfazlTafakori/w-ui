package service

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

func newInterfaces(t *testing.T, db *gorm.DB) *Interfaces {
	t.Helper()
	// Every interface belongs to a node, and creating one resolves this panel's
	// own entry. A fresh test database has no rows at all.
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	return NewInterfaces(db, ipam.NewPools(), quietLog())
}

// seedAmnezia makes the kind of tunnel worth copying: obfuscated, with a dozen
// settings nobody wants to retype.
func seedAmnezia(t *testing.T, db *gorm.DB) (*Interfaces, *model.Interface) {
	t.Helper()

	svc := newInterfaces(t, db)
	iface, err := svc.Create(context.Background(), CreateInterfaceInput{
		Name:         "wg0",
		Protocol:     model.ProtocolWireGuard,
		ListenPort:   51820,
		Subnet:       "10.66.0.0/24",
		EndpointHost: "vpn.example.com",
		MTU:          1380,
		DNS:          "1.1.1.1",
		NATInterface: "eth0",
		Mode:         model.ModeAmnezia,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return svc, iface
}

// The point of copying: the settings that make the tunnel behave as it does
// come across, so the copy is not subtly different from the original in a way
// found later by a customer who cannot connect.
func TestACopyKeepsHowTheTunnelBehaves(t *testing.T) {
	db := testDB(t)
	svc, src := seedAmnezia(t, db)

	copy, err := svc.Clone(context.Background(), src.ID, CloneInput{
		Name: "wg1", ListenPort: 51821, Subnet: "10.67.0.0/24",
	})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if copy.Protocol != src.Protocol {
		t.Errorf("protocol = %v, want %v", copy.Protocol, src.Protocol)
	}
	if copy.MTU != src.MTU {
		t.Errorf("mtu = %d, want %d", copy.MTU, src.MTU)
	}
	if copy.DNS != src.DNS {
		t.Errorf("dns = %q, want %q", copy.DNS, src.DNS)
	}
	if copy.NATInterface != src.NATInterface {
		t.Errorf("nat interface = %q, want %q", copy.NATInterface, src.NATInterface)
	}
	if copy.Mode != src.Mode {
		t.Errorf("mode = %v, want %v", copy.Mode, src.Mode)
	}
	// Not asked for, so it comes from the original: a copy made to sit beside
	// it is reached at the same address.
	if copy.EndpointHost != src.EndpointHost {
		t.Errorf("endpoint = %q, want %q", copy.EndpointHost, src.EndpointHost)
	}
}

// Identity is never copied. Two tunnels sharing a server key would be two
// servers a customer cannot tell apart, and two sharing a subnet would hand the
// same address to two people.
func TestACopyGetsItsOwnIdentity(t *testing.T) {
	db := testDB(t)
	svc, src := seedAmnezia(t, db)

	copy, err := svc.Clone(context.Background(), src.ID, CloneInput{
		Name: "wg1", ListenPort: 51821, Subnet: "10.67.0.0/24",
	})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	if copy.ID == src.ID {
		t.Fatal("the copy is the original")
	}
	if copy.PublicKey == src.PublicKey || copy.PrivateKey == src.PrivateKey {
		t.Error("the copy shares the original's server key")
	}
	if copy.Subnet == src.Subnet {
		t.Error("the copy shares the original's address range")
	}
	if copy.ListenPort == src.ListenPort {
		t.Error("the copy shares the original's port")
	}
	if copy.Name == src.Name {
		t.Error("the copy shares the original's name")
	}
}

// The obfuscation profile is deliberately fresh.
//
// Copying it looks helpful and is the opposite: the reason for a second tunnel
// is that the first is being blocked, and two tunnels with the same S1-S4 and
// H1-H4 look identical to whatever is doing the blocking. A shared profile
// means both blocked by the same rule on the same day.
func TestACopyIsNotBlockedByTheSameRuleAsTheOriginal(t *testing.T) {
	db := testDB(t)
	svc, src := seedAmnezia(t, db)

	copy, err := svc.Clone(context.Background(), src.ID, CloneInput{
		Name: "wg1", ListenPort: 51821, Subnet: "10.67.0.0/24",
	})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	a, b := src.AWG.V, copy.AWG.V
	if a == (model.AWGParams{}) {
		t.Fatal("the original has no obfuscation profile, so this proves nothing")
	}
	if a == b {
		t.Error("the copy carries the original's obfuscation profile; " +
			"one rule would block both, which is what the copy exists to avoid")
	}
}

// Customers are not copied. A copy is an empty tunnel that behaves like the
// original; giving it the original's customers would double every allocation
// without anybody asking.
func TestACopyStartsEmpty(t *testing.T) {
	db := testDB(t)
	svc, src := seedAmnezia(t, db)

	copy, err := svc.Clone(context.Background(), src.ID, CloneInput{
		Name: "wg1", ListenPort: 51821, Subnet: "10.67.0.0/24",
	})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}

	var accounts int64
	if err := db.Model(&model.Account{}).Where("interface_id = ?", copy.ID).
		Count(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if accounts != 0 {
		t.Errorf("the copy came with %d devices on it", accounts)
	}
}

// A tunnel this panel does not own is one the central panel would overwrite on
// its next sync, so a copy of it would be a tunnel nobody manages.
func TestATunnelBelongingToAnotherPanelIsNotCopied(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)

	managed := model.Interface{
		Name: "wg9", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51829,
		Subnet: "10.69.0.0/24", EndpointHost: "vpn.example.com", MTU: 1420,
		Managed: true, OriginID: 4,
	}
	if err := db.Create(&managed).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Clone(context.Background(), managed.ID, CloneInput{
		Name: "wg10", ListenPort: 51830, Subnet: "10.70.0.0/24",
	}); err == nil {
		t.Fatal("a tunnel owned by another panel was copied")
	}
}
