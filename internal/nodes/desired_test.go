package nodes

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// What a node is told is the whole of what it enforces.
//
// The node holds no allowance and no expiry date — it cannot, because an
// allowance is one number spent across every server and only the panel that
// sold the plan can see the whole of it. So `Enabled` is the entire decision,
// and if it is ever computed wrongly here a customer who has run out keeps
// working on every node they were sold while the central server shows them cut
// off. That is service given away, quietly, on the machines nobody looks at.

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedNode puts one tunnel on node 7 with one customer in each given status.
func seedNode(t *testing.T, db *gorm.DB, statuses ...model.ClientStatus) {
	t.Helper()

	iface := model.Interface{
		Name: "wg1", Protocol: model.ProtocolWireGuard, Enabled: true,
		ListenPort: 51820, Subnet: "10.67.0.0/16", EndpointHost: "vpn2.example.com",
		MTU: 1420, NodeID: 7,
	}
	if err := db.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}

	for i, status := range statuses {
		c := model.Client{
			Name: string(status), Protocol: model.ProtocolWireGuard, Status: status,
			QuotaBytes: 10 << 30, UsedBytes: 10 << 30,
		}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		acc := model.Account{
			ClientID: c.ID, InterfaceID: iface.ID,
			DeviceName: "phone", IP: "10.67.0.2", PublicKey: "pub", PrivateKey: "priv",
		}
		acc.IP = "10.67.0." + string(rune('2'+i))
		if err := db.Create(&acc).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func newSyncer(db *gorm.DB) *Syncer { return NewSyncer(db, nil, quietLog()) }

// A customer who has spent their allowance is sent to the node as disabled.
//
// The central panel flips them to exhausted; nothing else about the row
// changes, so the only thing carrying that decision across the wire is this.
func TestAnExhaustedCustomerIsDisabledOnTheNode(t *testing.T) {
	db := testDB(t)
	seedNode(t, db, model.StatusExhausted)

	states, err := newSyncer(db).desired(context.Background(), 7, false)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(states) != 1 || len(states[0].Clients) != 1 {
		t.Fatalf("desired() produced %d tunnels; want one with one customer", len(states))
	}
	if states[0].Clients[0].Enabled {
		t.Error("a customer who has used their whole allowance is still enabled on the node")
	}
}

// The same for every other status that means "not now", and only for those.
func TestOnlyAnActiveCustomerIsEnabled(t *testing.T) {
	cases := []struct {
		status model.ClientStatus
		want   bool
	}{
		{model.StatusActive, true},
		{model.StatusExhausted, false},
		{model.StatusExpired, false},
		{model.StatusDisabled, false},
	}

	for _, tc := range cases {
		t.Run(string(tc.status), func(t *testing.T) {
			db := testDB(t)
			seedNode(t, db, tc.status)

			states, err := newSyncer(db).desired(context.Background(), 7, false)
			if err != nil {
				t.Fatalf("desired: %v", err)
			}
			if got := states[0].Clients[0].Enabled; got != tc.want {
				t.Errorf("a %s customer is sent as enabled=%v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// A server past its host's transfer allowance carries nobody, whatever the
// customers' own standing is. Otherwise an operator's overage bill keeps
// growing while every customer on it is perfectly within their plan.
func TestASpentServerCarriesNobody(t *testing.T) {
	db := testDB(t)
	seedNode(t, db, model.StatusActive, model.StatusActive)

	states, err := newSyncer(db).desired(context.Background(), 7, true)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(states[0].Clients) != 2 {
		t.Fatalf("desired() sent %d customers, want 2", len(states[0].Clients))
	}
	for _, c := range states[0].Clients {
		if c.Enabled {
			t.Errorf("customer %d is still enabled on a server past its allowance", c.OriginID)
		}
	}
}

// A node is told about its own tunnels and nothing else. Sending another
// server's would have the node programming peers for an interface it does not
// have, and would leak one customer's keys onto a machine that may be rented
// from somebody else.
func TestANodeOnlyHearsAboutItsOwnTunnels(t *testing.T) {
	db := testDB(t)
	seedNode(t, db, model.StatusActive)

	other := model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true,
		ListenPort: 51820, Subnet: "10.66.0.0/16", EndpointHost: "vpn1.example.com",
		MTU: 1420, NodeID: 1,
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}

	states, err := newSyncer(db).desired(context.Background(), 7, false)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("desired() sent %d tunnels to the node, want 1", len(states))
	}
	if states[0].Interface.Name != "wg1" {
		t.Errorf("the node was sent %q, which is not its tunnel", states[0].Interface.Name)
	}
}

// The node needs the key material to terminate the tunnel, and must not be
// given the plan behind it: the allowance is spent across every server and only
// the central panel can see the whole of it.
func TestTheNodeGetsTheKeysAndNotThePlan(t *testing.T) {
	db := testDB(t)
	seedNode(t, db, model.StatusActive)

	states, err := newSyncer(db).desired(context.Background(), 7, false)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	acc := states[0].Clients[0].Accounts[0]
	if acc.PublicKey == "" || acc.PrivateKey == "" {
		t.Error("the node was not given the keys it needs to admit the peer")
	}
	if states[0].Interface.EndpointHost != "vpn2.example.com" {
		t.Errorf("the node was sent endpoint %q", states[0].Interface.EndpointHost)
	}
}
