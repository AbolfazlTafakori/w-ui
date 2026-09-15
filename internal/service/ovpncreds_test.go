package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

func seedOpenVPN(t *testing.T, db *gorm.DB) (*Clients, model.Interface, model.Interface) {
	t.Helper()
	wg := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51820,
		Subnet: "10.66.0.0/24", EndpointHost: "vpn.example.com", MTU: 1420, NodeID: 1}
	ov := model.Interface{Name: "ovpn0", Protocol: model.ProtocolOpenVPN, Enabled: true, ListenPort: 1194,
		Subnet: "10.8.0.0/24", EndpointHost: "vpn.example.com", MTU: 1420, NodeID: 1}
	for _, f := range []*model.Interface{&wg, &ov} {
		if err := db.Create(f).Error; err != nil {
			t.Fatal(err)
		}
	}
	pools := ipam.NewPools()
	for _, f := range []model.Interface{wg, ov} {
		if _, err := pools.Add(f.ID, f.Subnet); err != nil {
			t.Fatal(err)
		}
	}
	return NewClients(db, pools, quietLog()), wg, ov
}

func accountsOn(t *testing.T, db *gorm.DB, client, iface uint) []model.Account {
	t.Helper()
	var out []model.Account
	if err := db.Where("client_id = ? AND interface_id = ?", client, iface).Order("id").Find(&out).Error; err != nil {
		t.Fatal(err)
	}
	return out
}

// A reseller selling "username and password" plans types them in; the
// first device gets them as typed, a second one the name with a number.
func TestOpenVPNCredentialsAreWhatWasTyped(t *testing.T) {
	db := testDB(t)
	svc, wg, ov := seedOpenVPN(t, db)
	expires := time.Now().Add(24 * time.Hour)
	c, err := svc.Create(context.Background(), CreateInput{
		Name: "Roya", InterfaceIDs: []uint{wg.ID, ov.ID}, ExpiresAt: &expires, DeviceLimit: 3,
		DeviceNames:     []string{"phone", "laptop"},
		OpenVPNUsername: "roya", OpenVPNPassword: "s3cret-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := accountsOn(t, db, c.ID, ov.ID)
	if len(got) != 2 || got[0].Username != "roya" || got[1].Username != "roya-2" {
		t.Fatalf("usernames: %+v", got)
	}
	for _, a := range got {
		if a.Secret != "s3cret-pass" {
			t.Fatalf("password not applied: %q", a.Secret)
		}
	}
	for _, a := range accountsOn(t, db, c.ID, wg.ID) {
		if a.Username != "" {
			t.Fatalf("a WireGuard account picked up a username: %+v", a)
		}
	}

	// The same name for someone else on that tunnel is refused.
	_, err = svc.Create(context.Background(), CreateInput{
		Name: "Sina", InterfaceIDs: []uint{ov.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "roya",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate username accepted: %v", err)
	}
	// And on a customer with no OpenVPN tunnel it means nothing.
	_, err = svc.Create(context.Background(), CreateInput{
		Name: "Mina", InterfaceIDs: []uint{wg.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "mina",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("username without an OpenVPN tunnel accepted: %v", err)
	}
}

// Editing renames the devices already issued, and a tunnel added in the
// same save gets the name too.
func TestOpenVPNCredentialsCanBeChangedLater(t *testing.T) {
	db := testDB(t)
	svc, wg, ov := seedOpenVPN(t, db)
	expires := time.Now().Add(24 * time.Hour)
	c, err := svc.Create(context.Background(), CreateInput{
		Name: "Roya", InterfaceIDs: []uint{wg.ID}, ExpiresAt: &expires, DeviceLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Update(context.Background(), c.ID, UpdateInput{
		InterfaceIDs: []uint{wg.ID, ov.ID}, OpenVPNUsername: "roya.k", OpenVPNPassword: "another-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := accountsOn(t, db, c.ID, ov.ID)
	if len(got) != 1 || got[0].Username != "roya.k" || got[0].Secret != "another-1" {
		t.Fatalf("credentials after adding the tunnel: %+v", got)
	}
	// Only the password, keeping the name.
	if _, err := svc.Update(context.Background(), c.ID, UpdateInput{OpenVPNPassword: "changed-2"}); err != nil {
		t.Fatal(err)
	}
	got = accountsOn(t, db, c.ID, ov.ID)
	if got[0].Username != "roya.k" || got[0].Secret != "changed-2" {
		t.Fatalf("password-only change: %+v", got)
	}
	// Bad input is named.
	_, err = svc.Update(context.Background(), c.ID, UpdateInput{OpenVPNUsername: "no spaces"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad username accepted: %v", err)
	}
	_, err = svc.Update(context.Background(), c.ID, UpdateInput{OpenVPNPassword: "short"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("short password accepted: %v", err)
	}
}
