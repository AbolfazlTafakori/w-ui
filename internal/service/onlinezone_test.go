package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// A device that just connected is online on every page that counts, whatever
// time zone the owner set the panel to. Handshakes are stored in UTC; a
// cutoff worked out in local time compared wrong against them, and in Tehran
// time the interfaces page showed nobody online.
func TestOnlineIsCountedTheSameInAnyTimeZone(t *testing.T) {
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Skip("no time zone data here:", err)
	}
	was := time.Local
	time.Local = tehran
	t.Cleanup(func() { time.Local = was })
	window := OnlineWithin.Load()
	OnlineWithin.Store(180)
	t.Cleanup(func() { OnlineWithin.Store(window) })

	db := testDB(t)
	ctx := context.Background()
	iface := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.9.0.0/24", EndpointHost: "x", Enabled: true}
	if err := db.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	for i, hs := range []*time.Time{&now, &now, &old, nil} {
		c := model.Client{Name: string(rune('a' + i)), Status: model.StatusActive}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: iface.ID, DeviceName: "d", IP: "10.9.0." + string(rune('2'+i)), LastHandshake: hs}).Error; err != nil {
			t.Fatal(err)
		}
	}

	ifaces := NewInterfaces(db, ipam.NewPools(), quietLog())
	loads, err := ifaces.Loads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := loads[iface.ID].Online; got != 2 {
		t.Errorf("the interfaces page counts %d online, want 2", got)
	}
	own, err := ifaces.OwnCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := own[iface.ID].Online; got != 2 {
		t.Errorf("the server status counts %d online, want 2", got)
	}
	clients := NewClients(db, ipam.NewPools(), quietLog())
	o, err := clients.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Online != 2 {
		t.Errorf("the overview counts %d online, want 2", o.Online)
	}
	page, err := clients.List(ctx, ListFilter{Buckets: []string{"online"}, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Errorf("the online filter finds %d, want 2", page.Total)
	}
}
