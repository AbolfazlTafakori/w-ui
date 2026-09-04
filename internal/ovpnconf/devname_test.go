package ovpnconf

import (
	"strings"
	"testing"
)

// The server's device must be named after the interface.
//
// `dev tun` gets an auto-numbered tun0, and everything in the panel keyed on
// the interface name then refers to a device that is not there. That is how
// every speed limit on an OpenVPN tunnel came to do nothing while the shaper
// logged a failure every two seconds: it looked for ovpn443 and the kernel had
// called it tun0.
func TestServerDeviceIsNamedAfterTheInterface(t *testing.T) {
	iface := ovpnIface()
	iface.Name = "ovpn443"

	got, err := RenderServer(iface, NewLayout("/var/lib/wui", iface.Name))
	if err != nil {
		t.Fatalf("RenderServer: %v", err)
	}

	if !strings.Contains(got, "\ndev ovpn443\n") {
		t.Errorf("the server config does not name its device after the interface:\n%s", got)
	}
	// dev-type has to come with it: a device whose name is not tun/tap leaves
	// OpenVPN unable to guess which kind it is, and it refuses to start.
	if !strings.Contains(got, "\ndev-type tun\n") {
		t.Errorf("a named device needs dev-type; without it OpenVPN cannot start:\n%s", got)
	}
	if strings.Contains(got, "\ndev tun\n") {
		t.Errorf("the server config still asks for an auto-numbered device:\n%s", got)
	}
}

// The customer's side is the opposite case and must stay generic. What the
// device is called on their phone is theirs, and ours would collide with
// whatever else they have connected.
func TestClientDeviceStaysUnnamed(t *testing.T) {
	iface := ovpnIface()
	iface.Name = "ovpn443"

	got := RenderProfile(iface)
	if !strings.Contains(got, "\ndev tun\n") {
		t.Errorf("the customer's profile should ask for any tun device:\n%s", got)
	}
	if strings.Contains(got, "dev ovpn443") {
		t.Error("the customer's profile names our server's device")
	}
}
