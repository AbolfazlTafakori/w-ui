package ikev2driver

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ikev2conf"
)

// tunnel is an IKEv2 tunnel with its authority and server certificate, as
// the panel makes them; byCert signs devices in by certificate.
func tunnel(t *testing.T, byCert bool) *model.Interface {
	t.Helper()
	ca, err := ikev2conf.NewAuthority("ikev2-0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := ca.IssueServer([]string{"vpn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	iface := &model.Interface{ID: 5, Name: "ikev2-0", Protocol: model.ProtocolIKEv2, ListenPort: 500,
		Subnet: "10.81.0.0/24", EndpointHost: "vpn.example.com"}
	iface.IKEv2.V = model.IKEv2Params{MSCHAPv2: !byCert, EAPTLS: byCert,
		CACert: ca.Cert, CAKey: ca.Key, ServerCert: srv.Cert, ServerKey: srv.Key, ServerNames: []string{"vpn.example.com"}}
	return iface
}

func opened(t *testing.T, iface *model.Interface) *Driver {
	t.Helper()
	d := New()
	d.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.iface = iface
	d.paths = ikev2conf.NewPaths(t.TempDir())
	if err := os.MkdirAll(d.paths.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return d
}

func described(t *testing.T, d *Driver) []ikev2conf.Account {
	t.Helper()
	st, err := readState(d.paths)
	if err != nil {
		t.Fatal(err)
	}
	return st.Interface.Accounts
}

var ip = netip.MustParseAddr

// What the subscription link hands out carries no sign-in.
func TestTheLinkCarriesNoSignIn(t *testing.T) {
	iface := tunnel(t, false)
	acc := &model.Account{Username: "s1-ali", Secret: "Pa55word-ali"}
	if p := Profile(acc, iface); !strings.Contains(p, "Pa55word-ali") {
		t.Fatalf("the page's profile lacks the password:\n%s", p)
	}
	if p := WithheldProfile(acc, iface); strings.Contains(p, "Pa55word-ali") || strings.Contains(p, "s1-ali") {
		t.Fatalf("the link's profile carries the sign-in:\n%s", p)
	}
}

// Every device the agent is told of signs in as itself and no other, and
// none is named as the server is: a device by the server's own name could
// stand in for it.
func TestOnlyDevicesThatCanSignInSafelyAreDescribed(t *testing.T) {
	d := opened(t, tunnel(t, false))
	report, err := d.Sync(context.Background(), []backend.DesiredAccount{
		{ID: 1, Username: "s1-ali", Secret: "Pa55word-ali", IP: ip("10.81.0.2")},
		{ID: 2, Username: "s1-ali", Secret: "another-pass", IP: ip("10.81.0.3")},          // a username taken
		{ID: 3, Username: "vpn.example.com", Secret: "Pa55word-srv", IP: ip("10.81.0.4")}, // the server's own name
		{ID: 4, Username: "s1-sara", Secret: `quo"te-pass`, IP: ip("10.81.0.5")},          // cannot be written
		{ID: 5, Username: "s1-reza", Secret: "Pa55word-reza", IP: ip("10.66.0.2")},        // outside the tunnel
		{ID: 6, Username: "s1-gw", Secret: "Pa55word-gw", IP: ip("10.81.0.1")},            // the gateway's
		{ID: 7, Username: "s1-mina", Secret: "Pa55word-mina", IP: ip("10.81.0.7")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := described(t, d)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 7 || report.Added != 2 {
		t.Fatalf("described %+v, report %+v", got, report)
	}
	report, err = d.Sync(context.Background(), []backend.DesiredAccount{
		{ID: 7, Username: "s1-mina", Secret: "New-pass-mina", IP: ip("10.81.0.7")},
	})
	if err != nil || report.Updated != 1 || report.Removed != 1 {
		t.Fatalf("report %+v, %v", report, err)
	}
}

// On a tunnel that signs in by certificate, a device with none yet is left
// out rather than let in by its password alone.
func TestACertificateTunnelDescribesOnlyDevicesWithOne(t *testing.T) {
	iface := tunnel(t, true)
	d := opened(t, iface)
	dev, err := ikev2conf.Authority{Cert: iface.IKEv2.V.CACert, Key: iface.IKEv2.V.CAKey}.IssueDevice("s1-ali")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Sync(context.Background(), []backend.DesiredAccount{
		{ID: 1, Username: "s1-ali", Secret: "Pa55word-ali", IP: ip("10.81.0.2"), Cert: dev.Cert},
		{ID: 2, Username: "s1-sara", Secret: "Pa55word-sara", IP: ip("10.81.0.3")},
	}); err != nil {
		t.Fatal(err)
	}
	if got := described(t, d); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("described %+v", got)
	}
	// And a device's bundle is there only once it has a certificate, and
	// opens only with its own password.
	if _, ok, _ := CertBundle(&model.Account{Username: "s1-sara", Secret: "Pa55word-sara"}, iface); ok {
		t.Fatal("a bundle for a device with no certificate")
	}
	b, ok, err := CertBundle(&model.Account{Username: "s1-ali", Secret: "Pa55word-ali", CertPEM: dev.Cert, CertKeyPEM: dev.Key}, iface)
	if err != nil || !ok || len(b) == 0 {
		t.Fatalf("no bundle for a device with a certificate: %v", err)
	}
}

func TestAnUnopenedDriverRefusesToSync(t *testing.T) {
	if _, err := New().Sync(context.Background(), nil); err != backend.ErrNotOpen {
		t.Fatalf("%v", err)
	}
}
