package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// Rotating a customer's keys replaces what the files carry -- the
// WireGuard pair and preshared key, the OpenVPN password -- and keeps what
// identifies them: the address, the device name, the username, the plan
// and the usage.
func TestRotateKeysReplacesSecretsAndKeepsTheRest(t *testing.T) {
	db := testDB(t)
	svc := NewClients(db, ipam.NewPools(), quietLog())
	ctx := context.Background()

	for _, f := range []model.Interface{
		{Name: "wg0", Protocol: model.ProtocolWireGuard, Subnet: "10.66.0.0/24", ListenPort: 443},
		{Name: "ovpn", Protocol: model.ProtocolOpenVPN, Subnet: "10.88.0.0/24", ListenPort: 2053},
	} {
		if err := db.Create(&f).Error; err != nil {
			t.Fatal(err)
		}
	}
	c := model.Client{Name: "ali", Status: model.StatusActive, UsedBytes: 4242}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	before := []model.Account{
		{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-1", IP: "10.66.0.2",
			PrivateKey: "oldpriv", PublicKey: "oldpub", PresharedKey: "oldpsk", Enabled: true},
		{ClientID: c.ID, InterfaceID: 2, DeviceName: "user-1", IP: "10.88.0.2",
			Username: "s1-user-1", Secret: "oldsecret", Enabled: true},
	}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}

	n, err := svc.RotateKeys(ctx, c.ID, RotateInput{})
	if err != nil || n != 2 {
		t.Fatalf("rotate: %d %v", n, err)
	}

	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	byIface := map[uint]model.Account{}
	for _, a := range got.Accounts {
		byIface[a.InterfaceID] = a
	}
	wg, ovpn := byIface[1], byIface[2]
	if wg.PrivateKey == "oldpriv" || wg.PublicKey == "oldpub" || wg.PresharedKey == "oldpsk" {
		t.Fatalf("the WireGuard file keeps its old keys: %+v", wg)
	}
	if wg.PrivateKey == "" || wg.PublicKey == "" || wg.PresharedKey == "" {
		t.Fatalf("the WireGuard file has no keys: %+v", wg)
	}
	if ovpn.Secret == "oldsecret" || ovpn.Secret == "" {
		t.Fatalf("the OpenVPN password: %q", ovpn.Secret)
	}
	if wg.IP != "10.66.0.2" || ovpn.IP != "10.88.0.2" || ovpn.Username != "s1-user-1" ||
		wg.DeviceName != "user-1" || got.UsedBytes != 4242 {
		t.Fatalf("something that should have been kept changed: %+v %+v", wg, ovpn)
	}

	// One file alone, and a file that is somebody else's.
	firstKey := wg.PrivateKey
	if _, err := svc.RotateKeys(ctx, c.ID, RotateInput{AccountIDs: []uint{ovpn.ID}}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, c.ID)
	for _, a := range got.Accounts {
		if a.InterfaceID == 1 && a.PrivateKey != firstKey {
			t.Fatal("a file that was not named was rotated too")
		}
		if a.InterfaceID == 2 && a.Secret == ovpn.Secret {
			t.Fatal("the file that was named was not rotated")
		}
	}
	if _, err := svc.RotateKeys(ctx, c.ID, RotateInput{AccountIDs: []uint{9999}}); err == nil {
		t.Fatal("a file that is not this customer's is refused")
	}
}

// One user's files, and one kind of file, can be rotated on their own:
// a user is their place among the files on each tunnel.
func TestRotateKeysNarrowsToAUserOrAProtocol(t *testing.T) {
	db := testDB(t)
	svc := NewClients(db, ipam.NewPools(), quietLog())
	ctx := context.Background()
	for _, f := range []model.Interface{
		{Name: "wg0", Protocol: model.ProtocolWireGuard, Subnet: "10.66.0.0/24", ListenPort: 443},
		{Name: "ovpn", Protocol: model.ProtocolOpenVPN, Subnet: "10.88.0.0/24", ListenPort: 2053},
	} {
		if err := db.Create(&f).Error; err != nil {
			t.Fatal(err)
		}
	}
	c := model.Client{Name: "ali", Status: model.StatusActive}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	// Two users, each with a file on both tunnels, in issue order.
	accs := []model.Account{
		{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-1", IP: "10.66.0.2", PrivateKey: "w1", PublicKey: "p1", PresharedKey: "s1"},
		{ClientID: c.ID, InterfaceID: 2, DeviceName: "user-1", IP: "10.88.0.2", Username: "u1", Secret: "o1"},
		{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-2", IP: "10.66.0.3", PrivateKey: "w2", PublicKey: "p2", PresharedKey: "s2"},
		{ClientID: c.ID, InterfaceID: 2, DeviceName: "user-2", IP: "10.88.0.3", Username: "u2", Secret: "o2"},
	}
	if err := db.Create(&accs).Error; err != nil {
		t.Fatal(err)
	}

	// User 2's WireGuard file only: one file, and it is the one on wg0.
	n, err := svc.RotateKeys(ctx, c.ID, RotateInput{User: 2, Protocol: "wireguard"})
	if err != nil || n != 1 {
		t.Fatalf("one user's WireGuard: %d %v", n, err)
	}
	got, _ := svc.Get(ctx, c.ID)
	for _, a := range got.Accounts {
		switch {
		case a.DeviceName == "user-2" && a.InterfaceID == 1:
			if a.PrivateKey == "w2" {
				t.Fatal("user 2's WireGuard file was not rotated")
			}
		case a.DeviceName == "user-1" && a.InterfaceID == 1:
			if a.PrivateKey != "w1" {
				t.Fatal("user 1's WireGuard file was rotated too")
			}
		case a.InterfaceID == 2:
			if a.Secret != "o1" && a.Secret != "o2" {
				t.Fatal("an OpenVPN password was rotated by a WireGuard-only request")
			}
		}
	}

	// A user who is not in the plan, and a protocol that is not a protocol.
	if _, err := svc.RotateKeys(ctx, c.ID, RotateInput{User: 9}); err == nil {
		t.Fatal("a user the plan does not have is refused")
	}
	if _, err := svc.RotateKeys(ctx, c.ID, RotateInput{Protocol: "l2tp"}); err == nil {
		t.Fatal("an unknown protocol is refused")
	}
}
