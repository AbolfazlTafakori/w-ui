package reconciler

import (
	"io"
	"log/slog"
	"net"
	"testing"

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
