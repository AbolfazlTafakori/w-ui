// Package ovpnconf renders every OpenVPN artefact the panel produces: the
// customer's .ovpn file, the server configuration, the per-account address
// assignments, and the credential file its authentication script checks.
//
// One package renders both sides so they cannot drift. A client file that
// disagrees with the server about the cipher, the transport or the port fails
// with an error that names none of those things, and the only way to find it is
// to compare the two by eye.
package ovpnconf

import (
	"fmt"
	"net/netip"
	"path"
	"sort"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Account is one credential as the server should hold it.
type Account struct {
	Username string
	Secret   string
	IP       string
}

// Layout is where an interface's files live on the server.
//
// The credential file and the address directory are written on every sync; the
// server configuration is written once and only rewritten when the interface
// itself changes, because rewriting it is what forces a restart.
type Layout struct {
	Dir string // e.g. /var/lib/wui/openvpn/vpn0
}

func NewLayout(root, ifaceName string) Layout {
	return Layout{Dir: path.Join(root, "openvpn", ifaceName)}
}

func (l Layout) ServerConf() string  { return path.Join(l.Dir, "server.conf") }
func (l Layout) CACert() string      { return path.Join(l.Dir, "ca.crt") }
func (l Layout) ServerCert() string  { return path.Join(l.Dir, "server.crt") }
func (l Layout) ServerKey() string   { return path.Join(l.Dir, "server.key") }
func (l Layout) TLSCryptKey() string { return path.Join(l.Dir, "tls-crypt.key") }
func (l Layout) Credentials() string { return path.Join(l.Dir, "credentials") }
func (l Layout) AuthScript() string  { return path.Join(l.Dir, "authenticate") }
func (l Layout) ClientDir() string   { return path.Join(l.Dir, "ccd") }
func (l Layout) Management() string  { return path.Join(l.Dir, "management.sock") }
func (l Layout) PIDFile() string     { return path.Join(l.Dir, "openvpn.pid") }
func (l Layout) LogFile() string     { return path.Join(l.Dir, "openvpn.log") }
func (l Layout) StatusFile() string  { return path.Join(l.Dir, "status") }

// Network splits a CIDR subnet into the network address and dotted netmask that
// OpenVPN's `server` directive takes. It does not accept CIDR notation.
func Network(subnet string) (network, netmask string, err error) {
	p, err := netip.ParsePrefix(subnet)
	if err != nil {
		return "", "", fmt.Errorf("ovpnconf: subnet %q: %w", subnet, err)
	}
	if !p.Addr().Is4() {
		return "", "", fmt.Errorf("ovpnconf: subnet %q is not IPv4", subnet)
	}

	p = p.Masked()
	bits := p.Bits()
	// OpenVPN needs at least a /30 to have a usable pool, and rejects a mask
	// wider than the address space it hands out.
	if bits > 30 {
		return "", "", fmt.Errorf("ovpnconf: subnet %q is too small for a tunnel", subnet)
	}

	var mask [4]byte
	for i := 0; i < 4; i++ {
		remaining := bits - i*8
		switch {
		case remaining >= 8:
			mask[i] = 0xff
		case remaining > 0:
			mask[i] = ^byte(0) << (8 - remaining)
		}
	}
	return p.Addr().String(), fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3]), nil
}

// RenderClient produces the .ovpn file handed to a customer.
// RenderClient produces the profile for one account.
//
// The account is not read, and that is the point rather than an oversight: an
// OpenVPN profile carries no per-customer material at all. The certificate
// authority, the addresses and the cipher are the tunnel's; who the customer is
// gets settled at connect time by the username and password they type. Two
// customers on the same tunnel receive byte-identical files.
//
// It stays a distinct function because the driver contract renders per account,
// and because a WireGuard profile genuinely is per device.
func RenderClient(_ *model.Account, iface *model.Interface) string {
	return RenderProfile(iface)
}

// RenderProfile produces the one profile every customer on this tunnel uses.
//
// Handing out a file per customer, when the file cannot differ, is what makes
// an operator think revoking somebody means reissuing files. It does not: the
// file is the tunnel's, the credentials are the customer, and taking a customer
// away is deleting their credentials while everyone else's file keeps working.
func RenderProfile(iface *model.Interface) string {
	p := iface.OpenVPN.V
	var b strings.Builder

	b.WriteString("client\n")
	b.WriteString("dev tun\n")
	fmt.Fprintf(&b, "proto %s\n", transport(p))
	// One line per address the customer may dial, in the operator's order.
	//
	// OpenVPN walks this list and moves to the next when one will not connect,
	// which is the whole point of keeping spares: when an address is blocked,
	// the customer's own client finds the next one without being handed a new
	// file. Commercial sellers do this by shipping nine separate profiles and
	// telling people to try another; one profile with nine remotes is the same
	// thing without the instructions.
	for _, r := range remotes(iface) {
		fmt.Fprintf(&b, "remote %s %d\n", r.address, r.port)
	}
	b.WriteString("resolv-retry infinite\n")
	b.WriteString("nobind\n")
	b.WriteString("persist-key\n")
	b.WriteString("persist-tun\n")

	// The client has no certificate of its own: it proves who it is with a
	// username and password instead. Without this line the client offers an
	// empty certificate and the server rejects the connection.
	b.WriteString("auth-user-pass\n")

	// Checks that the server's certificate was issued for a server. Dropping
	// this would let anyone holding any certificate from this authority — that
	// is, any other customer, if per-client certificates were ever added —
	// impersonate the server.
	b.WriteString("remote-cert-tls server\n")

	if p.CipherSuite != "" {
		fmt.Fprintf(&b, "data-ciphers %s\n", p.CipherSuite)
		// Named separately for OpenVPN 2.4 clients, which do not know
		// data-ciphers and would otherwise negotiate nothing.
		fmt.Fprintf(&b, "cipher %s\n", p.CipherSuite)
	}
	if p.Auth != "" {
		fmt.Fprintf(&b, "auth %s\n", p.Auth)
	}
	if iface.MTU > 0 {
		fmt.Fprintf(&b, "tun-mtu %d\n", iface.MTU)
	}
	b.WriteString("verb 3\n")

	if p.CACert != "" {
		fmt.Fprintf(&b, "\n<ca>\n%s\n</ca>\n", strings.TrimSpace(p.CACert))
	}
	if p.TLSCryptKey != "" {
		fmt.Fprintf(&b, "\n<tls-crypt>\n%s\n</tls-crypt>\n", strings.TrimSpace(p.TLSCryptKey))
	}

	return b.String()
}

// RenderServer produces the server configuration.
//
// It references the key material by path rather than inlining it, because this
// file is rewritten whenever the interface changes and inlining would rewrite
// the private key with it.
func RenderServer(iface *model.Interface, l Layout) (string, error) {
	network, netmask, err := Network(iface.Subnet)
	if err != nil {
		return "", err
	}
	p := iface.OpenVPN.V

	var b strings.Builder
	b.WriteString("# Generated by W-UI. Edits are overwritten.\n")
	fmt.Fprintf(&b, "port %d\n", iface.ListenPort)
	fmt.Fprintf(&b, "proto %s\n", transport(p))
	b.WriteString("dev tun\n")

	// One address per client out of a flat subnet, rather than the legacy
	// scheme that burns a /30 on every client. It is also what makes a client's
	// address predictable enough to attach a quota to.
	b.WriteString("topology subnet\n")
	fmt.Fprintf(&b, "server %s %s\n", network, netmask)

	fmt.Fprintf(&b, "ca %s\n", l.CACert())
	fmt.Fprintf(&b, "cert %s\n", l.ServerCert())
	fmt.Fprintf(&b, "key %s\n", l.ServerKey())
	fmt.Fprintf(&b, "tls-crypt %s\n", l.TLSCryptKey())

	// No Diffie-Hellman parameter file: this is a TLS 1.3 / ECDHE deployment,
	// where the `dh` directive is obsolete and only slows startup.
	b.WriteString("dh none\n")
	b.WriteString("tls-version-min 1.2\n")

	if p.CipherSuite != "" {
		fmt.Fprintf(&b, "data-ciphers %s\n", p.CipherSuite)
		fmt.Fprintf(&b, "cipher %s\n", p.CipherSuite)
	}
	if p.Auth != "" {
		fmt.Fprintf(&b, "auth %s\n", p.Auth)
	}

	// Customers authenticate with a username and password only. There are no
	// per-client certificates to issue, revoke or lose, which is the whole
	// reason this panel can create an account instantly.
	b.WriteString("\nverify-client-cert none\n")
	b.WriteString("username-as-common-name\n")
	fmt.Fprintf(&b, "auth-user-pass-verify %s via-file\n", l.AuthScript())
	// via-file needs level 2. Level 3 would additionally pass the password in
	// the environment, where any process on the machine could read it.
	b.WriteString("script-security 2\n")

	// Addresses are assigned from here, one file per username, so that a
	// customer keeps the same address across reconnects and the quota attached
	// to that address keeps counting.
	fmt.Fprintf(&b, "client-config-dir %s\n", l.ClientDir())

	if p.DuplicateCN {
		b.WriteString("duplicate-cn\n")
	} else {
		// Left off deliberately: a second login on the same credential evicts
		// the first. That is what stops one account being shared between two
		// people, and it costs nothing to enforce because the protocol does it.
		b.WriteString("# duplicate-cn is off: a second login evicts the first\n")
	}

	b.WriteString("\nkeepalive 10 60\n")
	b.WriteString("persist-key\npersist-tun\n")
	if iface.MTU > 0 {
		fmt.Fprintf(&b, "tun-mtu %d\n", iface.MTU)
	}

	b.WriteString("\npush \"redirect-gateway def1 bypass-dhcp\"\n")
	for _, dns := range dnsServers(iface.DNS) {
		fmt.Fprintf(&b, "push \"dhcp-option DNS %s\"\n", dns)
	}

	fmt.Fprintf(&b, "\nmanagement %s unix\n", l.Management())
	fmt.Fprintf(&b, "status %s 10\n", l.StatusFile())
	b.WriteString("status-version 2\n")
	fmt.Fprintf(&b, "log-append %s\n", l.LogFile())
	b.WriteString("verb 3\n")

	return b.String(), nil
}

// RenderServerHuman is the copyable summary shown in the panel.
//
// It names the files rather than containing them. The private key and the
// tls-crypt key are both server secrets, and this text is displayed in a browser
// and pasted into chat windows.
func RenderServerHuman(iface *model.Interface, l Layout) (string, error) {
	conf, err := RenderServer(iface, l)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Server configuration for " + iface.Name + "\n")
	b.WriteString("# Key material lives beside this file and is not shown here.\n\n")
	b.WriteString(conf)

	if nat := strings.TrimSpace(iface.NATInterface); nat != "" {
		network, netmask, err := Network(iface.Subnet)
		if err != nil {
			return "", err
		}
		b.WriteString("\n# Customers reach the internet through NAT. Without these the tunnel\n")
		b.WriteString("# comes up and reaches nothing.\n")
		fmt.Fprintf(&b, "#   nft add rule ip nat postrouting ip saddr %s/%s oifname %q masquerade\n",
			network, netmask, nat)
		fmt.Fprintf(&b, "#   sysctl -w net.ipv4.ip_forward=1\n")
	}
	return b.String(), nil
}

// RenderCredentials produces the file the authentication script checks.
//
// One `username:secret` per line. The script matches a whole line with a fixed
// string, so neither field is ever interpreted as a pattern.
func RenderCredentials(accounts []Account) string {
	lines := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if a.Username == "" || a.Secret == "" {
			continue
		}
		lines = append(lines, a.Username+":"+a.Secret)
	}
	// Sorted so an unchanged account set produces an identical file and the
	// driver can tell "nothing changed" from a byte comparison.
	sort.Strings(lines)

	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// RenderAuthScript produces the script OpenVPN runs to check a login.
//
// OpenVPN writes the username and password to a temporary file and passes its
// path as the only argument. Exit 0 accepts the login, anything else rejects it.
func RenderAuthScript(l Layout) string {
	// Both fields are restricted to an alphabet the panel itself generates, and
	// anything outside it is rejected before the value reaches grep. That is
	// what makes it safe to build a search string out of a value that arrived
	// from the network.
	return `#!/bin/sh
# Generated by W-UI. Edits are overwritten.
#
# $1 is a temporary file: line 1 is the username, line 2 is the password.
set -u

credentials='` + l.Credentials() + `'

[ -f "$1" ] || exit 1
[ -f "$credentials" ] || exit 1

username=$(sed -n 1p -- "$1")
password=$(sed -n 2p -- "$1")

[ -n "$username" ] && [ -n "$password" ] || exit 1

# The panel only ever issues credentials from this alphabet. Rejecting anything
# else means a login attempt can never carry a newline, a colon or a shell
# metacharacter into the comparison below.
case "$username" in *[!A-Za-z0-9_-]*) exit 1 ;; esac
case "$password" in *[!A-Za-z0-9]*) exit 1 ;; esac

grep -qxF -- "$username:$password" "$credentials"
`
}

// RenderClientConfig produces the client-config-dir entry that pins an account
// to its address.
func RenderClientConfig(acc Account, subnet string) (string, error) {
	_, netmask, err := Network(subnet)
	if err != nil {
		return "", err
	}
	if _, err := netip.ParseAddr(acc.IP); err != nil {
		return "", fmt.Errorf("ovpnconf: account %q address %q: %w", acc.Username, acc.IP, err)
	}
	// Without this the server hands out an address from a pool and the customer
	// gets a different one on every reconnect, which would detach them from the
	// quota counting their traffic.
	return fmt.Sprintf("ifconfig-push %s %s\n", acc.IP, netmask), nil
}

// remote is one address a customer's client may dial.
type remote struct {
	address string
	port    int
}

// remotes lists them in the order the client should try.
//
// The interface's own endpoint comes first, because it is the one the operator
// filled in when they built the tunnel and the one that is certainly correct.
// Hosts follow in their configured priority. Duplicates are dropped: a host
// added that happens to repeat the endpoint would otherwise make the client
// try the same dead address twice before moving on.
func remotes(iface *model.Interface) []remote {
	out := make([]remote, 0, len(iface.Hosts)+1)
	seen := map[remote]bool{}

	add := func(addr string, port int) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}
		if port <= 0 {
			port = iface.ListenPort
		}
		r := remote{address: addr, port: port}
		if seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}

	add(iface.EndpointHost, iface.ListenPort)

	hosts := append([]model.Host(nil), iface.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].Priority < hosts[j].Priority })
	for _, h := range hosts {
		if !h.Enabled {
			continue
		}
		add(h.Address, h.Port)
	}
	return out
}

func transport(p model.OpenVPNParams) string {
	if p.Transport == "tcp" {
		return "tcp"
	}
	return "udp"
}

// dnsServers splits the interface's DNS field, which holds a comma-separated
// list, into individual addresses.
func dnsServers(field string) []string {
	var out []string
	for _, part := range strings.Split(field, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}
