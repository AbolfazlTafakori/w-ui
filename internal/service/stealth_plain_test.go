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

func wuiPlain(in CreateInterfaceInput, port int, ws WSSettings) CreateInterfaceInput {
	in.Transport, in.TransportPort, in.TransportWS = model.TransportBare, port, &ws
	return in
}

// bare is the fourth of a WireGuard tunnel's transports: drift's
// front, on port 80, with a decoy that must be set, a cookie name the panel
// draws, and a key of the transport's own.
func TestAWireGuardTunnelTakesWuiPlainBehindAFront(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	tcp := freeTCPPort(t)

	wg0, err := svc.Create(ctx, wuiPlain(wgInput("wg0", ports[0], "10.66.0.0/24"), tcp, WSSettings{Host: "Plain.Example.com", Origin: "http://127.0.0.1:8080"}))
	if err != nil {
		t.Fatal(err)
	}
	p := wg0.Stealth.V
	if wg0.WireGuardTransport() != model.TransportBare || wg0.StealthPlain() != tcp || wg0.StealthWS() != 0 || p.Host != "plain.example.com" {
		t.Fatalf("made as %s on %d: %+v", wg0.WireGuardTransport(), wg0.StealthPlain(), p)
	}
	if len(p.Path) != 1+wsPathLen || p.FrontPortOr() != 80 || !stealth.CookieName(p.Cookie) || len(p.Cookie) < 8 || len(p.Cookie) > 16 {
		t.Fatalf("the path drawn is %q, the cookie %q, the front's port %d", p.Path, p.Cookie, p.FrontPortOr())
	}
	if _, err := stealth.ParseTransportKey(p.Key); err != nil || p.KeyAt == 0 {
		t.Fatalf("the transport's key: %v", err)
	}
	if wg0.StealthOpen() {
		t.Fatal("with no front elsewhere the port is opened")
	}

	for _, tc := range []struct {
		what  string
		ws    WSSettings
		field string
	}{
		{"no decoy", WSSettings{Host: "p.example"}, "transportWs.origin"},
		{"no host", WSSettings{Origin: "http://127.0.0.1:8080"}, "transportWs.host"},
		{"a cookie with a blank", WSSettings{Host: "p.example", Origin: "http://127.0.0.1:8080", Cookie: "a b"}, "transportWs.cookie"},
		{"a cookie with =", WSSettings{Host: "p.example", Origin: "http://127.0.0.1:8080", Cookie: "a=b"}, "transportWs.cookie"},
		{"a cookie too long", WSSettings{Host: "p.example", Origin: "http://127.0.0.1:8080", Cookie: strings.Repeat("c", 33)}, "transportWs.cookie"},
	} {
		if _, err := svc.Create(ctx, wuiPlain(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), tc.ws)); FieldOf(err) != tc.field {
			t.Errorf("%s: %v, field %q", tc.what, err, FieldOf(err))
		}
	}

	// New settings keep the path, the cookie and the key; a cookie given is
	// taken.
	ws := WSSettings{Host: "plain.example.com", Origin: "http://127.0.0.1:8080", FrontPort: 8080}
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportWS: &ws}); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	db.First(&got, wg0.ID)
	if q := got.Stealth.V; q.Path != p.Path || q.Cookie != p.Cookie || q.Key != p.Key || q.FrontPort != 8080 {
		t.Fatalf("after new settings: %+v", q)
	}
	ws.Cookie = "PHPSESSID"
	svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportWS: &ws})
	db.First(&got, wg0.ID)
	if got.Stealth.V.Cookie != "PHPSESSID" {
		t.Fatalf("the cookie given: %q", got.Stealth.V.Cookie)
	}

	// drift in its place draws its own key and path, and keeps no cookie:
	// nothing bare showed in the clear goes on to a covered transport.
	wsName := model.TransportDrift
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{Transport: &wsName, TransportWS: &WSSettings{Host: "cdn.example.com"}}); err != nil {
		t.Fatal(err)
	}
	db.First(&got, wg0.ID)
	if q := got.Stealth.V; q.Transport != model.TransportDrift || q.Key == p.Key || q.Path == p.Path || q.Cookie != "" || q.PreviousKey != "" {
		t.Fatalf("drift after bare: %+v", q)
	}

	// Back to bare, with drift's path sent along as a form that kept
	// it would: the path is not carried over.
	plainName, wsPath := model.TransportBare, got.Stealth.V.Path
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{Transport: &plainName, TransportWS: &WSSettings{Host: "plain.example.com", Path: wsPath, Origin: "http://127.0.0.1:8080"}}); err != nil {
		t.Fatal(err)
	}
	db.First(&got, wg0.ID)
	if q := got.Stealth.V; q.Transport != model.TransportBare || q.Path == wsPath || q.Path == "" || q.Cookie == "" {
		t.Fatalf("bare after drift, its path sent along: %+v", q)
	}
}

// A bare front's name and path are read in the clear, so neither is a
// covered transport's, on any tunnel, whichever came first.
func TestWuiPlainNeverSharesAFrontOrAPathWithWuiWS(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 4)
	ws, err := svc.Create(ctx, wuiWS(wgInput("wg0", ports[0], "10.66.0.0/24"), freeTCPPort(t), WSSettings{Host: "front.example.com", Path: "/media/7f3a9c2e1b"}))
	if err != nil {
		t.Fatal(err)
	}
	decoy := "http://127.0.0.1:8080"
	for _, tc := range []struct {
		what  string
		ws    WSSettings
		field string
	}{
		{"drift's front", WSSettings{Host: "FRONT.example.com", Origin: decoy}, "transportWs.host"},
		{"drift's path", WSSettings{Host: "plain.example.com", Path: "/media/7f3a9c2e1b", Origin: decoy}, "transportWs.path"},
	} {
		if _, err := svc.Create(ctx, wuiPlain(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), tc.ws)); FieldOf(err) != tc.field {
			t.Errorf("%s: %v, field %q", tc.what, err, FieldOf(err))
		}
	}
	if _, err := svc.Create(ctx, wuiPlain(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), WSSettings{Host: "plain.example.com", Path: "/static/a8d0c1e9f4", Origin: decoy})); err != nil {
		t.Fatal(err)
	}
	// And the other way: a covered transport made after it.
	if _, err := svc.Create(ctx, wuiWS(wgInput("wg2", ports[2], "10.68.0.0/24"), freeTCPPort(t), WSSettings{Host: "plain.example.com"})); FieldOf(err) != "transportWs.host" {
		t.Errorf("drift on bare's front: %v", err)
	}
	if _, err := svc.Update(ctx, ws.ID, UpdateInterfaceInput{TransportWS: &WSSettings{Host: "front.example.com", Path: "/static/a8d0c1e9f4"}}); FieldOf(err) != "transportWs.path" {
		t.Errorf("drift moved to bare's path: %v", err)
	}
	// Two bare transports may share a front, on paths of their own.
	if _, err := svc.Create(ctx, wuiPlain(wgInput("wg3", ports[3], "10.69.0.0/24"), freeTCPPort(t), WSSettings{Host: "plain.example.com", Origin: decoy})); err != nil {
		t.Errorf("a second bare on the first one's front: %v", err)
	}
}

// bare's key turns over every plainKeyDays, sooner than drift's.
func TestAWuiPlainKeyTurnsOverInAWeek(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	seed(t, db, 1)
	old, _ := newWSKey()
	start := time.Unix(1_700_000_000, 0)
	p := model.StealthParams{Transport: model.TransportBare, Port: 8081, Host: "p.example", Path: "/abcdefghijklmn", Origin: "http://127.0.0.1:8080", Cookie: "sid", Key: old, KeyAt: start.Unix()}
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(p))
	read := func() model.StealthParams {
		var i model.Interface
		db.First(&i, 1)
		return i.Stealth.V
	}
	EnsureStealthKeys(ctx, db, start.Add((plainKeyDays*24-1)*time.Hour))
	if read().Key != old {
		t.Fatal("a key turned over before its time")
	}
	EnsureStealthKeys(ctx, db, start.Add(plainKeyDays*24*time.Hour))
	if q := read(); q.Key == old || q.PreviousKey != old || q.Cookie != "sid" {
		t.Fatalf("after a week: %+v", q)
	}
}

func TestTheWuiProfileCarriesWuiPlain(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 1)
	key, _ := stealth.NewTransportKey()
	p := model.StealthParams{Transport: model.TransportBare, Port: 8081, Host: "plain.example.com", Path: "/static/a8d0c1e9f4",
		Origin: "http://127.0.0.1:8080", Cookie: "sess_2b", Key: key.String(), KeyAt: 1}
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(p))
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
	var prof WUIProfile
	if err := json.Unmarshal(b.Body, &prof); err != nil || len(prof.Tunnels) != 1 || len(prof.Tunnels[0].Transports) != 1 {
		t.Fatalf("profile: %+v %v", prof, err)
	}
	tr := prof.Tunnels[0].Transports[0]
	if tr.Type != stealth.Bare || tr.URL != "ws://plain.example.com/static/a8d0c1e9f4" || tr.Key != key.Public() || tr.Cookie != "sess_2b" ||
		strings.Join(tr.Endpoints, " ") != "plain.example.com:80" || tr.Secret == "" {
		t.Fatalf("transport: %+v", tr)
	}
	if strings.Contains(string(b.Body), key.String()) {
		t.Fatal("the profile carries the transport's private key")
	}
	p.FrontPort = 8080
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(p))
	b, _ = s.Serve(ctx, token, "wui")
	json.Unmarshal(b.Body, &prof)
	if tr := prof.Tunnels[0].Transports[0]; tr.URL != "ws://plain.example.com:8080/static/a8d0c1e9f4" || tr.Endpoints[0] != "plain.example.com:8080" {
		t.Fatalf("on 8080: %+v", tr)
	}
}

func TestADrawnCookieIsANameOfItsOwn(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c, err := newPlainCookie()
		if err != nil {
			t.Fatal(err)
		}
		if !stealth.CookieName(c) || len(c) < 8 || len(c) > 16 || c[0] < 'a' || c[0] > 'z' || seen[c] {
			t.Fatalf("drawn: %q", c)
		}
		seen[c] = true
	}
}
