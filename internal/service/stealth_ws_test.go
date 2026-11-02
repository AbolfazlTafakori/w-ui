package service

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skip("no TCP port can be bound here")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func wuiWS(in CreateInterfaceInput, port int, ws WSSettings) CreateInterfaceInput {
	in.Transport, in.TransportPort, in.TransportWS = model.TransportDrift, port, &ws
	return in
}

// drift is the third of a WireGuard tunnel's transports: a front, a path
// the panel draws when none is given, and a key of the transport's own.
func TestAWireGuardTunnelTakesWuiWSBehindAFront(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	tcp := freeTCPPort(t)

	wg0, err := svc.Create(ctx, wuiWS(wgInput("wg0", ports[0], "10.66.0.0/24"), tcp, WSSettings{Host: "Front.Example.com.", Origin: "http://127.0.0.1:8080/"}))
	if err != nil {
		t.Fatal(err)
	}
	p := wg0.Stealth.V
	if wg0.WireGuardTransport() != model.TransportDrift || wg0.StealthWS() != tcp || p.Host != "front.example.com" || p.Origin != "http://127.0.0.1:8080" {
		t.Fatalf("made as %s on %d: %+v", wg0.WireGuardTransport(), wg0.StealthWS(), p)
	}
	if len(p.Path) != 1+wsPathLen || p.Path[0] != '/' || p.FrontPortOr() != 443 {
		t.Fatalf("the path drawn is %q, the front's port %d", p.Path, p.FrontPortOr())
	}
	if _, err := stealth.ParseTransportKey(p.Key); err != nil || p.KeyAt == 0 || p.PreviousKey != "" {
		t.Fatalf("the transport's key: %v, drawn at %d", err, p.KeyAt)
	}
	if wg0.StealthOpen() {
		t.Fatal("with no front elsewhere the port is opened")
	}

	for _, tc := range []struct {
		what  string
		ws    WSSettings
		field string
	}{
		{"no host", WSSettings{}, "transportWs.host"},
		{"a host with a scheme", WSSettings{Host: "https://front.example.com"}, "transportWs.host"},
		{"a short path", WSSettings{Host: "f.example", Path: "/short"}, "transportWs.path"},
		{"a path with a query", WSSettings{Host: "f.example", Path: "/abcdefghijkl?x"}, "transportWs.path"},
		{"a decoy with a path", WSSettings{Host: "f.example", Origin: "http://127.0.0.1:8080/site"}, "transportWs.origin"},
		{"a decoy of ftp", WSSettings{Host: "f.example", Origin: "ftp://127.0.0.1"}, "transportWs.origin"},
		{"a front that is no address", WSSettings{Host: "f.example", Proxies: "10.0.0.0/8, cdn"}, "transportWs.proxies"},
		{"a header that is none", WSSettings{Host: "f.example", ClientHeader: "X Real IP"}, "transportWs.clientHeader"},
		{"a front port out of range", WSSettings{Host: "f.example", FrontPort: 70000}, "transportWs.frontPort"},
	} {
		if _, err := svc.Create(ctx, wuiWS(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), tc.ws)); FieldOf(err) != tc.field {
			t.Errorf("%s: %v, field %q", tc.what, err, FieldOf(err))
		}
	}

	// New settings with an empty path keep the path and the key.
	ws := WSSettings{Host: "cdn.example.net", FrontPort: 8443, Proxies: "198.51.100.0/24 203.0.113.9", ClientHeader: "CF-Connecting-IP"}
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportWS: &ws}); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	db.First(&got, wg0.ID)
	q := got.Stealth.V
	if q.Host != "cdn.example.net" || q.FrontPort != 8443 || q.Path != p.Path || q.Key != p.Key || q.ClientHeader != "CF-Connecting-IP" || !got.StealthOpen() {
		t.Fatalf("after new settings: %+v", q)
	}

	// veil in its place leaves nothing of drift.
	udp := model.TransportVeil
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{Transport: &udp, TransportPort: &ports[1]}); err != nil {
		t.Fatal(err)
	}
	db.First(&got, wg0.ID)
	if got.Stealth.V != (model.StealthParams{Transport: model.TransportVeil, Port: ports[1]}) {
		t.Fatalf("veil after drift: %+v", got.Stealth.V)
	}
}

// A drift key turns over after wsKeyDays, and the one before is kept;
// a tunnel another panel manages keeps the keys it came with.
func TestAWuiWSKeyTurnsOverAndTheOneBeforeStays(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	seed(t, db, 1)
	old, _ := newWSKey()
	start := time.Unix(1_700_000_000, 0)
	ws := model.StealthParams{Transport: model.TransportDrift, Port: 8081, Host: "f.example", Path: "/abcdefghijklmn", Key: old, KeyAt: start.Unix()}
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(ws))
	managed := model.Interface{Name: "far", Protocol: model.ProtocolWireGuard, ListenPort: 51900, Subnet: "10.90.0.0/24", Managed: true, Stealth: model.JSON(ws)}
	if err := db.Create(&managed).Error; err != nil {
		t.Fatal(err)
	}

	read := func(id uint) model.StealthParams {
		var i model.Interface
		db.First(&i, id)
		return i.Stealth.V
	}
	if err := EnsureStealthKeys(ctx, db, start.Add(29*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if read(1).Key != old {
		t.Fatal("a key turned over before its time")
	}
	if err := EnsureStealthKeys(ctx, db, start.Add(wsKeyDays*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	p := read(1)
	if p.Key == old || p.PreviousKey != old || p.KeyAt != start.Add(wsKeyDays*24*time.Hour).Unix() {
		t.Fatalf("after its time: %+v", p)
	}
	if read(managed.ID).Key != old {
		t.Fatal("a tunnel another panel manages had its key turned over here")
	}
}

func TestTheWuiProfileCarriesWuiWS(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 1)
	key, _ := stealth.NewTransportKey()
	ws := model.StealthParams{Transport: model.TransportDrift, Port: 8081, Host: "front.example.com", Path: "/media/7f3a9c2e1b", Key: key.String(), KeyAt: 1}
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(ws))
	db.Model(&model.Account{}).Where("client_id = ?", c.ID).Update("private_key", "priv")
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
	if err := json.Unmarshal(b.Body, &p); err != nil || len(p.Tunnels) != 1 || len(p.Tunnels[0].Transports) != 1 {
		t.Fatalf("profile: %+v %v", p, err)
	}
	tr := p.Tunnels[0].Transports[0]
	var acc model.Account
	db.Where("client_id = ?", c.ID).First(&acc)
	if tr.Type != stealth.Drift || tr.URL != "wss://front.example.com/media/7f3a9c2e1b" || tr.Key != key.Public() ||
		strings.Join(tr.Endpoints, " ") != "front.example.com:443" || tr.Secret != acc.StealthSecret || tr.MTU != 1420 {
		t.Fatalf("transport: %+v", tr)
	}
	if strings.Contains(string(b.Body), key.String()) {
		t.Fatal("the profile carries the transport's private key")
	}

	// A front on another port is in the address.
	ws.FrontPort = 8443
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(ws))
	b, _ = s.Serve(ctx, token, "wui")
	json.Unmarshal(b.Body, &p)
	if tr := p.Tunnels[0].Transports[0]; tr.URL != "wss://front.example.com:8443/media/7f3a9c2e1b" || tr.Endpoints[0] != "front.example.com:8443" {
		t.Fatalf("on 8443: %+v", tr)
	}
}
