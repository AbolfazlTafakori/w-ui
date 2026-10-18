package service

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fleet"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// seeded is a database with the local node and one WireGuard tunnel, which
// most inputs refer to.
func seeded(t *testing.T) (*gorm.DB, *model.Interface) {
	t.Helper()
	db := testDB(t)
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	iface := &model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.9.0.0/24", EndpointHost: "vpn.example.com", NodeID: 1}
	if err := db.Create(iface).Error; err != nil {
		t.Fatal(err)
	}
	return db, iface
}

// Every message an operator can be shown for bad input is exact, names the
// field it belongs to, and is the one the reference page documents. These
// pin them: a reworded message fails here and in the docs check, so the two
// cannot drift apart unnoticed.

type wantErr struct {
	field string
	msg   string
}

func check(t *testing.T, name string, err error, want wantErr) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error, wanted %q", name, want.msg)
		return
	}
	if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotFound) {
		t.Errorf("%s: %v is neither invalid nor not-found", name, err)
	}
	if got := FieldOf(err); want.field != "" && got != want.field {
		t.Errorf("%s: field %q, want %q (%v)", name, got, want.field, err)
	}
	if !strings.Contains(err.Error(), want.msg) {
		t.Errorf("%s: got %q, want it to contain %q", name, err.Error(), want.msg)
	}
}

func TestClientInputErrors(t *testing.T) {
	ctx := context.Background()
	db, _ := seeded(t)
	clients := NewClients(db, ipam.NewPools(), quietLog())
	past := time.Now().Add(-time.Hour)

	cases := []struct {
		name string
		in   CreateInput
		want wantErr
	}{
		{"no name", CreateInput{InterfaceIDs: []uint{1}}, wantErr{"name", "name is required"}},
		{"no interface", CreateInput{Name: "x"}, wantErr{"interfaceId", "choose at least one server for this customer"}},
		{"unknown interface", CreateInput{Name: "x", InterfaceIDs: []uint{99}}, wantErr{"", "not found: interface 99"}},
		{"device limit", CreateInput{Name: "x", InterfaceIDs: []uint{1}, DeviceLimit: 65}, wantErr{"deviceLimit", "device limit must be between 0 and 50"}},
		{"expiry in the past", CreateInput{Name: "x", InterfaceIDs: []uint{1}, ExpiresAt: &past}, wantErr{"expiresAt", "expiry is in the past"}},
	}
	for _, c := range cases {
		_, err := clients.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

// freeUDPPort is one this machine lets a test bind, since the tunnel's port
// is checked before the rest of its input.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no UDP port can be bound here")
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	return port
}

// freeUDPPorts is n ports nothing holds on this machine, all different: held
// open together until every one is drawn, so the system cannot hand the same
// one out twice. A test that names a port outright fails wherever that port
// is taken or reserved, as Windows reserves whole ranges for Hyper-V.
func freeUDPPorts(t *testing.T, n int) []int {
	t.Helper()
	var held []net.PacketConn
	defer func() {
		for _, pc := range held {
			pc.Close()
		}
	}()
	out := make([]int, 0, n)
	for len(out) < n {
		pc, err := net.ListenPacket("udp", ":0")
		if err != nil {
			t.Skip("no UDP port can be bound here")
		}
		held = append(held, pc)
		out = append(out, pc.LocalAddr().(*net.UDPAddr).Port)
	}
	return out
}

func TestInterfaceInputErrors(t *testing.T) {
	ctx := context.Background()
	db, _ := seeded(t)
	ifaces := NewInterfaces(db, ipam.NewPools(), quietLog())
	port := freeUDPPort(t)

	cases := []struct {
		name string
		in   CreateInterfaceInput
		want wantErr
	}{
		{"no name", CreateInterfaceInput{Protocol: "wireguard", ListenPort: port, Subnet: "10.9.0.0/24", EndpointHost: "vpn.example.com"}, wantErr{"name", "name is required"}},
		{"unknown protocol", CreateInterfaceInput{Name: "wg0", Protocol: "tinc", ListenPort: port, Subnet: "10.9.0.0/24", EndpointHost: "h"}, wantErr{"protocol", `unknown protocol "tinc"`}},
		{"port out of range", CreateInterfaceInput{Name: "wg0", Protocol: "wireguard", ListenPort: 70000, Subnet: "10.9.0.0/24", EndpointHost: "h"}, wantErr{"listenPort", "listen port 70000 is out of range"}},
		{"bad subnet", CreateInterfaceInput{Name: "wg0", Protocol: "wireguard", ListenPort: port, Subnet: "not-a-subnet", EndpointHost: "h"}, wantErr{"subnet", `subnet "not-a-subnet"`}},
		{"no endpoint", CreateInterfaceInput{Name: "wg0", Protocol: "wireguard", ListenPort: port, Subnet: "10.9.0.0/24"}, wantErr{"endpointHost", "endpoint host is required"}},
		{"mtu", CreateInterfaceInput{Name: "wg0", Protocol: "wireguard", ListenPort: port, Subnet: "10.9.0.0/24", EndpointHost: "h", MTU: 100}, wantErr{"mtu", "MTU 100 is out of range (576-9000)"}},
		{"awg on openvpn", CreateInterfaceInput{Name: "o0", Protocol: "openvpn", ListenPort: port, Subnet: "10.9.0.0/24", EndpointHost: "h", Mode: "amnezia"}, wantErr{"mode", "AmneziaWG mode applies to WireGuard only"}},
	}
	for _, c := range cases {
		_, err := ifaces.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

func TestHostInputErrors(t *testing.T) {
	ctx := context.Background()
	db, _ := seeded(t)
	hosts := NewHosts(db, quietLog())
	cases := []struct {
		name string
		in   HostInput
		want wantErr
	}{
		{"no interface", HostInput{Name: "a", Address: "1.2.3.4"}, wantErr{"interfaceId", "a host has to belong to an interface"}},
		{"no name", HostInput{InterfaceID: 1, Address: "1.2.3.4"}, wantErr{"name", "give the host a name so the list can be read later"}},
		{"no address", HostInput{InterfaceID: 1, Name: "a"}, wantErr{"address", "a host needs the address customers will dial"}},
		{"bad port", HostInput{InterfaceID: 1, Name: "a", Address: "1.2.3.4", Port: 99999}, wantErr{"port", "99999 is not a port number"}},
	}
	for _, c := range cases {
		_, err := hosts.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

func TestSubscriptionSettingsErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	subs := NewSubscriptions(db, nil, nil, quietLog())
	cases := []struct {
		name string
		in   SubSettings
		want wantErr
	}{
		{"api path", SubSettings{Path: "/api/x/", UpdateHours: 12}, wantErr{"path", "the path cannot start with /api/, which the panel serves"}},
		{"short path", SubSettings{Path: "/a/", UpdateHours: 12}, wantErr{"path", "that path is too short to be worth having"}},
		{"taken path", SubSettings{Path: "/api/", UpdateHours: 12}, wantErr{"path", `"/api/" is already used by the panel itself`}},
		{"cert alone", SubSettings{Path: "/subscribe/", UpdateHours: 12, CertFile: "/x.pem"}, wantErr{"certFile", "a certificate and its key go together"}},
		{"interval", SubSettings{Path: "/subscribe/", UpdateHours: 0}, wantErr{"updateHours", ""}},
		{"template", SubSettings{Path: "/subscribe/", UpdateHours: 12, Template: "nope"}, wantErr{"template", `"nope" is not a template; choose one of`}},
		{"announce", SubSettings{Path: "/subscribe/", UpdateHours: 12, Announce: strings.Repeat("x", 1001)}, wantErr{"announce", "that notice is too long"}},
	}
	for _, c := range cases {
		_, err := subs.SaveSettings(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

func TestPanelSettingsErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	settings := NewSettings(db, "en")
	base, err := settings.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	with := func(f func(*PanelSettings)) PanelSettings { p := base; f(&p); return p }
	cases := []struct {
		name string
		in   PanelSettings
		want string
	}{
		{"port", with(func(p *PanelSettings) { p.WebPort = 70000 }), "panel port 70000 is out of range"},
		{"listen", with(func(p *PanelSettings) { p.WebListen = "not-an-ip" }), `listen IP "not-an-ip" is not an address`},
		{"base path", with(func(p *PanelSettings) { p.WebBasePath = "/a/b/" }), "the URI path is one segment, like /panel/"},
		{"session", with(func(p *PanelSettings) { p.SessionMaxAge = 0 }), "session duration must be between 1 and"},
		{"locale", with(func(p *PanelSettings) { p.DefaultLocale = "de" }), `unknown language "de"`},
		{"proxy", with(func(p *PanelSettings) { p.TrustedProxyCIDRs = "nope" }), `trusted proxy "nope" is not an address or a CIDR`},
	}
	for _, c := range cases {
		_, err := settings.Save(ctx, c.in)
		check(t, c.name, err, wantErr{"", c.want})
	}
}

func TestNodeInputErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	nodes := fleet.NewNodes(db, quietLog())
	cases := []struct {
		name string
		in   fleet.NodeInput
		want wantErr
	}{
		{"no name", fleet.NodeInput{Address: "https://n.example.com:2096", Token: "wui_x"}, wantErr{"name", "a node needs a name"}},
		{"no address", fleet.NodeInput{Name: "n", Token: "wui_x"}, wantErr{"address", "a node needs an address"}},
		{"bad address", fleet.NodeInput{Name: "n", Address: "n.example.com", Token: "wui_x"}, wantErr{"address", "is not a URL. It should look like https://vpn2.example.com:2096"}},
		{"no token", fleet.NodeInput{Name: "n", Address: "https://n.example.com:2096"}, wantErr{"token", "a node needs an access token"}},
	}
	for _, c := range cases {
		_, err := nodes.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}
