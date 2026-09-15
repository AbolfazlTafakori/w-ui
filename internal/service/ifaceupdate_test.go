package service

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// A tunnel's name, port, subnet and mode can all be changed after the fact,
// and a change of subnet moves every device on it to the new range.

func TestATunnelNameIsADeviceName(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	port := freeUDPPort(t)
	for _, name := range []string{"irmorghmina.mrnobudi.ir", "has space", strings.Repeat("x", 16)} {
		_, err := svc.Create(context.Background(), CreateInterfaceInput{Name: name, Protocol: "wireguard", ListenPort: port, Subnet: "10.77.0.0/24", EndpointHost: "h"})
		check(t, name, err, wantErr{"name", "a tunnel name"})
	}
}

func TestSubnetsOnOneServerMayNotOverlap(t *testing.T) {
	db, _ := seeded(t) // wg0 on 10.9.0.0/24
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	_, err := svc.Create(context.Background(), CreateInterfaceInput{Name: "wg1", Protocol: "wireguard", ListenPort: freeUDPPort(t), Subnet: "10.9.0.0/16", EndpointHost: "h"})
	check(t, "overlap", err, wantErr{"subnet", `overlaps 10.9.0.0/24, the subnet of tunnel "wg0"`})
}

func TestChangingTheSubnetMovesEveryDevice(t *testing.T) {
	ctx := context.Background()
	db, iface := seeded(t)
	pools := ipam.NewPools()
	if _, err := pools.Add(iface.ID, iface.Subnet); err != nil {
		t.Fatal(err)
	}
	svc := NewInterfaces(db, pools, quietLog())
	client := model.Client{Name: "c"}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	for i, ip := range []string{"10.9.0.2", "10.9.0.3"} {
		if err := db.Create(&model.Account{ClientID: client.ID, InterfaceID: iface.ID, IP: ip, DeviceName: "d" + string(rune('a'+i))}).Error; err != nil {
			t.Fatal(err)
		}
	}
	torn := 0
	svc.Teardown = func(context.Context, *model.Interface) { torn++ }

	sub := "10.50.0.0/24"
	got, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{Subnet: &sub})
	if err != nil {
		t.Fatal(err)
	}
	if got.Subnet != sub {
		t.Errorf("subnet %q", got.Subnet)
	}
	if torn != 1 {
		t.Errorf("the old device was torn down %d times, want 1", torn)
	}
	var accounts []model.Account
	db.Where("interface_id = ?", iface.ID).Find(&accounts)
	prefix := netip.MustParsePrefix(sub)
	seen := map[string]bool{}
	for _, a := range accounts {
		addr, err := netip.ParseAddr(a.IP)
		if err != nil || !prefix.Contains(addr) {
			t.Errorf("device %s kept %q, outside %s", a.DeviceName, a.IP, sub)
		}
		if seen[a.IP] {
			t.Errorf("two devices share %s", a.IP)
		}
		seen[a.IP] = true
	}
	// The pool follows: the next allocation is in the new range and not one
	// of the addresses just handed out.
	alloc, err := pools.Get(iface.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := alloc.Allocate()
	if err != nil || !prefix.Contains(next) || seen[next.String()] {
		t.Errorf("next allocation %v %v", next, err)
	}

	// Too small for the devices it has.
	tiny := "10.60.0.0/30"
	_, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{Subnet: &tiny})
	check(t, "too small", err, wantErr{"subnet", "is too small for the 2 devices"})
}

func TestRenamingAndMovingPortTearDownTheOldDevice(t *testing.T) {
	ctx := context.Background()
	db, iface := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	torn := 0
	svc.Teardown = func(context.Context, *model.Interface) { torn++ }

	name := "wg9"
	if _, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	port := freeUDPPort(t)
	got, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{ListenPort: &port})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "wg9" || got.ListenPort != port || torn != 2 {
		t.Errorf("name %q port %d torn %d", got.Name, got.ListenPort, torn)
	}
	// The same values again change nothing and tear nothing down.
	if _, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{Name: &name, ListenPort: &port}); err != nil || torn != 2 {
		t.Errorf("no-op update: %v torn %d", err, torn)
	}
	bad := "wg 9"
	_, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{Name: &bad})
	check(t, "bad rename", err, wantErr{"name", "a tunnel name can only contain"})
}

func TestStartOnFirstUseCanBeChangedLater(t *testing.T) {
	ctx := context.Background()
	db, iface := seeded(t)
	pools := ipam.NewPools()
	pools.Add(iface.ID, iface.Subnet)
	clients := NewClients(db, pools, quietLog())
	c, err := clients.Create(ctx, CreateInput{Name: "x", InterfaceIDs: []uint{iface.ID}, DeviceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	on, days := true, 30
	got, err := clients.Update(ctx, c.ID, UpdateInput{StartOnFirstUse: &on, DurationDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartOnFirstUse || got.DurationDays != 30 || got.ExpiresAt != nil {
		t.Errorf("%+v", got)
	}
	off := false
	got, err = clients.Update(ctx, c.ID, UpdateInput{StartOnFirstUse: &off})
	if err != nil || got.StartOnFirstUse || got.DurationDays != 0 {
		t.Errorf("%+v %v", got, err)
	}
}
