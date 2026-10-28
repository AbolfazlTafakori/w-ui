package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/occonf"
	"github.com/abolfazl/w-ui/internal/stealth"
)

func wuiTLS(in CreateInterfaceInput, port int, ws WSSettings) CreateInterfaceInput {
	in.Transport, in.TransportPort, in.TransportWS = model.TransportWuiTLS, port, &ws
	return in
}

// pinOf is the pin of a wui-tls transport's own certificate.
func pinOf(t *testing.T, p model.StealthParams) stealth.Pin {
	t.Helper()
	_, info, err := stealth.ReadCertificate(p.Cert)
	if err != nil {
		t.Fatal(err)
	}
	return info.Pin
}

// wui-tls is the server itself: no front, a decoy that must be an http://
// address, a path and a key the panel draws, and a certificate of the
// tunnel's own whose key is drawn once.
func TestAWireGuardTunnelTakesWuiTLSAsTheServer(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	tcp := freeTCPPort(t)

	wg0, err := svc.Create(ctx, wuiTLS(wgInput("wg0", ports[0], "10.66.0.0/24"), tcp, WSSettings{
		Host: "Edge.Example.com.", Origin: "http://127.0.0.1:8080/",
		// What a front needs is no wui-tls's, and is not kept.
		FrontPort: 8443, Proxies: "10.0.0.0/8", ClientHeader: "X-Real-IP",
	}))
	if err != nil {
		t.Fatal(err)
	}
	p := wg0.Stealth.V
	if wg0.WireGuardTransport() != model.TransportWuiTLS || wg0.StealthTLS() != tcp || wg0.StealthWS() != 0 || p.Host != "edge.example.com" || p.Origin != "http://127.0.0.1:8080" {
		t.Fatalf("made as %s on %d: %+v", wg0.WireGuardTransport(), wg0.StealthTLS(), p)
	}
	if p.FrontPort != 0 || p.Proxies != "" || p.ClientHeader != "" || p.Cookie != "" {
		t.Fatalf("a front's settings were kept: %+v", p)
	}
	if len(p.Path) != 1+wsPathLen || p.Path[0] != '/' {
		t.Fatalf("the path drawn is %q", p.Path)
	}
	if _, err := stealth.ParseTransportKey(p.Key); err != nil || p.KeyAt == 0 {
		t.Fatalf("the transport's key: %v", err)
	}
	if !wg0.StealthOpen() {
		t.Fatal("wui-tls is the server, and its port is not opened")
	}
	_, info, err := stealth.ReadCertificate(p.Cert)
	if err != nil || p.CertSource != model.CertSourceOwn || info.Name != "edge.example.com" {
		t.Fatalf("the certificate: %v, %q, named %q", err, p.CertSource, info.Name)
	}
	if keyPin, err := stealth.CertificateKeyPin(p.CertKey); err != nil || keyPin != info.Pin {
		t.Fatal("the certificate is not of its key")
	}
	if _, err := stealth.TLSCertificate(p.ServedCert()); err != nil {
		t.Fatalf("the certificate does not serve: %v", err)
	}

	for _, tc := range []struct {
		what  string
		ws    WSSettings
		field string
	}{
		{"no host", WSSettings{Origin: "http://127.0.0.1:8080"}, "transportWs.host"},
		{"no decoy", WSSettings{Host: "e.example"}, "transportWs.origin"},
		{"an https decoy", WSSettings{Host: "e.example", Origin: "https://127.0.0.1:8443"}, "transportWs.origin"},
		{"a decoy with a path", WSSettings{Host: "e.example", Origin: "http://127.0.0.1:8080/site"}, "transportWs.origin"},
		{"a short path", WSSettings{Host: "e.example", Origin: "http://127.0.0.1:8080", Path: "/short"}, "transportWs.path"},
	} {
		if _, err := svc.Create(ctx, wuiTLS(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), tc.ws)); FieldOf(err) != tc.field {
			t.Errorf("%s: %v, field %q", tc.what, err, FieldOf(err))
		}
	}

	// A new name gives a certificate of that name, with the same key: the
	// pin every profile carries holds.
	ws := WSSettings{Host: "203.0.113.5", Origin: "http://127.0.0.1:8080", ProxyProtocol: true}
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportWS: &ws}); err != nil {
		t.Fatal(err)
	}
	ws = WSSettings{Host: "other.example.com", Origin: "http://127.0.0.1:8080", ProxyProtocol: true}
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{TransportWS: &ws}); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	db.First(&got, wg0.ID)
	q := got.Stealth.V
	_, info2, _ := stealth.ReadCertificate(q.Cert)
	if q.CertKey != p.CertKey || info2.Pin != info.Pin || info2.Name != "other.example.com" || !q.ProxyProtocol || q.Path != p.Path || q.Key != p.Key {
		t.Fatalf("after a new name: %+v, named %q", q, info2.Name)
	}

	// wui-ws in its place starts with nothing of wui-tls's.
	wsT := model.TransportWuiWS
	front := WSSettings{Host: "front.example.com"}
	if _, err := svc.Update(ctx, wg0.ID, UpdateInterfaceInput{Transport: &wsT, TransportWS: &front}); err != nil {
		t.Fatal(err)
	}
	db.First(&got, wg0.ID)
	if r := got.Stealth.V; r.Cert != "" || r.CertKey != "" || r.CertSource != "" || r.ProxyProtocol || r.Key == p.Key || r.Path == p.Path {
		t.Fatalf("wui-ws after wui-tls: %+v", r)
	}
}

// The certificate is made again before it runs out, with the same key; a
// tunnel another panel manages keeps the one it came with.
func TestAWuiTLSCertificateIsMadeAgainWithTheSameKey(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	seed(t, db, 1)
	start := time.Now()
	p := model.StealthParams{Transport: model.TransportWuiTLS, Port: 8443, Host: "edge.example.com", Path: "/abcdefghijklmn", Origin: "http://127.0.0.1:8080"}
	if _, err := tlsCertFor(&p, PanelCert{}, start); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(p))
	managed := model.Interface{Name: "far", Protocol: model.ProtocolWireGuard, ListenPort: 51900, Subnet: "10.90.0.0/24", Managed: true, Stealth: model.JSON(p)}
	if err := db.Create(&managed).Error; err != nil {
		t.Fatal(err)
	}
	read := func(id uint) model.StealthParams {
		var i model.Interface
		db.First(&i, id)
		return i.Stealth.V
	}

	if err := EnsureStealthCerts(ctx, db, 0, PanelCert{}, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if read(1).Cert != p.Cert {
		t.Fatal("a certificate in date was made again")
	}
	_, info, _ := stealth.ReadCertificate(p.Cert)
	due := info.NotAfter.Add(-stealth.CertificateLead)
	if err := EnsureStealthCerts(ctx, db, 0, PanelCert{}, due); err != nil {
		t.Fatal(err)
	}
	q := read(1)
	_, renewed, _ := stealth.ReadCertificate(q.Cert)
	if q.Cert == p.Cert || q.CertKey != p.CertKey || renewed.Pin != info.Pin || !renewed.NotAfter.After(info.NotAfter) {
		t.Fatalf("at its lead: renewed %v, pin kept %v", q.Cert != p.Cert, renewed.Pin == info.Pin)
	}
	if read(managed.ID).Cert != p.Cert {
		t.Fatal("a tunnel another panel manages had its certificate made here")
	}

	// A key that does not read is never replaced: it is the pinned one.
	broken := p
	broken.Cert, broken.CertKey = "", "not a key"
	if _, err := tlsCertFor(&broken, PanelCert{}, start); err == nil || broken.CertKey != "not a key" {
		t.Fatalf("a key that does not read: %v, now %q", err, broken.CertKey)
	}
}

// The panel's own certificate is served when asked for and in date for the
// name, checked by devices as a browser checks it; when it stops covering
// the name, the tunnel's own is served again, with the pin it always had.
func TestAWuiTLSTunnelTakesThePanelsCertificate(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	dir := t.TempDir()
	pc, pk, err := occonf.NewServerCert([]string{"edge.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(cf, []byte(pc), 0o600)
	os.WriteFile(kf, []byte(pk), 0o600)
	panel := PanelCert{CertFile: cf, KeyFile: kf}
	svc.SetPanelCert(panel)

	ports := freeUDPPorts(t, 1)
	wg0, err := svc.Create(ctx, wuiTLS(wgInput("wg0", ports[0], "10.66.0.0/24"), freeTCPPort(t), WSSettings{
		Host: "edge.example.com", Origin: "http://127.0.0.1:8080", UsePanelCert: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	p := wg0.Stealth.V
	if p.CertSource != model.CertSourcePanel || p.PanelCert != pc || p.PanelCertKey != pk || p.CertKey == "" {
		t.Fatalf("the panel's certificate was not taken: %q", p.CertSource)
	}
	if cert, key := p.ServedCert(); cert != pc || key != pk {
		t.Fatal("the panel's certificate is not the one served")
	}
	tr, ok := wsTransport(p)
	if !ok || tr.SelfSigned || len(tr.Pins) != 0 {
		t.Fatalf("a profile for the panel's certificate: %+v", tr)
	}

	// The panel's certificate gone, the pass serves the tunnel's own.
	var local model.Node
	db.Where("kind = ?", model.KindLocal).First(&local)
	if err := EnsureStealthCerts(ctx, db, local.ID, PanelCert{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var got model.Interface
	db.First(&got, wg0.ID)
	q := got.Stealth.V
	if q.CertSource != model.CertSourceOwn || q.PanelCert != "" || q.PanelCertKey != "" || q.CertKey != p.CertKey || !q.UsePanelCert {
		t.Fatalf("with the panel's certificate gone: %+v", q.CertSource)
	}
	if tr, ok := wsTransport(q); !ok || !tr.SelfSigned || len(tr.Pins) != 1 || tr.Pins[0] != pinOf(t, q).String() {
		t.Fatalf("a profile for the tunnel's own: %+v", tr)
	}
	// And back, on a pass that finds it again.
	if err := EnsureStealthCerts(ctx, db, local.ID, panel, time.Now()); err != nil {
		t.Fatal(err)
	}
	db.First(&got, wg0.ID)
	if got.Stealth.V.CertSource != model.CertSourcePanel {
		t.Fatal("the panel's certificate was not taken up again")
	}

	// A tunnel on another server never takes it: its key would go there.
	if _, err := svc.checkStealth(ctx, local.ID, 0, model.ModeStandard, 51820, model.StealthParams{
		Transport: model.TransportWuiTLS, Port: 8443, Host: "edge.example.com", Origin: "http://127.0.0.1:8080", UsePanelCert: true,
	}, 0, false); FieldOf(err) != "transportWs.usePanelCert" {
		t.Fatalf("the panel's certificate for another server: %v", err)
	}
}

func TestTheWuiProfileCarriesWuiTLS(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c := seed(t, db, 1)
	key, _ := stealth.NewTransportKey()
	p := model.StealthParams{Transport: model.TransportWuiTLS, Port: 8443, Host: "edge.example.com", Path: "/media/7f3a9c2e1b", Origin: "http://127.0.0.1:8080", Key: key.String(), KeyAt: 1}
	if _, err := tlsCertFor(&p, PanelCert{}, time.Now()); err != nil {
		t.Fatal(err)
	}
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
	var acc model.Account
	db.Where("client_id = ?", c.ID).First(&acc)
	if tr.Type != stealth.TLS || tr.URL != "wss://edge.example.com:8443/media/7f3a9c2e1b" || tr.Key != key.Public() ||
		strings.Join(tr.Endpoints, " ") != "edge.example.com:8443" || tr.Secret != acc.StealthSecret ||
		!tr.SelfSigned || len(tr.Pins) != 1 || tr.Pins[0] != pinOf(t, p).String() {
		t.Fatalf("transport: %+v", tr)
	}
	for _, private := range []string{key.String(), p.CertKey} {
		if strings.Contains(string(b.Body), private) {
			t.Fatal("the profile carries a private key")
		}
	}

	// On 443 the address carries no port.
	p.Port = 443
	db.Model(&model.Interface{}).Where("id = 1").Update("stealth", model.JSON(p))
	b, _ = s.Serve(ctx, token, "wui")
	json.Unmarshal(b.Body, &prof)
	if tr := prof.Tunnels[0].Transports[0]; tr.URL != "wss://edge.example.com/media/7f3a9c2e1b" || tr.Endpoints[0] != "edge.example.com:443" {
		t.Fatalf("on 443: %+v", tr)
	}
}

// wui-plain's name and path, read in the clear, are never wui-tls's.
func TestWuiPlainStaysApartFromWuiTLS(t *testing.T) {
	db := testDB(t)
	svc := newInterfaces(t, db)
	ctx := context.Background()
	ports := freeUDPPorts(t, 2)
	wg0, err := svc.Create(ctx, wuiTLS(wgInput("wg0", ports[0], "10.66.0.0/24"), freeTCPPort(t), WSSettings{Host: "edge.example.com", Origin: "http://127.0.0.1:8080"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what  string
		ws    WSSettings
		field string
	}{
		{"its name", WSSettings{Host: "edge.example.com", Origin: "http://127.0.0.1:8080"}, "transportWs.host"},
		{"its path", WSSettings{Host: "plain.example.com", Origin: "http://127.0.0.1:8080", Path: wg0.Stealth.V.Path}, "transportWs.path"},
	} {
		if _, err := svc.Create(ctx, wuiPlain(wgInput("wg1", ports[1], "10.67.0.0/24"), freeTCPPort(t), tc.ws)); FieldOf(err) != tc.field {
			t.Errorf("wui-plain with wui-tls's %s: %v", tc.what, err)
		}
	}
}
