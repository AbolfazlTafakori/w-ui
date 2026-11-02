package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/stealth"
)

func wgInput(name string, port int, subnet string) CreateInterfaceInput {
	return CreateInterfaceInput{
		Name: name, Protocol: model.ProtocolWireGuard, ListenPort: port, Subnet: subnet,
		EndpointHost: "vpn.example.com", MTU: 1420, DNS: "1.1.1.1", NATInterface: "eth0",
	}
}

func wuiUDP(in CreateInterfaceInput, port int) CreateInterfaceInput {
	in.Transport, in.TransportPort = model.TransportVeil, port
	return in
}

// A WireGuard tunnel's transport is plain UDP, or veil beside it on a
// port of its own, chosen as OpenVPN's transport is.
func TestAWireGuardTunnelsTransportIsUDPOrWuiUDP(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 4)

	wg0, err := svc.Create(ctx, wuiUDP(wgInput("wg0", ports[0], "10.66.0.0/24"), ports[1]))
	if err != nil {
		t.Fatal(err)
	}
	if wg0.WireGuardTransport() != model.TransportVeil || wg0.StealthUDP() != ports[1] || wg0.ListenPort != ports[0] {
		t.Fatalf("made as %s on %d, veil on %d", wg0.WireGuardTransport(), wg0.ListenPort, wg0.StealthUDP())
	}
	plain := wgInput("plain", ports[2], "10.69.0.0/24")
	plain.Transport = model.TransportUDP
	if p, err := svc.Create(ctx, plain); err != nil || p.WireGuardTransport() != model.TransportUDP {
		t.Fatalf("plain UDP named: %v", err)
	}
	svc.Delete(ctx, wg0.ID+1)

	for _, tc := range []struct {
		what  string
		in    CreateInterfaceInput
		field string
	}{
		{"the tunnel's own port", wuiUDP(wgInput("wg1", ports[2], "10.67.0.0/24"), ports[2]), "transportPort"},
		{"another tunnel's port", wuiUDP(wgInput("wg1", ports[2], "10.67.0.0/24"), ports[0]), "transportPort"},
		{"another tunnel's veil port", wuiUDP(wgInput("wg1", ports[2], "10.67.0.0/24"), ports[1]), "transportPort"},
		{"a port out of range", wuiUDP(wgInput("wg1", ports[2], "10.67.0.0/24"), 70000), "transportPort"},
		{"no port", wuiUDP(wgInput("wg1", ports[2], "10.67.0.0/24"), 0), "transportPort"},
		{"a transport there is none of", func() CreateInterfaceInput {
			in := wgInput("wg1", ports[2], "10.67.0.0/24")
			in.Transport = "tcp"
			return in
		}(), "transport"},
		{"a port for plain UDP", func() CreateInterfaceInput {
			in := wgInput("wg1", ports[2], "10.67.0.0/24")
			in.TransportPort = ports[3]
			return in
		}(), "transportPort"},
	} {
		if _, err := svc.Create(ctx, tc.in); FieldOf(err) != tc.field {
			t.Errorf("%s: %v, field %q", tc.what, err, FieldOf(err))
		}
	}
	// A tunnel's own port on another's veil port is refused too.
	if _, err := svc.Create(ctx, wgInput("wg1", ports[1], "10.67.0.0/24")); FieldOf(err) != "listenPort" {
		t.Fatalf("a tunnel on another's veil port: %v", err)
	}
	// AmneziaWG's packets are not WireGuard's: no veil in front of them.
	awg := wuiUDP(wgInput("awg0", ports[2], "10.68.0.0/24"), ports[3])
	awg.Mode = model.ModeAmnezia
	if _, err := svc.Create(ctx, awg); FieldOf(err) != "transport" {
		t.Fatalf("veil on AmneziaWG: %v", err)
	}

	// Moved, then back to plain UDP.
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{ListenPort: &ports[1]}); FieldOf(err) != "listenPort" {
		t.Fatalf("the tunnel moved onto its own veil port: %v", err)
	}
	got, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportPort: &ports[3]})
	if err != nil || got.StealthUDP() != ports[3] {
		t.Fatalf("moving veil: %v, port %d", err, got.StealthUDP())
	}
	udp := model.TransportUDP
	got, err = svc.Update(ctx, wg0.ID, UpdateInterfaceInput{Transport: &udp})
	if err != nil || got.WireGuardTransport() != model.TransportUDP || got.ListenPort != ports[0] {
		t.Fatalf("back to plain UDP: %v, %s", err, got.WireGuardTransport())
	}
	// And the API says nothing of it once it is off.
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "stealth") {
		t.Fatalf("a tunnel on plain UDP answers with %s", b)
	}
	// veil is package stealth's name for it.
	if model.TransportVeil != stealth.Veil {
		t.Fatal("the panel and the protocol name veil apart")
	}
}

func TestEveryWireGuardDeviceHasATransportSecret(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 2) // made the way the panel made devices before
	other := model.Interface{Name: "ovpn", Protocol: model.ProtocolOpenVPN, Subnet: "10.88.0.0/24", ListenPort: 1194}
	db.Create(&other)
	db.Create(&model.Account{ClientID: c.ID, InterfaceID: other.ID, DeviceName: "laptop", IP: "10.88.0.2"})
	managed := model.Interface{Name: "far", Protocol: model.ProtocolWireGuard, Subnet: "10.99.0.0/24", ListenPort: 1, Managed: true, NodeID: 2}
	db.Create(&managed)
	db.Create(&model.Account{ClientID: c.ID, InterfaceID: managed.ID, DeviceName: "far", IP: "10.99.0.2"})

	if err := EnsureStealthSecrets(ctx, db); err != nil {
		t.Fatal(err)
	}
	var accs []model.Account
	db.Order("id").Find(&accs)
	seen := map[string]bool{}
	for _, a := range accs {
		switch a.InterfaceID {
		case 1:
			if _, err := stealth.ParseSecret(a.StealthSecret); err != nil || seen[a.StealthSecret] {
				t.Fatalf("device %s: secret %q, %v", a.DeviceName, a.StealthSecret, err)
			}
			seen[a.StealthSecret] = true
		default:
			if a.StealthSecret != "" {
				t.Fatalf("%s has a secret this panel is not the one to give", a.DeviceName)
			}
		}
	}
	// A second pass changes nothing anybody was handed.
	before := accs[0].StealthSecret
	EnsureStealthSecrets(ctx, db)
	var again model.Account
	db.First(&again, accs[0].ID)
	if again.StealthSecret != before {
		t.Fatal("a second pass replaced a secret")
	}

	// A new device gets one, and rotating its keys replaces it.
	clients := NewClients(db, ipam.NewPools(), quietLog())
	if _, err := clients.RotateKeys(ctx, c.ID, RotateInput{}); err != nil {
		t.Fatal(err)
	}
	db.First(&again, accs[0].ID)
	if again.StealthSecret == before || again.StealthSecret == "" {
		t.Fatal("rotating the keys left the transport secret")
	}
}

func TestTheWuiProfileCarriesEachFileAndItsTransport(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 1)
	if err := db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(model.StealthParams{Transport: model.TransportVeil, Port: 2096})).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Account{}).Where("client_id = ?", c.ID).Update("private_key", "priv")
	db.Create(&model.Host{InterfaceID: 1, Name: "spare", Enabled: true, Address: "spare.example.com:8443"})
	db.Create(&model.Host{InterfaceID: 1, Name: "first", Enabled: true, Address: "first.example.com", Priority: -1})
	if err := EnsureStealthSecrets(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(ctx, c.ID)

	b, err := s.Serve(ctx, token, "wui")
	if err != nil {
		t.Fatal(err)
	}
	var p WUIProfile
	if err := json.Unmarshal(b.Body, &p); err != nil || b.ContentType != "application/json; charset=utf-8" {
		t.Fatalf("%s: %v", b.ContentType, err)
	}
	if p.Version != 1 || p.ServerTime == 0 || len(p.Tunnels) != 1 {
		t.Fatalf("profile: %+v", p)
	}
	tun := p.Tunnels[0]
	if !strings.HasPrefix(tun.WireGuard, "CONFIG") || len(tun.Transports) != 1 {
		t.Fatalf("tunnel: %+v", tun)
	}
	tr := tun.Transports[0]
	var acc model.Account
	db.Where("client_id = ?", c.ID).First(&acc)
	if tr.Type != stealth.Veil || tr.Secret != acc.StealthSecret || tr.MTU != 1420 ||
		strings.Join(tr.Endpoints, " ") != "first.example.com:2096 spare.example.com:2096" {
		t.Fatalf("transport: %+v", tr)
	}

	// Without veil the file is there and no transport is.
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(model.StealthParams{}))
	b, _ = s.Serve(ctx, token, "wui")
	json.Unmarshal(b.Body, &p)
	if len(p.Tunnels) != 1 || len(p.Tunnels[0].Transports) != 0 {
		t.Fatalf("profile without veil: %+v", p)
	}
	// Every other format is as it was.
	if conf, err := s.Serve(ctx, token, "conf"); err != nil || strings.Contains(string(conf.Body), "wui") {
		t.Fatalf("the .conf changed: %v", err)
	}
	if _, err := s.Serve(ctx, token, "nope"); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("an unknown format: %v", err)
	}
}
