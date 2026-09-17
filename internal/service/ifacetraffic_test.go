package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// A customer on two tunnels spends one allowance, but each tunnel is
// charged only with what crossed it: the interfaces page sums each
// tunnel's own files, not the customers on it.
func TestEachTunnelIsChargedWithItsOwnTraffic(t *testing.T) {
	db := testDB(t)
	c := model.Client{Name: "ali", Status: model.StatusActive, UsedBytes: 1000, UpBytes: 400, DownBytes: 600}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	accounts := []model.Account{
		{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-1", IP: "10.0.0.2", UpBytes: 300, DownBytes: 500},
		{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-2", IP: "10.0.0.3", UpBytes: 10, DownBytes: 20},
		{ClientID: c.ID, InterfaceID: 2, DeviceName: "user-1", IP: "10.1.0.2", UpBytes: 90, DownBytes: 80},
	}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	loads, err := NewInterfaces(db, ipam.NewPools(), quietLog()).Loads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wg, ovpn := loads[1], loads[2]
	if wg.UpBytes != 310 || wg.DownBytes != 520 || wg.UsedBytes != 830 {
		t.Fatalf("tunnel 1 is charged with its own files: %+v", wg)
	}
	if ovpn.UpBytes != 90 || ovpn.DownBytes != 80 || ovpn.UsedBytes != 170 {
		t.Fatalf("tunnel 2 is charged with its own file: %+v", ovpn)
	}
	if wg.Clients != 1 || wg.Devices != 2 || wg.Active != 1 || ovpn.Clients != 1 || ovpn.Active != 1 {
		t.Fatalf("the customer is counted once per tunnel: %+v %+v", wg, ovpn)
	}
}
