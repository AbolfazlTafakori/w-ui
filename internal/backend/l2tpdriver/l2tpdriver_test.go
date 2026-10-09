package l2tpdriver

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

func setup(t *testing.T) (*model.Interface, l2tpconf.Paths) {
	t.Helper()
	DataRoot = t.TempDir()
	ensureDeviceFn = func(context.Context, *model.Interface) error { return nil }
	removeDeviceFn = func(context.Context, *model.Interface) error { return nil }
	t.Cleanup(func() { ensureDeviceFn, removeDeviceFn = ensureDevice, removeDevice })
	iface := &model.Interface{
		ID: 7, Name: "l2tp0", Protocol: model.ProtocolL2TP, Enabled: true,
		ListenPort: l2tpconf.PortL2TP, Subnet: "10.77.0.0/24", EndpointHost: "vpn.example.com",
		MTU: 1400, DNS: "1.1.1.1, 9.9.9.9, 8.8.8.8",
		L2TP: model.JSON(model.L2TPParams{PSK: "SharedKey123"}),
	}
	return iface, l2tpconf.NewPaths(DataRoot)
}

func agentReports(t *testing.T, p l2tpconf.Paths, r l2tpconf.Report) {
	t.Helper()
	raw, _ := json.Marshal(r)
	if err := os.MkdirAll(p.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Report(), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readStateFile(t *testing.T, p l2tpconf.Paths) *l2tpconf.State {
	t.Helper()
	raw, err := os.ReadFile(p.State())
	if err != nil {
		t.Fatal(err)
	}
	s, err := l2tpconf.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func desired(id uint, user, secret, ip string) backend.DesiredAccount {
	return backend.DesiredAccount{ID: id, Username: user, Secret: secret, IP: netip.MustParseAddr(ip)}
}

// A fresh tunnel: the state is written for the agent to find, and the tunnel
// is not running until the agent says it is.
func TestOpenWritesTheStateAndWaitsForTheAgent(t *testing.T) {
	iface, p := setup(t)
	d := New()
	if err := d.Open(context.Background(), iface); err == nil || !errors.Is(err, ErrNoAgent) {
		t.Fatalf("open before the agent answered: %v", err)
	}
	s := readStateFile(t, p)
	if s.Interface.Gateway != "10.77.0.1" || s.Interface.PSK != "SharedKey123" ||
		strings.Join(s.Interface.DNS, ",") != "1.1.1.1,9.9.9.9" || len(s.Interface.Accounts) != 0 {
		t.Fatalf("state = %+v", s.Interface)
	}
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentRunning})
	if err := New().Open(context.Background(), iface); err != nil {
		t.Fatalf("open with the agent running: %v", err)
	}
}

// Open runs on every panel start, before the first Sync. It must keep the
// accounts already described: written with nobody in it, the state would
// have the agent end every customer's session.
func TestOpenKeepsTheAccountsOnARestart(t *testing.T) {
	iface, p := setup(t)
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentRunning})
	d := New()
	if err := d.Open(context.Background(), iface); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Sync(context.Background(), []backend.DesiredAccount{
		desired(1, "s9-laptop", "Secret11", "10.77.0.2"),
		desired(2, "s9-phone", "Secret99", "10.77.0.3"),
	}); err != nil {
		t.Fatal(err)
	}
	// The panel restarts: a new driver opens the same tunnel.
	if err := New().Open(context.Background(), iface); err != nil {
		t.Fatal(err)
	}
	if n := len(readStateFile(t, p).Interface.Accounts); n != 2 {
		t.Fatalf("a restart left %d accounts in the state, want 2", n)
	}
}

func TestSyncDescribesTheAccountsAndReportsWhatChanged(t *testing.T) {
	iface, p := setup(t)
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentRunning})
	d := New()
	if err := d.Open(context.Background(), iface); err != nil {
		t.Fatal(err)
	}

	rep, err := d.Sync(context.Background(), []backend.DesiredAccount{
		desired(1, "s9-laptop", "Secret11", "10.77.0.2"),
		desired(2, "s9-phone", "bad\"quote", "10.77.0.3"), // cannot reach pppd's file as it is
		desired(3, "s9-tablet", "Secret33", "10.99.0.3"),  // not on this tunnel
		{ID: 4, PublicKey: "wireguard-only"},              // not an L2TP account at all
	})
	if err != nil {
		t.Fatal(err)
	}
	s := readStateFile(t, p)
	if len(s.Interface.Accounts) != 1 || s.Interface.Accounts[0].Username != "s9-laptop" {
		t.Fatalf("accounts = %+v", s.Interface.Accounts)
	}
	if rep.Added != 1 {
		t.Fatalf("report = %+v", rep)
	}
	rep, _ = d.Sync(context.Background(), []backend.DesiredAccount{desired(1, "s9-laptop", "Rotated1", "10.77.0.2")})
	if rep.Updated != 1 || rep.Added != 0 || rep.Removed != 0 {
		t.Fatalf("report = %+v", rep)
	}
	rep, _ = d.Sync(context.Background(), nil)
	if rep.Removed != 1 || len(readStateFile(t, p).Interface.Accounts) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestStatsComeFromTheAgentsReport(t *testing.T) {
	iface, p := setup(t)
	now := time.Now().Unix()
	agentReports(t, p, l2tpconf.Report{At: now, State: l2tpconf.AgentRunning, Sessions: []l2tpconf.Session{
		{Account: 1, Device: "ppp0", Endpoint: "203.0.113.5", Since: now - 60, RX: 10, TX: 20},
		{Account: 0, Device: "ppp1"},
	}})
	d := New()
	if err := d.Open(context.Background(), iface); err != nil {
		t.Fatal(err)
	}
	stats, err := d.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].AccountID != 1 || stats[0].RX != 10 || stats[0].Endpoint != "203.0.113.5" ||
		stats[0].LastHandshake.Unix() != now {
		t.Fatalf("stats = %+v", stats)
	}
	// A report the agent stopped writing is no report.
	agentReports(t, p, l2tpconf.Report{At: now - 3600, State: l2tpconf.AgentRunning})
	if _, err := d.Stats(context.Background()); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("stale report: %v", err)
	}
}

func TestHealthCarriesTheAgentsFailure(t *testing.T) {
	iface, p := setup(t)
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentRunning})
	d := New()
	if err := d.Open(context.Background(), iface); err != nil {
		t.Fatal(err)
	}
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentFailed, Error: "port 1701 is taken"})
	if err := d.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "port 1701 is taken") {
		t.Fatalf("health = %v", err)
	}
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentInstalling})
	if err := d.Health(context.Background()); err != nil {
		t.Fatalf("installing is not unhealthy: %v", err)
	}
}

func TestKickAndRemove(t *testing.T) {
	iface, p := setup(t)
	agentReports(t, p, l2tpconf.Report{At: time.Now().Unix(), State: l2tpconf.AgentRunning})
	d := New()
	if err := d.Open(context.Background(), iface); err != nil {
		t.Fatal(err)
	}
	if err := d.Kick(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.KickDir(), "42")); err != nil {
		t.Fatalf("no kick request: %v", err)
	}
	if err := Remove(context.Background(), iface); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.State()); !os.IsNotExist(err) {
		t.Fatalf("the state survived removal: %v", err)
	}
}

func TestProfileListsEveryServerAndTheSignIn(t *testing.T) {
	iface, _ := setup(t)
	iface.Hosts = []model.Host{
		{Address: "b.example.com", Priority: 2, Enabled: true},
		{Address: "a.example.com", Priority: 1, Enabled: true},
		{Address: "off.example.com", Priority: 0, Enabled: false},
		{Address: "vpn.example.com", Priority: 3, Enabled: true},
	}
	got := Profile(&model.Account{Username: "s9-laptop", Secret: "Secret11"}, iface)
	for _, want := range []string{"Server:          vpn.example.com", "a.example.com", "b.example.com", "SharedKey123", "s9-laptop", "Secret11"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "off.example.com") || strings.Count(got, "vpn.example.com") != 1 {
		t.Errorf("a disabled or repeated host was listed:\n%s", got)
	}
	if strings.Index(got, "a.example.com") > strings.Index(got, "b.example.com") {
		t.Errorf("hosts are not in their order:\n%s", got)
	}
}
