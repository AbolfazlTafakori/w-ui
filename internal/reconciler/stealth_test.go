package reconciler

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
	"github.com/abolfazl/w-ui/internal/stealthgw"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

func freeUDP(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Skip("no UDP port can be bound here")
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// A tunnel with wui-udp has a gateway beside it while it wants one, with
// the devices it carries, started again when what it runs with changes.
func TestTheGatewaysFollowTheTunnels(t *testing.T) {
	r := &Reconciler{dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer r.StopGateways()
	server, _ := wgkey.NewPair()
	device, _ := wgkey.NewPair()
	secret, _ := stealth.NewSecret()
	iface := model.Interface{ID: 7, Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, MTU: 1420,
		PrivateKey: server.Private.String(), Stealth: model.JSON(model.StealthParams{Transport: model.TransportWuiUDP, Port: freeUDP(t)})}
	d := &desired{interfaces: []model.Interface{iface}, perIface: map[uint][]backend.DesiredAccount{
		7: {{ID: 1, PublicKey: device.Public.String(), StealthSecret: secret.String()}, {ID: 2, PublicKey: "no secret"}},
	}}

	r.keepStealth(d)
	first := r.gws.running[7]
	if first == nil || first.gw == nil || first.gw.(*stealthgw.Gateway).Stats().Devices != 1 {
		t.Fatal("no gateway, or not with the one device that has a secret")
	}
	listening := func(port int) bool {
		c, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
		if err != nil {
			return true
		}
		c.Close()
		return false
	}
	if !listening(iface.StealthUDP()) {
		t.Fatal("nothing listens on the wui-udp port")
	}

	// Another pass changes nothing; a new port is a new gateway.
	r.keepStealth(d)
	if r.gws.running[7] != first {
		t.Fatal("a pass with nothing changed started the gateway again")
	}
	old := iface.StealthUDP()
	d.interfaces[0].Stealth = model.JSON(model.StealthParams{Transport: model.TransportWuiUDP, Port: freeUDP(t)})
	r.keepStealth(d)
	if r.gws.running[7] == first || listening(old) {
		t.Fatal("the gateway did not move to the new port")
	}

	// AmneziaWG, or a tunnel that is gone, has none.
	d.interfaces[0].Mode = model.ModeAmnezia
	r.keepStealth(d)
	if len(r.gws.running) != 0 || listening(d.interfaces[0].Stealth.V.Port) {
		t.Fatal("a gateway outlived the tunnel's wanting one")
	}
	if got := r.realEndpoint(7, "1.2.3.4:5"); got != "1.2.3.4:5" {
		t.Fatalf("an endpoint without a gateway came back as %q", got)
	}
}

func freeTCP(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no TCP port can be bound here")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// A tunnel with wui-ws has a stream gateway beside it: on the loopback alone
// while no front elsewhere is trusted, on every address once one is, and
// started again with the key when the key turns over.
func TestAWSGatewayListensWhereItsFrontsAre(t *testing.T) {
	r := &Reconciler{dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer r.StopGateways()
	server, _ := wgkey.NewPair()
	device, _ := wgkey.NewPair()
	secret, _ := stealth.NewSecret()
	key, _ := stealth.NewTransportKey()
	ws := model.StealthParams{Transport: model.TransportWuiWS, Port: freeTCP(t), Host: "front.example", Path: "/a1b2c3d4e5f6g7", Key: key.String()}
	iface := model.Interface{ID: 9, Name: "wg1", Protocol: model.ProtocolWireGuard, ListenPort: 51821, MTU: 1420,
		PrivateKey: server.Private.String(), Stealth: model.JSON(ws)}
	d := &desired{interfaces: []model.Interface{iface}, perIface: map[uint][]backend.DesiredAccount{
		9: {{ID: 1, PublicKey: device.Public.String(), StealthSecret: secret.String()}},
	}}

	r.keepStealth(d)
	first := r.gws.running[9]
	gw, ok := first.gw.(*stealthgw.StreamGateway)
	if first == nil || !ok || gw.Stats().Devices != 1 {
		t.Fatal("no stream gateway, or not with the device")
	}
	if a := gw.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("with no front elsewhere the gateway listens at %s", a)
	}

	// A front elsewhere: every address.
	ws.Proxies = "192.0.2.0/24"
	d.interfaces[0].Stealth = model.JSON(ws)
	r.keepStealth(d)
	gw = r.gws.running[9].gw.(*stealthgw.StreamGateway)
	if a := gw.Addr().(*net.TCPAddr); a.IP.IsLoopback() {
		t.Fatalf("with a front elsewhere the gateway listens at %s", a)
	}

	// The key turns over: the gateway starts again, with both.
	second := r.gws.running[9]
	next, _ := stealth.NewTransportKey()
	ws.PreviousKey, ws.Key = ws.Key, next.String()
	d.interfaces[0].Stealth = model.JSON(ws)
	r.keepStealth(d)
	if r.gws.running[9] == second || r.gws.running[9].gw == nil {
		t.Fatal("a new key did not start the gateway again")
	}
}

// A tunnel with wui-plain has a plain gateway beside it, on the loopback
// while no front elsewhere is trusted, started again when its cookie moves.
func TestAPlainGatewayRunsBesideItsTunnel(t *testing.T) {
	r := &Reconciler{dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer r.StopGateways()
	server, _ := wgkey.NewPair()
	device, _ := wgkey.NewPair()
	secret, _ := stealth.NewSecret()
	key, _ := stealth.NewTransportKey()
	p := model.StealthParams{Transport: model.TransportWuiPlain, Port: freeTCP(t), Host: "plain.example", Path: "/a1b2c3d4e5f6g7",
		Origin: "http://127.0.0.1:8080", Cookie: "sid", Key: key.String()}
	iface := model.Interface{ID: 10, Name: "wg2", Protocol: model.ProtocolWireGuard, ListenPort: 51822, MTU: 1420,
		PrivateKey: server.Private.String(), Stealth: model.JSON(p)}
	d := &desired{interfaces: []model.Interface{iface}, perIface: map[uint][]backend.DesiredAccount{
		10: {{ID: 1, PublicKey: device.Public.String(), StealthSecret: secret.String()}},
	}}

	r.keepStealth(d)
	first := r.gws.running[10]
	gw, ok := first.gw.(*stealthgw.PlainGateway)
	if !ok || gw.Stats().Devices != 1 {
		t.Fatalf("no plain gateway, or not with the device: %T", first.gw)
	}
	if a := gw.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("with no front elsewhere the gateway listens at %s", a)
	}
	if got := replayFile(r.dataDir, iface); filepath.Base(got) != "10.wui-plain.replay" {
		t.Fatalf("the replay file is %s", got)
	}

	p.Cookie = "other"
	d.interfaces[0].Stealth = model.JSON(p)
	r.keepStealth(d)
	if r.gws.running[10] == first {
		t.Fatal("a new cookie did not start the gateway again")
	}

	// Without a decoy it does not start, and says so; the tunnel goes on.
	p.Origin = ""
	d.interfaces[0].Stealth = model.JSON(p)
	r.keepStealth(d)
	if r.gws.running[10].gw != nil || r.gws.running[10].failed == "" {
		t.Fatal("a plain gateway started without a decoy")
	}
}

// A tunnel with wui-poll has a poll gateway beside it, with a replay file
// of its own, on the loopback while no front elsewhere is trusted.
func TestAPollGatewayRunsBesideItsTunnel(t *testing.T) {
	r := &Reconciler{dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer r.StopGateways()
	server, _ := wgkey.NewPair()
	device, _ := wgkey.NewPair()
	secret, _ := stealth.NewSecret()
	key, _ := stealth.NewTransportKey()
	p := model.StealthParams{Transport: model.TransportWuiPoll, Port: freeTCP(t), Host: "poll.example", Path: "/a1b2c3d4e5f6g7", Key: key.String()}
	iface := model.Interface{ID: 11, Name: "wg3", Protocol: model.ProtocolWireGuard, ListenPort: 51823, MTU: 1420,
		PrivateKey: server.Private.String(), Stealth: model.JSON(p)}
	d := &desired{interfaces: []model.Interface{iface}, perIface: map[uint][]backend.DesiredAccount{
		11: {{ID: 1, PublicKey: device.Public.String(), StealthSecret: secret.String()}},
	}}

	r.keepStealth(d)
	first := r.gws.running[11]
	gw, ok := first.gw.(*stealthgw.PollGateway)
	if !ok || gw.Stats().Devices != 1 {
		t.Fatalf("no poll gateway, or not with the device: %T", first.gw)
	}
	if a := gw.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
		t.Fatalf("with no front elsewhere the gateway listens at %s", a)
	}
	if got := replayFile(r.dataDir, iface); filepath.Base(got) != "11.wui-poll.replay" {
		t.Fatalf("the replay file is %s", got)
	}
	p.Path = "/z9y8x7w6v5u4t3"
	d.interfaces[0].Stealth = model.JSON(p)
	r.keepStealth(d)
	if r.gws.running[11] == first || r.gws.running[11].gw == nil {
		t.Fatal("a new path did not start the gateway again")
	}
}

// wui-tls's gateway is the server itself, on every address; a renewed
// certificate is handed to it as it runs, so no session ends for it.
func TestATLSGatewayTakesARenewedCertificateWithoutStopping(t *testing.T) {
	r := &Reconciler{dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer r.StopGateways()
	server, _ := wgkey.NewPair()
	key, _ := stealth.NewTransportKey()
	certKey, _ := stealth.NewCertificateKey()
	now := time.Now()
	cert, _ := stealth.MakeCertificate(certKey, "edge.example", "", now)
	p := model.StealthParams{Transport: model.TransportWuiTLS, Port: freeTCP(t), Host: "edge.example", Path: "/a1b2c3d4e5f6g7",
		Origin: "http://127.0.0.1:8080", Key: key.String(), Cert: cert, CertKey: certKey, CertSource: model.CertSourceOwn}
	iface := model.Interface{ID: 11, Name: "wg3", Protocol: model.ProtocolWireGuard, ListenPort: 51823, MTU: 1420,
		PrivateKey: server.Private.String(), Stealth: model.JSON(p)}
	d := &desired{interfaces: []model.Interface{iface}, perIface: map[uint][]backend.DesiredAccount{}}

	r.keepStealth(d)
	first := r.gws.running[11]
	gw, ok := first.gw.(*stealthgw.TLSGateway)
	if !ok {
		t.Fatalf("no TLS gateway: %T %q", first.gw, first.failed)
	}
	if a := gw.Addr().(*net.TCPAddr); a.IP.IsLoopback() {
		t.Fatalf("wui-tls listens at %s, not on every address", a)
	}
	if got := replayFile(r.dataDir, iface); filepath.Base(got) != "11.wui-tls.replay" {
		t.Fatalf("the replay file is %s", got)
	}
	served := func() *x509.Certificate {
		c, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Port)), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.ConnectionState().PeerCertificates[0]
	}
	before := served()

	// Made again with the same key: the same gateway serves it.
	renewed, _ := stealth.MakeCertificate(certKey, "edge.example", cert, now.Add(time.Hour))
	p.Cert = renewed
	d.interfaces[0].Stealth = model.JSON(p)
	r.keepStealth(d)
	if r.gws.running[11] != first {
		t.Fatal("a renewed certificate started the gateway again")
	}
	after := served()
	if bytes.Equal(after.Raw, before.Raw) || stealth.PinOf(after) != stealth.PinOf(before) {
		t.Fatal("the renewed certificate is not the one served, or its pin changed")
	}

	// The decoy's PROXY header is what it was started with: a change starts
	// it again.
	p.ProxyProtocol = true
	d.interfaces[0].Stealth = model.JSON(p)
	r.keepStealth(d)
	if r.gws.running[11] == first {
		t.Fatal("the PROXY header turned on did not start the gateway again")
	}

	// A certificate that does not read stops nothing that runs, and is said.
	running := r.gws.running[11]
	p.Cert = "not a certificate"
	d.interfaces[0].Stealth = model.JSON(p)
	r.keepStealth(d)
	if r.gws.running[11] != running || running.cert == p.Cert {
		t.Fatal("a certificate that does not read was taken")
	}
}
