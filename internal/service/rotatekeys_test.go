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

	n, err := svc.RotateKeys(ctx, c.ID, nil)
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
	if _, err := svc.RotateKeys(ctx, c.ID, []uint{ovpn.ID}); err != nil {
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
	if _, err := svc.RotateKeys(ctx, c.ID, []uint{9999}); err == nil {
		t.Fatal("a file that is not this customer's is refused")
	}
}
