package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func managedState(held *time.Time, limit int) NodeState {
	return NodeState{
		Interface: NodeInterface{OriginID: 3, Name: "wg1", Protocol: model.ProtocolWireGuard,
			Enabled: true, ListenPort: 51820, Subnet: "10.67.0.0/16", EndpointHost: "vpn2.example.com", MTU: 1420},
		Clients: []NodeClient{{OriginID: 7, Enabled: true, DeviceLimit: limit, Accounts: []NodeAccount{
			{OriginID: 40, DeviceName: "phone", IP: "10.67.0.2", Enabled: true, PublicKey: "p1", HeldUntil: held},
			{OriginID: 41, DeviceName: "laptop", IP: "10.67.0.3", Enabled: true, PublicKey: "p2"},
		}}},
	}
}

// On the node, a hold in the push and a hold by the direct call both reach
// the data plane by the account's local id, and the plan's limit is kept
// for the fallback.
func TestHoldsReachTheNodesDataPlane(t *testing.T) {
	db := testDB(t)
	ns := NewNodeSync(db, quietLog())
	var got []uint
	ns.OnHold = func(_ context.Context, id uint, until time.Time) { got = append(got, id) }

	until := time.Now().Add(time.Minute)
	if err := ns.Apply(context.Background(), 1, managedState(&until, 1)); err != nil {
		t.Fatal(err)
	}
	var phone model.Account
	if err := db.Where("origin_id = ?", 40).First(&phone).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != phone.ID {
		t.Fatalf("hold in the push reached %v, want [%d]", got, phone.ID)
	}
	var c model.Client
	if err := db.Where("origin_id = ?", 7).First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.DeviceLimit != 1 {
		t.Fatalf("device limit on the node = %d, want 1", c.DeviceLimit)
	}

	// A hold that has already ended is not applied.
	got = nil
	past := time.Now().Add(-time.Minute)
	if err := ns.Apply(context.Background(), 1, managedState(&past, 1)); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("an expired hold was applied: %v", got)
	}

	// The direct call, by the panel's ids.
	var laptop model.Account
	if err := db.Where("origin_id = ?", 41).First(&laptop).Error; err != nil {
		t.Fatal(err)
	}
	n, err := ns.Hold(context.Background(), []NodeHold{{OriginID: 41, Until: until}, {OriginID: 999, Until: until}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(got) != 1 || got[0] != laptop.ID {
		t.Fatalf("direct hold reached %v (n=%d), want [%d]", got, n, laptop.ID)
	}

	// An older panel that sends no limit: the node's count is the files it
	// holds, so its fallback holds nobody.
	if err := ns.Apply(context.Background(), 1, managedState(nil, 0)); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("origin_id = ?", 7).First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.DeviceLimit != 2 {
		t.Fatalf("device limit with none sent = %d, want 2", c.DeviceLimit)
	}
}

// A node with two tunnels: a push for one must not withdraw the customers
// of the other, or every round would tear them down and build them again.
func TestAPushForOneTunnelLeavesTheOtherTunnelsCustomersAlone(t *testing.T) {
	db := testDB(t)
	ns := NewNodeSync(db, quietLog())
	one := managedState(nil, 1)
	two := managedState(nil, 1)
	two.Interface.OriginID, two.Interface.Name, two.Interface.ListenPort, two.Interface.Subnet = 4, "wg2", 51821, "10.68.0.0/16"
	two.Clients = []NodeClient{{OriginID: 8, Enabled: true, DeviceLimit: 1, Accounts: []NodeAccount{
		{OriginID: 50, DeviceName: "tv", IP: "10.68.0.2", Enabled: true, PublicKey: "p3"},
	}}}
	for _, st := range []NodeState{one, two, one, two} {
		if err := ns.Apply(context.Background(), 1, st); err != nil {
			t.Fatal(err)
		}
	}
	var clients, accounts int64
	db.Model(&model.Client{}).Count(&clients)
	db.Model(&model.Account{}).Count(&accounts)
	if clients != 2 || accounts != 3 {
		t.Fatalf("after pushes for two tunnels: %d clients, %d accounts; want 2 and 3", clients, accounts)
	}

	// The panel deletes the second tunnel: the round names only the first.
	var torn []string
	ns.OnRemoveInterface = func(_ context.Context, iface *model.Interface) { torn = append(torn, iface.Name) }
	n, err := ns.Prune(context.Background(), 1, []uint{3})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(torn) != 1 || torn[0] != "wg2" {
		t.Fatalf("prune removed %d (%v), want wg2", n, torn)
	}
	db.Model(&model.Client{}).Count(&clients)
	db.Model(&model.Account{}).Count(&accounts)
	var ifaces int64
	db.Model(&model.Interface{}).Count(&ifaces)
	if clients != 1 || accounts != 2 || ifaces != 1 {
		t.Fatalf("after prune: %d clients, %d accounts, %d interfaces; want 1, 2, 1", clients, accounts, ifaces)
	}
}
