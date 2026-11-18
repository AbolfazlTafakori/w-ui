package fleet

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A node holds a customer to what they have left in its own kernel: its
// quota for them is what it has counted of them and what the panel says is
// left. Its own count -- what it reports upward -- is never touched, and a
// plan with no allowance has none here either.
func TestANodeHoldsACustomerToWhatIsLeft(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := NewNodeSync(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	left := uint64(500)
	state := NodeState{
		Interface: NodeInterface{OriginID: 5, Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true,
			ListenPort: 51820, Subnet: "10.66.0.0/24", MTU: 1420, Mode: model.ModeStandard, PrivateKey: "priv", PublicKey: "pub"},
		Clients: []NodeClient{{OriginID: 9, Enabled: true, AllowanceBytes: &left, Accounts: []NodeAccount{{
			OriginID: 11, DeviceName: "phone", IP: "10.66.0.2", Enabled: true, PrivateKey: "p", PublicKey: "k"}}}},
	}
	read := func() model.Client {
		t.Helper()
		var c model.Client
		db.Where("origin_id = 9").First(&c)
		return c
	}
	if err := s.Apply(ctx, 1, state); err != nil {
		t.Fatal(err)
	}
	if c := read(); c.QuotaBytes != 500 || c.UsedBytes != 0 {
		t.Fatalf("first push: quota %d used %d, want 500 and 0", c.QuotaBytes, c.UsedBytes)
	}
	// It used 300 here; the panel, having counted it, says 200 are left.
	db.Model(&model.Client{}).Where("origin_id = 9").Update("used_bytes", 300)
	left = 200
	if err := s.Apply(ctx, 1, state); err != nil {
		t.Fatal(err)
	}
	if c := read(); c.QuotaBytes != 500 || c.UsedBytes != 300 {
		t.Fatalf("second push: quota %d used %d, want 500 and its own 300", c.QuotaBytes, c.UsedBytes)
	}
	// Nothing left is a limit.
	left = 0
	db.Model(&model.Client{}).Where("origin_id = 9").Update("used_bytes", 0)
	_ = s.Apply(ctx, 1, state)
	if c := read(); c.QuotaBytes == 0 {
		t.Fatal("nothing left became no limit at all")
	}
	// No allowance: none here.
	state.Clients[0].AllowanceBytes = nil
	_ = s.Apply(ctx, 1, state)
	if c := read(); c.QuotaBytes != 0 {
		t.Fatalf("no allowance pushed, quota %d here", c.QuotaBytes)
	}
}
