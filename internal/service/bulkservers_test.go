package service

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// seedServers makes two tunnels and n customers, all on the first one.
func seedServers(t *testing.T, db *gorm.DB, n int) (*Clients, []model.Interface, []model.Client) {
	t.Helper()

	ifaces := []model.Interface{
		{Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51820,
			Subnet: "10.66.0.0/24", EndpointHost: "vpn1.example.com", MTU: 1420, NodeID: 1},
		{Name: "wg1", Protocol: model.ProtocolWireGuard, Enabled: true, ListenPort: 51821,
			Subnet: "10.67.0.0/24", EndpointHost: "vpn2.example.com", MTU: 1420, NodeID: 1},
	}
	for i := range ifaces {
		if err := db.Create(&ifaces[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	pools := ipam.NewPools()
	for _, f := range ifaces {
		if _, err := pools.Add(f.ID, f.Subnet); err != nil {
			t.Fatalf("pool for %s: %v", f.Name, err)
		}
	}
	svc := NewClients(db, pools, quietLog())

	expires := time.Now().Add(48 * time.Hour).UTC()
	var made []model.Client
	for i := 0; i < n; i++ {
		c, err := svc.Create(context.Background(), CreateInput{
			Name:         names[i],
			InterfaceIDs: []uint{ifaces[0].ID},
			QuotaBytes:   10 << 30,
			ExpiresAt:    &expires,
			DeviceLimit:  2,
		})
		if err != nil {
			t.Fatalf("create %s: %v", names[i], err)
		}
		made = append(made, *c)
	}
	return svc, ifaces, made
}

var names = []string{"Roya", "Sina", "Mina", "Kian", "Sara"}

func ids(cs []model.Client) []uint {
	out := make([]uint, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func serversOf(t *testing.T, db *gorm.DB, id uint) []uint {
	t.Helper()
	var accounts []model.Account
	if err := db.Where("client_id = ?", id).Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	return clientInterfaces(accounts)
}

// The operation the whole node feature needs: a new server, given to the
// customers who already exist, in one action rather than three hundred.
func TestASecondServerReachesEverybodySelected(t *testing.T) {
	db := testDB(t)
	svc, ifaces, made := seedServers(t, db, 3)

	res, err := svc.AttachServers(context.Background(), ids(made), []uint{ifaces[1].ID})
	if err != nil {
		t.Fatalf("AttachServers: %v", err)
	}
	if res.Changed != 3 {
		t.Errorf("changed %d customers, want 3 (failures: %v)", res.Changed, res.Failures)
	}

	for _, c := range made {
		got := serversOf(t, db, c.ID)
		if len(got) != 2 {
			t.Errorf("%s is on %d servers, want 2", c.Name, len(got))
		}
	}
}

// Adding is not replacing. The reason a customer has several servers is that
// one being blocked leaves the rest working, and a bulk action that quietly
// replaced their list would take that away from everybody at once.
func TestAttachingKeepsTheServersTheyAlreadyHad(t *testing.T) {
	db := testDB(t)
	svc, ifaces, made := seedServers(t, db, 1)

	if _, err := svc.AttachServers(context.Background(), ids(made), []uint{ifaces[1].ID}); err != nil {
		t.Fatalf("AttachServers: %v", err)
	}

	got := serversOf(t, db, made[0].ID)
	var hasFirst bool
	for _, id := range got {
		if id == ifaces[0].ID {
			hasFirst = true
		}
	}
	if !hasFirst {
		t.Errorf("the server they were already on was taken away: %v", got)
	}
}

// Doing it twice must not double anything. An operator who is not sure whether
// the first attempt went through will press it again.
func TestAttachingTwiceChangesNothingTheSecondTime(t *testing.T) {
	db := testDB(t)
	svc, ifaces, made := seedServers(t, db, 2)
	ctx := context.Background()

	if _, err := svc.AttachServers(ctx, ids(made), []uint{ifaces[1].ID}); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	res, err := svc.AttachServers(ctx, ids(made), []uint{ifaces[1].ID})
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if res.Changed != 0 || res.Unchanged != 2 {
		t.Errorf("the second attach changed %d and left %d alone, want 0 and 2",
			res.Changed, res.Unchanged)
	}
	if got := serversOf(t, db, made[0].ID); len(got) != 2 {
		t.Errorf("a customer ended up on %d servers after attaching twice", len(got))
	}
}

// Giving a server up means taking every customer off it first.
func TestDetachingTakesTheServerAway(t *testing.T) {
	db := testDB(t)
	svc, ifaces, made := seedServers(t, db, 2)
	ctx := context.Background()

	if _, err := svc.AttachServers(ctx, ids(made), []uint{ifaces[1].ID}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	res, err := svc.DetachServers(ctx, ids(made), []uint{ifaces[0].ID})
	if err != nil {
		t.Fatalf("DetachServers: %v", err)
	}
	if res.Changed != 2 {
		t.Errorf("detached %d customers, want 2 (failures: %v)", res.Changed, res.Failures)
	}
	for _, c := range made {
		got := serversOf(t, db, c.ID)
		if len(got) != 1 || got[0] != ifaces[1].ID {
			t.Errorf("%s is on %v, want only the second server", c.Name, got)
		}
	}
}

// A customer left with nothing has a subscription that renders nothing, and
// finding that out from the customer is the worst way to find it out. Refused,
// and named, rather than done quietly.
func TestACustomerIsNeverLeftWithNoServer(t *testing.T) {
	db := testDB(t)
	svc, ifaces, made := seedServers(t, db, 2)

	res, err := svc.DetachServers(context.Background(), ids(made), []uint{ifaces[0].ID})
	if err != nil {
		t.Fatalf("DetachServers: %v", err)
	}
	if res.Changed != 0 {
		t.Errorf("%d customers were left with no server", res.Changed)
	}
	if len(res.Failures) != 2 {
		t.Fatalf("expected both customers to be named as refused, got %v", res.Failures)
	}
	for _, c := range made {
		if _, named := res.Failures[c.Name]; !named {
			t.Errorf("%s was refused without being named", c.Name)
		}
		if got := serversOf(t, db, c.ID); len(got) != 1 {
			t.Errorf("%s ended up on %d servers", c.Name, len(got))
		}
	}
}

// Nothing selected is a mistake about the request, not a no-op to be reported
// as success.
func TestNothingSelectedIsRefused(t *testing.T) {
	db := testDB(t)
	svc, ifaces, made := seedServers(t, db, 1)
	ctx := context.Background()

	if _, err := svc.AttachServers(ctx, nil, []uint{ifaces[1].ID}); err == nil {
		t.Error("attaching with no customers selected was accepted")
	}
	if _, err := svc.AttachServers(ctx, ids(made), nil); err == nil {
		t.Error("attaching with no servers chosen was accepted")
	}
}

// A server that does not exist is a mistake about the request too, and must be
// caught before any customer is touched.
func TestAnUnknownServerTouchesNobody(t *testing.T) {
	db := testDB(t)
	svc, _, made := seedServers(t, db, 2)

	if _, err := svc.AttachServers(context.Background(), ids(made), []uint{9999}); err == nil {
		t.Fatal("attaching a server that does not exist was accepted")
	}
	for _, c := range made {
		if got := serversOf(t, db, c.ID); len(got) != 1 {
			t.Errorf("%s was changed by a request that should have failed outright", c.Name)
		}
	}
}
