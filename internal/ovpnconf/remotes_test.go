package ovpnconf

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func ovpnIface(hosts ...model.Host) *model.Interface {
	return &model.Interface{
		Name: "ovpn0", Protocol: model.ProtocolOpenVPN,
		ListenPort: 443, EndpointHost: "vpn.example.com", Subnet: "10.9.0.0/24",
		Hosts: hosts,
		OpenVPN: model.JSON(model.OpenVPNParams{
			Transport: "tcp", CipherSuite: "AES-256-GCM", Auth: "SHA256",
			CACert: "-----BEGIN CERTIFICATE-----\nAA\n-----END CERTIFICATE-----",
		}),
	}
}

func remoteLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "remote ") {
			out = append(out, l)
		}
	}
	return out
}

// Commercial sellers hand out one profile per address and tell people to try
// another when one stops working. OpenVPN walks a list of remotes by itself, so
// the same thing fits in one file and the customer needs no instructions — and
// no new file — when an address is blocked.
func TestEveryEnabledHostBecomesARemote(t *testing.T) {
	got := RenderClient(&model.Account{Username: "ali", IP: "10.9.0.2"}, ovpnIface(
		model.Host{Address: "de.example.com", Priority: 2, Enabled: true},
		model.Host{Address: "uk.example.com", Priority: 1, Enabled: true},
		model.Host{Address: "blocked.example.com", Priority: 0, Enabled: false},
	))

	lines := remoteLines(got)
	want := []string{
		// The operator's own endpoint first: it is the one they filled in when
		// they built the tunnel and the one that is certainly right.
		"remote vpn.example.com 443",
		// Then the spares, in the order the operator put them in.
		"remote uk.example.com 443",
		"remote de.example.com 443",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d remote lines, want %d:\n%v", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("remote %d = %q, want %q", i, lines[i], want[i])
		}
	}
	if strings.Contains(got, "blocked.example.com") {
		t.Error("a host switched off was still handed to the customer")
	}
}

// A host reached through a forwarder answers on a different port from the one
// the interface listens on.
func TestHostPortOverridesTheInterfacePort(t *testing.T) {
	got := RenderClient(&model.Account{Username: "ali", IP: "10.9.0.2"}, ovpnIface(
		model.Host{Address: "cdn.example.com", Port: 8443, Enabled: true},
		model.Host{Address: "plain.example.com", Port: 0, Enabled: true},
	))
	lines := remoteLines(got)
	if len(lines) != 3 {
		t.Fatalf("got %v", lines)
	}
	if lines[1] != "remote cdn.example.com 8443" {
		t.Errorf("a host's own port was ignored: %q", lines[1])
	}
	// Port 0 means "the same as the interface", not port zero.
	if lines[2] != "remote plain.example.com 443" {
		t.Errorf("a host with no port should inherit the interface's: %q", lines[2])
	}
}

// A spare that repeats the endpoint would make the client dial the same dead
// address twice before moving on, which is exactly the delay the list exists
// to avoid.
func TestDuplicateRemotesAreDropped(t *testing.T) {
	got := RenderClient(&model.Account{Username: "ali", IP: "10.9.0.2"}, ovpnIface(
		model.Host{Address: "vpn.example.com", Port: 443, Enabled: true},
		model.Host{Address: "  vpn.example.com  ", Enabled: true},
		model.Host{Address: "", Enabled: true},
	))
	if lines := remoteLines(got); len(lines) != 1 {
		t.Errorf("got %v, want the endpoint once and nothing else", lines)
	}
}

// An interface with no spares must keep behaving exactly as it did.
func TestNoHostsIsStillOneRemote(t *testing.T) {
	got := RenderClient(&model.Account{Username: "ali", IP: "10.9.0.2"}, ovpnIface())
	if lines := remoteLines(got); len(lines) != 1 || lines[0] != "remote vpn.example.com 443" {
		t.Errorf("got %v, want exactly the interface's own endpoint", lines)
	}
}
