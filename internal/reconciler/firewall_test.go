package reconciler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fwsync"
)

// The ports the tunnels need are written where the firewall helper reads
// them, in the data directory the reconciler is given; without one, nothing
// is written.
func TestTheTunnelsPortsReachTheFirewallHelper(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{DB: newTestDB(t), Enforcer: newFakeEnforcer(), Pool: backend.NewPool(quietLog()),
		Interval: time.Second, Log: quietLog(), DataDir: dir})
	ifaces := []model.Interface{{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Enabled: true}}
	r.keepFirewall(ifaces)
	b, err := os.ReadFile(filepath.Join(dir, fwsync.DesiredFile))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := fwsync.Decode(b)
	if err != nil || len(rules) == 0 || rules[0].Port != 51820 {
		t.Fatalf("written %v, %v", rules, err)
	}

	none := New(Options{DB: newTestDB(t), Enforcer: newFakeEnforcer(), Pool: backend.NewPool(quietLog()),
		Interval: time.Second, Log: quietLog()})
	none.keepFirewall(ifaces) // nowhere to write: does nothing, and does not fail
}
