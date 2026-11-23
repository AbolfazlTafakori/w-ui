package fleet

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A tunnel and a file the panel sends switched off arrive switched off on
// the node, the first time too. Created through Save, they came up on --
// the schema defaults enabled to true and a create leaves a false out --
// and a paused customer's file served on the node until the next sync.
func TestANodeKeepsWhatArrivesSwitchedOff(t *testing.T) {
	db := testDB(t)
	s := NewNodeSync(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state := NodeState{
		Interface: NodeInterface{OriginID: 5, Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: false,
			ListenPort: 51820, Subnet: "10.66.0.0/24", MTU: 1420, Mode: model.ModeStandard, PrivateKey: "priv", PublicKey: "pub"},
		Clients: []NodeClient{{OriginID: 9, Enabled: true, Accounts: []NodeAccount{{
			OriginID: 11, DeviceName: "phone", IP: "10.66.0.2", Enabled: false, PrivateKey: "p", PublicKey: "k"}}}},
	}
	if err := s.Apply(context.Background(), 1, state); err != nil {
		t.Fatal(err)
	}
	var iface model.Interface
	db.Where("origin_id = 5").First(&iface)
	var acc model.Account
	db.Where("origin_id = 11").First(&acc)
	if iface.ID == 0 || iface.Enabled {
		t.Fatalf("the tunnel sent off arrived as %+v", iface.Enabled)
	}
	if acc.ID == 0 || acc.Enabled {
		t.Fatalf("the file sent off arrived switched on (enabled %v)", acc.Enabled)
	}
}
