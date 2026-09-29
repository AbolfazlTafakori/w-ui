package service

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The machine running the tests has networks of its own -- a Docker bridge, a
// runner's 10.x network -- and whether a test's subnet happens to overlap one
// must not decide whether it passes. Every test starts on a machine with none;
// the ones about them say which.
func init() {
	hostNetworks = func() ([]hostNet, error) { return nil, nil }
}

func onHost(t *testing.T, nets ...hostNet) {
	t.Helper()
	saved := hostNetworks
	hostNetworks = func() ([]hostNet, error) { return nets, nil }
	t.Cleanup(func() { hostNetworks = saved })
}

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// A tunnel on a range another program already uses on this server -- Docker's
// bridge, another VPN -- or the server's own network would take that traffic
// into the tunnel. It is refused, naming the device.
func TestATunnelIsRefusedARangeAlreadyOnThisServer(t *testing.T) {
	svc := newInterfaces(t, testDB(t))
	onHost(t,
		hostNet{Device: "eth0", Prefix: pfx("203.0.113.5/24")},
		hostNet{Device: "docker0", Prefix: pfx("172.17.0.1/16")},
		hostNet{Device: "wg-other", Prefix: pfx("10.8.0.1/24")},
	)

	for _, c := range []struct{ subnet, device string }{
		{"172.17.5.0/24", "docker0"},
		{"10.8.0.0/16", "wg-other"},
		{"203.0.113.0/24", "eth0"},
	} {
		_, err := svc.Create(t.Context(), CreateInterfaceInput{
			Name: "wg9", Protocol: model.ProtocolWireGuard, ListenPort: 51899, Subnet: c.subnet,
			EndpointHost: "vpn.example.com", NATInterface: "eth0",
		})
		if err == nil {
			t.Errorf("%s was accepted over %s", c.subnet, c.device)
			continue
		}
		if !strings.Contains(err.Error(), c.device) {
			t.Errorf("%s: the refusal does not name %s: %v", c.subnet, c.device, err)
		}
	}

	// A range nothing on the machine uses is fine.
	if _, err := svc.Create(t.Context(), CreateInterfaceInput{
		Name: "wg9", Protocol: model.ProtocolWireGuard, ListenPort: 51899, Subnet: "10.99.0.0/16",
		EndpointHost: "vpn.example.com", NATInterface: "eth0",
	}); err != nil {
		t.Fatalf("a free range was refused: %v", err)
	}
}

// The panel's own tunnels are on the machine too, once they are up. Their
// ranges are checked against each other already, and editing a tunnel is not
// refused because its own device carries its own range.
func TestATunnelsOwnDeviceIsNotAnotherProgram(t *testing.T) {
	svc := newInterfaces(t, testDB(t))
	created, err := svc.Create(t.Context(), CreateInterfaceInput{
		Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.66.0.0/16",
		EndpointHost: "vpn.example.com", NATInterface: "eth0",
	})
	if err != nil {
		t.Fatal(err)
	}
	onHost(t, hostNet{Device: "wg0", Prefix: pfx("10.66.0.1/16")})

	wider := "10.66.0.0/15"
	if _, err := svc.Update(t.Context(), created.ID, UpdateInterfaceInput{Subnet: &wider}); err != nil &&
		strings.Contains(err.Error(), "already on this server") {
		t.Fatalf("editing a tunnel was refused over its own device: %v", err)
	}
}
