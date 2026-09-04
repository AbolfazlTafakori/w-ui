package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/ovpndriver"
	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// The real drivers, because a tunnel that runs on another server has no open
// driver here: what renders its configuration is one built from the registry,
// and the registry is empty unless a driver package has registered itself.
// Guarded, since registering a protocol twice is a panic.
func init() {
	if !backend.Supports(model.ProtocolWireGuard) {
		wgdriver.Register()
	}
	if !backend.Supports(model.ProtocolOpenVPN) {
		ovpndriver.Register()
	}
}

// seedTwoServers gives one customer a device on this panel's own tunnel and a
// device on a tunnel that belongs to a node.
func seedTwoServers(t *testing.T, db *gorm.DB) *model.Client {
	t.Helper()

	local := model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true,
		ListenPort: 51820, Subnet: "10.66.0.0/16", EndpointHost: "vpn1.example.com",
		MTU: 1420, NodeID: 1,
	}
	remote := model.Interface{
		Name: "wg1", Protocol: model.ProtocolWireGuard, Enabled: true,
		ListenPort: 51820, Subnet: "10.67.0.0/16", EndpointHost: "vpn2.example.com",
		MTU: 1420, NodeID: 7,
	}
	for _, iface := range []*model.Interface{&local, &remote} {
		if err := db.Create(iface).Error; err != nil {
			t.Fatal(err)
		}
	}

	expires := time.Now().Add(48 * time.Hour).UTC()
	c := model.Client{
		Name: "Roya", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
		QuotaBytes: 50 << 30, ExpiresAt: &expires, DeviceLimit: 2,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range []model.Account{
		{ClientID: c.ID, InterfaceID: local.ID, DeviceName: "phone", IP: "10.66.0.2"},
		{ClientID: c.ID, InterfaceID: remote.ID, DeviceName: "phone", IP: "10.67.0.2"},
	} {
		acc := a
		if err := db.Create(&acc).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &c
}

// A customer sold two servers must receive two configurations.
//
// The panel that holds the records does not run the tunnels on its nodes, so it
// has no open driver for them. Rendering used to require one, and every
// configuration for a node was quietly dropped from the subscription — the
// customer received only the servers this panel happened to terminate itself,
// which is the opposite of the reason for having nodes at all. A customer whose
// first server is blocked is supposed to still have the second.
func TestACustomerOnANodeStillGetsThatServersConfig(t *testing.T) {
	db := testDB(t)
	c := seedTwoServers(t, db)
	// No driver in the pool at all: this is the panel as it runs when every
	// tunnel belongs to a node.
	s := newSubs(db, nil)

	b, err := s.BundleForClient(context.Background(), c.ID, "raw")
	if err != nil {
		t.Fatalf("BundleForClient: %v", err)
	}

	body := string(b.Body)
	for _, host := range []string{"vpn1.example.com", "vpn2.example.com"} {
		if !strings.Contains(body, host) {
			t.Errorf("the subscription is missing %s:\n%s", host, body)
		}
	}
}

// The same for one file at a time, which is what the customer's own page links
// to for each device.
func TestADeviceOnANodeCanBeDownloadedOnItsOwn(t *testing.T) {
	db := testDB(t)
	c := seedTwoServers(t, db)
	s := newSubs(db, nil)

	token, err := s.EnsureToken(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}

	var accounts []model.Account
	if err := db.Where("client_id = ?", c.ID).Find(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("seed made %d devices, want 2", len(accounts))
	}

	for _, acc := range accounts {
		profile, err := s.DeviceConfig(context.Background(), token, acc.ID)
		if err != nil {
			t.Fatalf("DeviceConfig for device %d: %v", acc.ID, err)
		}
		if len(profile.Body) == 0 {
			t.Errorf("device %d downloaded an empty configuration", acc.ID)
		}
	}
}

// An open driver still wins where there is one. It is this machine's own view
// of the interface it is actually running, and a rebuilt one is a fallback for
// tunnels that live elsewhere, not a replacement.
func TestAnOpenDriverIsStillPreferred(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	s := newSubs(db, &fakeDriver{body: "FROM-THE-OPEN-DRIVER"})

	b, err := s.BundleForClient(context.Background(), c.ID, "raw")
	if err != nil {
		t.Fatalf("BundleForClient: %v", err)
	}
	if !strings.Contains(string(b.Body), "FROM-THE-OPEN-DRIVER") {
		t.Errorf("the open driver was bypassed:\n%s", b.Body)
	}
}

// A server past its host's transfer allowance drops out of what the customer is
// handed, and the ones that are left stay.
//
// This is the point of selling more than one server: the customer's app moves
// on to a server that still works instead of the operator being billed for
// overage, and neither the customer nor the operator has to do anything.
func TestAServerPastItsAllowanceLeavesTheSubscription(t *testing.T) {
	db := testDB(t)
	c := seedTwoServers(t, db)

	// Node 7 runs vpn2 and has used every byte its host allows.
	spent := model.Node{
		ID: 7, Name: "berlin", Kind: model.KindRemote,
		DataLimitBytes: 1 << 30, UsedBytes: 1 << 30,
	}
	if err := db.Create(&spent).Error; err != nil {
		t.Fatal(err)
	}

	s := newSubs(db, nil)
	b, err := s.BundleForClient(context.Background(), c.ID, "raw")
	if err != nil {
		t.Fatalf("BundleForClient: %v", err)
	}

	body := string(b.Body)
	if strings.Contains(body, "vpn2.example.com") {
		t.Errorf("a server past its allowance is still being handed out:\n%s", body)
	}
	if !strings.Contains(body, "vpn1.example.com") {
		t.Errorf("the servers that still have allowance were dropped too:\n%s", body)
	}
}

// And it comes back on its own when the host's month starts again. Nothing is
// rebuilt and no operator has to remember anything.
func TestTheServerComesBackWhenItsAllowanceDoes(t *testing.T) {
	db := testDB(t)
	c := seedTwoServers(t, db)

	last := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	spent := model.Node{
		ID: 7, Name: "berlin", Kind: model.KindRemote,
		DataLimitBytes: 1 << 30, UsedBytes: 1 << 30,
		ResetDay: 1, UsageResetAt: &last,
	}
	if err := db.Create(&spent).Error; err != nil {
		t.Fatal(err)
	}

	if err := RecordNodeTraffic(context.Background(), db, 7, 0,
		time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordNodeTraffic: %v", err)
	}

	s := newSubs(db, nil)
	b, err := s.BundleForClient(context.Background(), c.ID, "raw")
	if err != nil {
		t.Fatalf("BundleForClient: %v", err)
	}
	if !strings.Contains(string(b.Body), "vpn2.example.com") {
		t.Errorf("the server did not come back after its month rolled over:\n%s", b.Body)
	}
}
