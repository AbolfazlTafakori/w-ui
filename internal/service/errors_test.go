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
		{"device limit", CreateInput{Name: "x", InterfaceIDs: []uint{1}, DeviceLimit: 65}, wantErr{"deviceLimit", "device limit must be between 1 and 64"}},
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

func TestOutboundAndBalancerInputErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	outs := NewOutbounds(db, quietLog())
	bals := NewBalancers(db, quietLog())

	ocases := []struct {
		name string
		in   OutboundInput
		want wantErr
	}{
		{"no tag", OutboundInput{Kind: "socks", Address: "1.2.3.4:1080"}, wantErr{"tag", "an outbound needs a tag; routing rules refer to it by that name"}},
		{"bad tag", OutboundInput{Tag: "bad tag!", Kind: "socks", Address: "1.2.3.4:1080"}, wantErr{"tag", "a tag can only contain letters, digits, - and _"}},
		{"built-in tag", OutboundInput{Tag: "direct", Kind: "socks", Address: "1.2.3.4:1080"}, wantErr{"tag", `"direct" is the name of a built-in outbound`}},
		{"unknown kind", OutboundInput{Tag: "x", Kind: "carrier-pigeon", Address: "1.2.3.4:1080"}, wantErr{"kind", `"carrier-pigeon" is not an outbound kind this panel serves`}},
		{"no address", OutboundInput{Tag: "x", Kind: "socks"}, wantErr{"address", "an outbound of this kind needs an address to reach"}},
		{"wireguard without peer", OutboundInput{Tag: "x", Kind: "wireguard", Address: "1.2.3.4:51820", PrivateKey: strings.Repeat("A", 43) + "="}, wantErr{"peerPubKey", "a WireGuard hop needs the upstream peer's public key"}},
	}
	for _, c := range ocases {
		_, err := outs.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}

	bcases := []struct {
		name string
		in   BalancerInput
		want wantErr
	}{
		{"no tag", BalancerInput{Members: []string{"a"}}, wantErr{"tag", "a balancer needs a tag; rules refer to it by that name"}},
		{"no members", BalancerInput{Tag: "b"}, wantErr{"members", "a balancer needs at least one outbound to send traffic to"}},
		{"bad strategy", BalancerInput{Tag: "b", Members: []string{"a"}, Strategy: "roundrobin"}, wantErr{"strategy", `"roundrobin" is not a strategy; use random or leastPing`}},
		{"unknown member", BalancerInput{Tag: "b", Members: []string{"nope"}}, wantErr{"members", `there is no outbound called "nope"`}},
	}
	for _, c := range bcases {
		_, err := bals.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}

func TestRoutingRuleInputErrors(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	routing := NewRouting(db, quietLog())
	cases := []struct {
		name string
		in   RuleInput
		want wantErr
	}{
		{"no comment", RuleInput{DestIPs: "1.1.1.1", OutboundTag: "direct"}, wantErr{"name", "give the rule a comment so the list can be read later"}},
		{"matches nothing", RuleInput{Name: "r", OutboundTag: "direct"}, wantErr{"destIps", "the rule matches nothing as written; fill in at least one criterion"}},
		{"bad network", RuleInput{Name: "r", DestIPs: "1.1.1.1", Network: "sctp", OutboundTag: "direct"}, wantErr{"network", `"sctp" is not a network the router matches; use tcp, udp or icmp`}},
		{"icmp with ports", RuleInput{Name: "r", DestIPs: "1.1.1.1", Network: "icmp", Ports: "53", OutboundTag: "direct"}, wantErr{"network", "icmp has no ports; drop the ports or pick tcp or udp"}},
		{"unknown outbound", RuleInput{Name: "r", DestIPs: "1.1.1.1", OutboundTag: "nowhere"}, wantErr{"outboundTag", `there is no outbound or balancer called "nowhere"`}},
		{"unknown client", RuleInput{Name: "r", Clients: "abc", OutboundTag: "direct"}, wantErr{"clients", `a client is named by id here, and "abc" is not one`}},
	}
	for _, c := range cases {
		_, err := routing.CreateRule(ctx, c.in)
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
	nodes := NewNodes(db, quietLog())
	cases := []struct {
		name string
		in   NodeInput
		want wantErr
	}{
		{"no name", NodeInput{Address: "https://n.example.com:2096", Token: "wui_x"}, wantErr{"name", "a node needs a name"}},
		{"no address", NodeInput{Name: "n", Token: "wui_x"}, wantErr{"address", "a node needs an address"}},
		{"bad address", NodeInput{Name: "n", Address: "n.example.com", Token: "wui_x"}, wantErr{"address", "is not a URL. It should look like https://vpn2.example.com:2096"}},
		{"no token", NodeInput{Name: "n", Address: "https://n.example.com:2096"}, wantErr{"token", "a node needs an access token"}},
	}
	for _, c := range cases {
		_, err := nodes.Create(ctx, c.in)
		check(t, c.name, err, c.want)
	}
}
