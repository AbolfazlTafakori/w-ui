package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// needL2TPPort skips where UDP 1701 is already held: the create path asks
// this machine whether the port is free, and here it would rightly say no.
func needL2TPPort(t *testing.T) {
	t.Helper()
	pc, err := net.ListenPacket("udp", ":1701")
	if err != nil {
		t.Skip("UDP 1701 is in use on this machine")
	}
	pc.Close()
}

func createL2TP(t *testing.T, svc *Interfaces, in CreateInterfaceInput) (*model.Interface, error) {
	t.Helper()
	needL2TPPort(t)
	if in.Name == "" {
		in.Name = "l2tp0"
	}
	in.Protocol = model.ProtocolL2TP
	if in.Subnet == "" {
		in.Subnet = "10.77.0.0/24"
	}
	if in.EndpointHost == "" {
		in.EndpointHost = "vpn.example.com"
	}
	return svc.Create(context.Background(), in)
}

// The port is not the operator's to choose, a key is made when none is
// given, and the MTU leaves room for L2TP and IPsec.
func TestAnL2TPTunnelIsMadeWithWhatItsClientsExpect(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	got, err := createL2TP(t, svc, CreateInterfaceInput{ListenPort: 4242})
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenPort != l2tpconf.PortL2TP {
		t.Errorf("port %d, want %d", got.ListenPort, l2tpconf.PortL2TP)
	}
	if got.MTU != l2tpconf.DefaultMTU {
		t.Errorf("MTU %d, want %d", got.MTU, l2tpconf.DefaultMTU)
	}
	if psk := got.L2TP.V.PSK; l2tpconf.CheckPSK(psk) != nil || len(psk) < 20 {
		t.Errorf("generated key %q", psk)
	}

	// One per server: the second would need the same fixed ports.
	_, err = createL2TP(t, svc, CreateInterfaceInput{Name: "l2tp1", Subnet: "10.78.0.0/24"})
	check(t, "second", err, wantErr{"protocol", "already has an L2TP tunnel"})
}

func TestL2TPInputIsChecked(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	for _, c := range []struct {
		name string
		in   CreateInterfaceInput
		want wantErr
	}{
		{"space in key", CreateInterfaceInput{PSK: "two words"}, wantErr{"psk", "pre-shared key is 1 to 64"}},
		{"long key", CreateInterfaceInput{PSK: strings.Repeat("k", 65)}, wantErr{"psk", "pre-shared key is 1 to 64"}},
		{"quote in key", CreateInterfaceInput{PSK: `has"quote1`}, wantErr{"psk", "pre-shared key"}},
		{"v6 subnet", CreateInterfaceInput{Subnet: "fd00::/64"}, wantErr{"subnet", "an L2TP subnet is IPv4"}},
		{"tiny subnet", CreateInterfaceInput{Subnet: "10.77.0.0/31"}, wantErr{"subnet", "a /30 or larger"}},
		{"big MTU", CreateInterfaceInput{MTU: 1501}, wantErr{"mtu", "an L2TP MTU is"}},
	} {
		_, err := createL2TP(t, svc, c.in)
		check(t, c.name, err, c.want)
	}
	// Plain L2TP on anything else means nothing either.
	_, err := svc.Create(context.Background(), CreateInterfaceInput{
		Name: "wg2", Protocol: model.ProtocolWireGuard, ListenPort: freeUDPPort(t),
		Subnet: "10.80.0.0/24", EndpointHost: "h", L2TPPlain: true,
	})
	check(t, "plain on wireguard", err, wantErr{"l2tpPlain", "applies to L2TP tunnels only"})
	// A key on anything else means nothing and is said so.
	_, err = svc.Create(context.Background(), CreateInterfaceInput{
		Name: "wg1", Protocol: model.ProtocolWireGuard, ListenPort: freeUDPPort(t),
		Subnet: "10.79.0.0/24", EndpointHost: "h", PSK: "SharedKey123",
	})
	check(t, "psk on wireguard", err, wantErr{"psk", "applies to L2TP tunnels only"})
}

func TestAnL2TPTunnelsKeyCanBeChangedButNotItsPort(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	iface, err := createL2TP(t, svc, CreateInterfaceInput{PSK: "FirstKey123"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	port := 1702
	_, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{ListenPort: &port})
	check(t, "port", err, wantErr{"listenPort", "always on UDP 1701"})

	bad := "no spaces allowed"
	_, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{PSK: &bad})
	check(t, "bad key", err, wantErr{"psk", "pre-shared key"})

	small := "10.77.0.0/31"
	_, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{Subnet: &small})
	check(t, "subnet", err, wantErr{"subnet", "a /30 or larger"})

	key := "SecondKey456"
	got, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{PSK: &key})
	if err != nil {
		t.Fatal(err)
	}
	if got.L2TP.V.PSK != key {
		t.Fatalf("key %q, want %q", got.L2TP.V.PSK, key)
	}

	// A WireGuard tunnel is not given one.
	var wg model.Interface
	db.Where("name = ?", "wg0").First(&wg)
	_, err = svc.Update(ctx, wg.ID, UpdateInterfaceInput{PSK: &key})
	check(t, "psk on wireguard", err, wantErr{"psk", "applies to L2TP tunnels only"})
}

// A node that predates L2TP has no agent to carry it.
func TestL2TPOnANodeNeedsANodeThatKnowsIt(t *testing.T) {
	db, _ := seeded(t)
	old := model.Node{Name: "old", Kind: model.KindRemote, Enabled: true, Version: "v2.6.6"}
	cur := model.Node{Name: "new", Kind: model.KindRemote, Enabled: true, Version: "v3.0.0"}
	for _, n := range []*model.Node{&old, &cur} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	_, err := createL2TP(t, svc, CreateInterfaceInput{NodeID: old.ID})
	check(t, "old node", err, wantErr{"nodeId", "L2TP needs W-UI 3.0 or later"})
	if _, err := createL2TP(t, svc, CreateInterfaceInput{NodeID: cur.ID}); err != nil {
		t.Fatalf("a 3.0 node: %v", err)
	}
	// And the one-per-server rule is per server: this one is not affected.
	if _, err := createL2TP(t, svc, CreateInterfaceInput{Name: "l2tp9", Subnet: "10.90.0.0/24"}); err != nil {
		t.Fatalf("this server, beside a node's: %v", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for v, want := range map[string]bool{
		"v3.0.0": true, "3.0.0": true, "v3.1.2": true, "v4.0.0": true, "v3.0.0-rc1": true,
		"v2.6.6": false, "v2.99.0": false, "": false, "garbage": false, "dev": true,
	} {
		if got := versionAtLeast(v, 3, 0); got != want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

// A customer on L2TP signs in the same way as on OpenVPN, and the password
// has to be one pppd's secrets file can hold as it is.
func TestL2TPCustomersSignInWithAUsernameAndPassword(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	l2 := model.Interface{Name: "l2tp0", Protocol: model.ProtocolL2TP, Enabled: true, ListenPort: 1701,
		Subnet: "10.77.0.0/24", EndpointHost: "vpn.example.com", MTU: 1400, NodeID: 1,
		L2TP: model.JSON(model.L2TPParams{PSK: "SharedKey123"})}
	if err := db.Create(&l2).Error; err != nil {
		t.Fatal(err)
	}
	pools := ipam.NewPools()
	if _, err := pools.Add(l2.ID, l2.Subnet); err != nil {
		t.Fatal(err)
	}
	svc := NewClients(db, pools, quietLog())
	expires := time.Now().Add(24 * time.Hour)
	ctx := context.Background()

	c, err := svc.Create(ctx, CreateInput{
		Name: "Roya", InterfaceIDs: []uint{l2.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "roya", OpenVPNPassword: "s3cret-pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := accountsOn(t, db, c.ID, l2.ID)
	if len(got) != 1 || got[0].Username != "roya" || got[0].Secret != "s3cret-pass" || got[0].PublicKey != "" {
		t.Fatalf("accounts: %+v", got)
	}

	// Generated when not typed, and usable as it is.
	c2, err := svc.Create(ctx, CreateInput{Name: "Sina", InterfaceIDs: []uint{l2.ID}, ExpiresAt: &expires, DeviceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	gen := accountsOn(t, db, c2.ID, l2.ID)
	if len(gen) != 1 || l2tpconf.CheckUsername(gen[0].Username) != nil || l2tpconf.CheckSecret(gen[0].Secret) != nil {
		t.Fatalf("generated credentials: %+v", gen)
	}

	// A password pppd would read as its own syntax is refused.
	_, err = svc.Create(ctx, CreateInput{
		Name: "Mina", InterfaceIDs: []uint{l2.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "mina", OpenVPNPassword: `pass"word1`,
	})
	if !errors.Is(err, ErrInvalid) || FieldOf(err) != "openvpnPassword" {
		t.Fatalf("a quote in an L2TP password: %v", err)
	}
	// The same name as a customer already on the tunnel is refused.
	_, err = svc.Create(ctx, CreateInput{
		Name: "Other", InterfaceIDs: []uint{l2.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "roya",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a duplicate username: %v", err)
	}
}

// A node is handed the key with the tunnel, and carries one L2TP tunnel.
func TestANodeIsHandedItsL2TPTunnel(t *testing.T) {
	db := testDB(t)
	ns := NewNodeSync(db, quietLog())
	ctx := context.Background()
	st := NodeState{Interface: NodeInterface{
		OriginID: 5, Name: "l2tp0", Protocol: model.ProtocolL2TP, Enabled: true, ListenPort: 1701,
		Subnet: "10.77.0.0/24", EndpointHost: "vpn.example.com", MTU: 1400,
		L2TP: &model.L2TPParams{PSK: "SharedKey123"},
	}}
	if err := ns.Apply(ctx, 1, st); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	if err := db.Where("origin_id = ?", 5).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.L2TP.V.PSK != "SharedKey123" {
		t.Fatalf("stored key %q", got.L2TP.V.PSK)
	}
	// The same tunnel again is an update, not a second one.
	if err := ns.Apply(ctx, 1, st); err != nil {
		t.Fatalf("re-applying the same tunnel: %v", err)
	}

	second := st
	second.Interface.OriginID = 6
	second.Interface.Name = "l2tp1"
	if err := ns.Apply(ctx, 1, second); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a second L2TP tunnel on one server: %v", err)
	}
	nokey := st
	nokey.Interface.L2TP = nil
	if err := ns.Apply(ctx, 1, nokey); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an L2TP tunnel with no key: %v", err)
	}

	// Other protocols' payloads do not grow the field.
	body, _ := json.Marshal(NodeInterface{OriginID: 1, Name: "wg0", Protocol: model.ProtocolWireGuard})
	if strings.Contains(string(body), "l2tp") {
		t.Fatalf("a WireGuard payload carries l2tp: %s", body)
	}
}

// The text an app imports carries what an app can import; an L2TP sign-in
// goes in only when it is all there is, and an archive carries everything.
func TestAnAppsBundleLeavesOutTheL2TPSignIn(t *testing.T) {
	byID := map[uint]model.Interface{
		1: {ID: 1, Protocol: model.ProtocolWireGuard},
		2: {ID: 2, Protocol: model.ProtocolL2TP},
	}
	wg := RenderedDevice{Account: model.Account{InterfaceID: 1}, Profile: backend.ClientProfile{Filename: "a.conf"}}
	l2 := RenderedDevice{Account: model.Account{InterfaceID: 2}, Profile: backend.ClientProfile{Filename: "a-l2tp.txt"}}

	names := func(ps []backend.ClientProfile) string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Filename)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		format string
		in     []RenderedDevice
		want   string
	}{
		{"", []RenderedDevice{wg, l2}, "a.conf"},
		{"base64", []RenderedDevice{wg, l2}, "a.conf"},
		{"zip", []RenderedDevice{wg, l2}, "a.conf,a-l2tp.txt"},
		{"", []RenderedDevice{l2}, "a-l2tp.txt"},
	} {
		if got := names(bundleParts(c.in, byID, c.format)); got != c.want {
			t.Errorf("format %q: %s, want %s", c.format, got, c.want)
		}
	}
}

// An existing service's key is kept as it is, and routers without IPsec can
// be let in -- each turned on and off without touching the other.
func TestAnL2TPTunnelForAnExistingServiceAndItsRouters(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	iface, err := createL2TP(t, svc, CreateInterfaceInput{PSK: "123", L2TPPlain: true})
	if err != nil {
		t.Fatal(err)
	}
	if iface.L2TP.V.PSK != "123" || !iface.L2TP.V.AllowPlain {
		t.Fatalf("made with %+v", iface.L2TP.V)
	}
	ctx := context.Background()
	off := false
	got, err := svc.Update(ctx, iface.ID, UpdateInterfaceInput{L2TPPlain: &off})
	if err != nil {
		t.Fatal(err)
	}
	if got.L2TP.V.AllowPlain || got.L2TP.V.PSK != "123" {
		t.Fatalf("turning plain L2TP off: %+v", got.L2TP.V)
	}
	key := "NewKey2026"
	got, err = svc.Update(ctx, iface.ID, UpdateInterfaceInput{PSK: &key})
	if err != nil {
		t.Fatal(err)
	}
	if got.L2TP.V.AllowPlain || got.L2TP.V.PSK != key {
		t.Fatalf("changing the key: %+v", got.L2TP.V)
	}
	on := true
	var wg model.Interface
	db.Where("name = ?", "wg0").First(&wg)
	_, err = svc.Update(ctx, wg.ID, UpdateInterfaceInput{L2TPPlain: &on})
	check(t, "plain on wireguard", err, wantErr{"l2tpPlain", "applies to L2TP tunnels only"})
}
