package occdriver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/occonf"
)

func tunnel() *model.Interface {
	return &model.Interface{ID: 4, Name: "oc0", Protocol: model.ProtocolOpenConnect, ListenPort: 443,
		Subnet: "10.79.0.0/24", EndpointHost: "vpn.example.com", MTU: 1400}
}

// opened is a driver as Open leaves it, its files in a directory of the
// test's own, with no agent behind it.
func opened(t *testing.T) *Driver {
	t.Helper()
	d := New()
	d.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.iface = tunnel()
	cert, key, err := occonf.NewServerCert([]string{"vpn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	d.iface.OpenConnect.V = model.OpenConnectParams{Cert: cert, Key: key}
	d.paths = occonf.NewPaths(t.TempDir())
	if err := os.MkdirAll(d.paths.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return d
}

func written(t *testing.T, d *Driver) []occonf.Account {
	t.Helper()
	st, err := readState(d.paths)
	if err != nil {
		t.Fatal(err)
	}
	return st.Interface.Accounts
}

// What the subscription link hands out carries no sign-in; the customer's
// own page carries it.
func TestTheLinkCarriesNoSignIn(t *testing.T) {
	acc := &model.Account{Username: "s1-ali", Secret: "Pa55word-ali"}
	if p := Profile(acc, tunnel()); !strings.Contains(p, "Pa55word-ali") || !strings.Contains(p, "s1-ali") {
		t.Fatalf("the page's profile lacks the sign-in:\n%s", p)
	}
	if p := WithheldProfile(acc, tunnel()); strings.Contains(p, "Pa55word-ali") || strings.Contains(p, "s1-ali") {
		t.Fatalf("the link's profile carries the sign-in:\n%s", p)
	}
}

// Every device the agent is told of can sign in as itself and no other: one
// whose sign-in or address cannot be used, or whose username another device
// already has, is left out.
func TestOnlyDevicesThatCanSignInSafelyAreDescribed(t *testing.T) {
	d := opened(t)
	ip := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	report, err := d.Sync(context.Background(), []backend.DesiredAccount{
		{ID: 1, Username: "s1-ali", Secret: "Pa55word-ali", IP: ip("10.79.0.2")},
		{ID: 2, Username: "s1-ali", Secret: "another-pass", IP: ip("10.79.0.3")},   // a username taken
		{ID: 3, Username: "s1-sara", Secret: `quo"te-pass`, IP: ip("10.79.0.4")},   // a password that cannot be written
		{ID: 4, Username: "bad name!", Secret: "Pa55word-xx", IP: ip("10.79.0.5")}, // a username that cannot
		{ID: 5, Username: "s1-reza", Secret: "Pa55word-reza", IP: ip("10.80.0.2")}, // outside the tunnel
		{ID: 6, Username: "s1-gw", Secret: "Pa55word-gw", IP: ip("10.79.0.1")},     // the gateway's
		{ID: 7, Username: "s1-none", Secret: "", IP: ip("10.79.0.7")},              // no password
		{ID: 8, Username: "s1-mina", Secret: "Pa55word-mina", IP: ip("10.79.0.8")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := written(t, d)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 8 || report.Added != 2 {
		t.Fatalf("described %+v, report %+v", got, report)
	}

	// Again with one changed and one gone: the report says so, and the file
	// is the new one.
	report, err = d.Sync(context.Background(), []backend.DesiredAccount{
		{ID: 1, Username: "s1-ali", Secret: "New-pass-ali", IP: ip("10.79.0.2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 || report.Removed != 1 || report.Added != 0 {
		t.Fatalf("report %+v", report)
	}
	if got := written(t, d); len(got) != 1 || got[0].Secret != "New-pass-ali" {
		t.Fatalf("described %+v", got)
	}
}

// A driver that was never opened describes nothing.
func TestAnUnopenedDriverRefusesToSync(t *testing.T) {
	if _, err := New().Sync(context.Background(), nil); err != backend.ErrNotOpen {
		t.Fatalf("%v", err)
	}
}

// report writes what the agent says, as it says it.
func report(t *testing.T, d *Driver, r l2tpconf.Report) {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.paths.Report(), b, 0o640); err != nil {
		t.Fatal(err)
	}
}

// Who is connected is what the agent last said, while it is still saying
// it: a session with no account is nobody's, a stale report is no agent,
// and only a session's own vpns device is ever shaped.
func TestWhoIsConnectedIsWhatTheAgentSays(t *testing.T) {
	d := opened(t)
	report(t, d, l2tpconf.Report{At: time.Now().Unix(), State: "running", Sessions: []l2tpconf.Session{
		{Account: 1, Device: "vpns0", RX: 100, TX: 900, Endpoint: "198.51.100.7:4433", Since: 1},
		{Account: 0, Device: "vpns1"},
		{Account: 2, Device: "eth0"},
	}})
	stats, err := d.Stats(context.Background())
	if err != nil || len(stats) != 2 || stats[0].AccountID != 1 || stats[0].TX != 900 {
		t.Fatalf("stats %+v, %v", stats, err)
	}
	devs, err := d.SessionDevices(context.Background())
	if err != nil || len(devs) != 1 || devs[0].Device != "vpns0" {
		t.Fatalf("shaped %+v, %v", devs, err)
	}
	report(t, d, l2tpconf.Report{At: time.Now().Add(-time.Hour).Unix(), State: "running"})
	if _, err := d.Stats(context.Background()); err == nil {
		t.Fatal("an hour-old report was taken as the agent running")
	}
}

// Ending a session is a request the agent picks up; an account of nobody is
// not one.
func TestASessionIsEndedThroughTheAgent(t *testing.T) {
	d := opened(t)
	if err := os.MkdirAll(d.paths.KickDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := d.Kick(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.paths.KickFile(7)); err != nil {
		t.Fatalf("no request for the agent: %v", err)
	}
	if err := d.Kick(context.Background(), 0); err != backend.ErrUnknownAcct {
		t.Fatalf("account 0: %v", err)
	}
	for _, name := range []string{"vpns0", "vpns12"} {
		if !vpnsDevice(name) {
			t.Errorf("%s is a session's device", name)
		}
	}
	for _, name := range []string{"vpns", "vpns-1", "eth0", "vpnsx", "wg0"} {
		if vpnsDevice(name) {
			t.Errorf("%s was taken for a session's device", name)
		}
	}
}
