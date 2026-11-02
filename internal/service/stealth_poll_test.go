package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
)

func wuiPoll(in CreateInterfaceInput, port int, ws WSSettings) CreateInterfaceInput {
	in.Transport, in.TransportPort, in.TransportWS = model.TransportRelay, port, &ws
	return in
}

// relay takes drift's settings: a front, a path the panel draws, and a
// key of the transport's own, turned over as drift's is.
func TestAWireGuardTunnelTakesWuiPollBehindAFront(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	tcp := freeTCPPort(t)

	wg0, err := svc.Create(ctx, wuiPoll(wgInput("wg0", ports[0], "10.66.0.0/24"), tcp, WSSettings{Host: "Poll.Example.com.", Origin: "https://127.0.0.1:8443"}))
	if err != nil {
		t.Fatal(err)
	}
	p := wg0.Stealth.V
	if wg0.WireGuardTransport() != model.TransportRelay || wg0.StealthPoll() != tcp || p.Host != "poll.example.com" || p.Cookie != "" {
		t.Fatalf("made as %s on %d: %+v", wg0.WireGuardTransport(), wg0.StealthPoll(), p)
	}
	if len(p.Path) != 1+wsPathLen || p.FrontPortOr() != 443 || !p.Fronted() || !p.Proven() {
		t.Fatalf("the path drawn is %q, the front's port %d", p.Path, p.FrontPortOr())
	}
	if _, err := stealth.ParseTransportKey(p.Key); err != nil || keyDays(p.Transport) != wsKeyDays {
		t.Fatalf("the transport's key: %v, turned over every %d days", err, keyDays(p.Transport))
	}
	if wg0.StealthOpen() || model.TransportNetwork(model.TransportRelay) != "tcp" {
		t.Fatal("with no front elsewhere the port is opened")
	}
	if _, err := svc.Create(ctx, wuiPoll(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), WSSettings{})); FieldOf(err) != "transportWs.host" {
		t.Fatalf("no host: %v", err)
	}

	// drift in its place starts with a path and a key of its own.
	ws := model.TransportDrift
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{Transport: &ws, TransportWS: &WSSettings{Host: "poll.example.com", Path: p.Path}}); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	db.First(&got, wg0.ID)
	if q := got.Stealth.V; q.Transport != model.TransportDrift || q.Path == p.Path || q.Key == p.Key {
		t.Fatalf("drift after relay kept what was relay's: %+v", q)
	}
}

// relay is a covered transport: bare never shares its front or its
// path.
func TestWuiPlainNeverSharesAFrontOrAPathWithWuiPoll(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	if _, err := svc.Create(ctx, wuiPoll(wgInput("wg0", ports[0], "10.66.0.0/24"), freeTCPPort(t), WSSettings{Host: "front.example.com", Path: "/media/7f3a9c2e1b"})); err != nil {
		t.Fatal(err)
	}
	decoy := "http://127.0.0.1:8080"
	for _, tc := range []struct {
		ws    WSSettings
		field string
	}{
		{WSSettings{Host: "front.example.com", Origin: decoy}, "transportWs.host"},
		{WSSettings{Host: "plain.example.com", Path: "/media/7f3a9c2e1b", Origin: decoy}, "transportWs.path"},
	} {
		if _, err := svc.Create(ctx, wuiPlain(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), tc.ws)); FieldOf(err) != tc.field {
			t.Errorf("%+v: %v", tc.ws, err)
		}
	}
}

func TestTheWuiProfileCarriesWuiPoll(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 1)
	key, _ := stealth.NewTransportKey()
	poll := model.StealthParams{Transport: model.TransportRelay, Port: 8081, Host: "front.example.com", Path: "/media/7f3a9c2e1b", Key: key.String(), KeyAt: time.Now().Unix()}
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(poll))
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
	if tr.Type != stealth.Relay || tr.URL != "https://front.example.com/media/7f3a9c2e1b" || tr.Key != key.Public() ||
		strings.Join(tr.Endpoints, " ") != "front.example.com:443" || tr.Secret != acc.StealthSecret || tr.Cookie != "" || len(tr.Pins) != 0 {
		t.Fatalf("transport: %+v", tr)
	}
	if strings.Contains(string(b.Body), key.String()) {
		t.Fatal("the profile carries the transport's private key")
	}
	poll.FrontPort = 8443
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(poll))
	b, _ = s.Serve(ctx, token, "wui")
	json.Unmarshal(b.Body, &p)
	if tr := p.Tunnels[0].Transports[0]; tr.URL != "https://front.example.com:8443/media/7f3a9c2e1b" || tr.Endpoints[0] != "front.example.com:8443" {
		t.Fatalf("on 8443: %+v", tr)
	}
}
