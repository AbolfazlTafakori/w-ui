package fleet

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A node is given a tunnel's veil and every device's transport secret,
// and loses veil when the panel turns it off.
func TestANodeTakesWuiUDPAsThePanelHasIt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := NewNodeSync(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state := NodeState{
		Interface: NodeInterface{OriginID: 5, Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true,
			ListenPort: 51820, Subnet: "10.66.0.0/24", MTU: 1420, Mode: model.ModeStandard,
			PrivateKey: "priv", PublicKey: "pub", Stealth: &model.StealthParams{Transport: model.TransportVeil, Port: 2096}},
		Clients: []NodeClient{{OriginID: 9, Enabled: true, Accounts: []NodeAccount{{
			OriginID: 11, DeviceName: "phone", IP: "10.66.0.2", Enabled: true,
			PrivateKey: "p", PublicKey: "k", StealthSecret: "secret",
		}}}},
	}
	if err := s.Apply(ctx, 1, state); err != nil {
		t.Fatal(err)
	}
	var iface model.Interface
	var acc model.Account
	db.Where("origin_id = 5").First(&iface)
	db.Where("origin_id = 11").First(&acc)
	if iface.StealthUDP() != 2096 || acc.StealthSecret != "secret" {
		t.Fatalf("on the node: port %d, secret %q", iface.StealthUDP(), acc.StealthSecret)
	}
	state.Interface.Stealth = nil
	if err := s.Apply(ctx, 1, state); err != nil {
		t.Fatal(err)
	}
	db.Where("origin_id = 5").First(&iface)
	if iface.StealthUDP() != 0 {
		t.Fatal("veil stayed on after the panel turned it off")
	}
}
