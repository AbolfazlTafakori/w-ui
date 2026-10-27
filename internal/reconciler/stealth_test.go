package reconciler

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
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
	if first == nil || first.gw == nil || first.gw.Stats().Devices != 1 {
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
