// Package wgconf renders WireGuard configuration.
//
// Both the API (handing a file to a customer) and the driver (writing the
// server's own config) generate WireGuard text. Keeping one renderer means the
// two cannot drift: a change to how peers are written would otherwise land in
// the customer's file and not on the server, and the tunnel would stop working
// for reasons nothing in the panel would explain.
package wgconf

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// DefaultKeepalive is the interval clients send keepalives at.
//
// 25 seconds is the conventional value: long enough to be cheap on battery and
// data, short enough to hold open a NAT mapping on networks that expire them at
// thirty.
const DefaultKeepalive = 25

// Peer is one device as the server needs to know it.
type Peer struct {
	PublicKey    string
	PresharedKey string
	AllowedIP    string // a single /32 inside the tunnel subnet
}

// Gateway returns the address the server itself holds inside a subnet.
func Gateway(subnet string) (netip.Addr, int, error) {
	p, err := netip.ParsePrefix(subnet)
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("wgconf: subnet %q: %w", subnet, err)
	}
	return p.Masked().Addr().Next(), p.Bits(), nil
}

// amneziaLines renders the obfuscation profile shared by server and client.
//
// These values must be byte-identical on both ends. Rendering them from one
// function is what guarantees that; two copies would eventually disagree and
// every handshake would fail with nothing in the logs to say why.
func amneziaLines(p model.AWGParams) []string {
	lines := []string{
		fmt.Sprintf("Jc = %d", p.Jc),
		fmt.Sprintf("Jmin = %d", p.Jmin),
		fmt.Sprintf("Jmax = %d", p.Jmax),
		fmt.Sprintf("S1 = %d", p.S1),
		fmt.Sprintf("S2 = %d", p.S2),
		fmt.Sprintf("S3 = %d", p.S3),
		fmt.Sprintf("S4 = %d", p.S4),
		fmt.Sprintf("H1 = %d", p.H1),
		fmt.Sprintf("H2 = %d", p.H2),
		fmt.Sprintf("H3 = %d", p.H3),
		fmt.Sprintf("H4 = %d", p.H4),
	}
	if p.HeaderProtectionKey != "" {
		lines = append(lines, "HeaderProtectionKey = "+p.HeaderProtectionKey)
	}
	return lines
}

// RenderClient produces the file a customer imports.
func RenderClient(acc *model.Account, iface *model.Interface) string {
	var b strings.Builder

	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", acc.PrivateKey)
	// A /32 is deliberate: the address identifies this device only. A wider
	// mask would make the client claim the whole tunnel subnet and blackhole
	// every other customer's traffic on that machine.
	fmt.Fprintf(&b, "Address = %s/32\n", acc.IP)
	if iface.DNS != "" {
		fmt.Fprintf(&b, "DNS = %s\n", iface.DNS)
	}
	if iface.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", iface.MTU)
	}

	if iface.Mode == model.ModeAmnezia {
		b.WriteString("\n# AmneziaWG obfuscation — must match the server exactly\n")
		for _, l := range amneziaLines(iface.AWG.V) {
			b.WriteString(l + "\n")
		}
	}

	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", iface.PublicKey)
	if acc.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", acc.PresharedKey)
	}
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	fmt.Fprintf(&b, "Endpoint = %s:%d\n", iface.EndpointHost, iface.ListenPort)
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", DefaultKeepalive)

	return b.String()
}

// RenderServer produces what `awg setconf` and `awg syncconf` accept.
//
// This is the device's own configuration and nothing else. Address, MTU, DNS
// and the PostUp rules belong to wg-quick, which reads them itself and then
// calls this same tool without them. Passing them here is answered with "Line
// unrecognized" and the whole file is rejected, leaving a tunnel that exists
// and carries nothing. The copyable wg-quick file an operator sees in the panel
// is RenderServerHuman: a different format for a different reader.
//
// Standard WireGuard interfaces are configured over netlink and never need
// this. It is the only way to reach an AmneziaWG one, whose obfuscation
// parameters netlink knows nothing about.
func RenderServer(iface *model.Interface, peers []Peer) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "ListenPort = %d\n", iface.ListenPort)
	fmt.Fprintf(&b, "PrivateKey = %s\n", iface.PrivateKey)

	if iface.Mode == model.ModeAmnezia {
		b.WriteString("\n# AmneziaWG obfuscation — must match every client exactly\n")
		for _, l := range amneziaLines(iface.AWG.V) {
			b.WriteString(l + "\n")
		}
	}

	for _, p := range peers {
		b.WriteString("\n[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", p.PublicKey)
		if p.PresharedKey != "" {
			fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
		}
		// On the server side AllowedIPs is a filter, not a route: it is the set
		// of addresses this peer may send from. A wide mask here would let one
		// customer spoof another's address and have their traffic billed to it.
		fmt.Fprintf(&b, "AllowedIPs = %s\n", p.AllowedIP)
	}

	return b.String()
}

// RenderServerHuman is the copyable version shown in the panel, with the key
// withheld and the NAT rules an operator would otherwise have to remember.
func RenderServerHuman(iface *model.Interface, natIface string) (string, error) {
	gw, bits, err := Gateway(iface.Subnet)
	if err != nil {
		return "", err
	}
	if natIface == "" {
		natIface = "eth0"
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "Address = %s/%d\n", gw, bits)
	fmt.Fprintf(&b, "ListenPort = %d\n", iface.ListenPort)
	b.WriteString("PrivateKey = <held in the panel database>\n")
	if iface.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", iface.MTU)
	}
	if iface.Mode == model.ModeAmnezia {
		b.WriteString("\n# AmneziaWG obfuscation — must match every client exactly\n")
		for _, l := range amneziaLines(iface.AWG.V) {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("\nPostUp = sysctl -w net.ipv4.ip_forward=1\n")
	fmt.Fprintf(&b, "PostUp = iptables -t nat -A POSTROUTING -s %s -o %s -j MASQUERADE\n",
		iface.Subnet, natIface)
	fmt.Fprintf(&b, "PostUp = iptables -A FORWARD -i %s -j ACCEPT\n", iface.Name)
	fmt.Fprintf(&b, "PostDown = iptables -t nat -D POSTROUTING -s %s -o %s -j MASQUERADE\n",
		iface.Subnet, natIface)
	fmt.Fprintf(&b, "PostDown = iptables -D FORWARD -i %s -j ACCEPT\n", iface.Name)
	b.WriteString("\n# The panel writes one [Peer] block per device.\n")

	return b.String(), nil
}
