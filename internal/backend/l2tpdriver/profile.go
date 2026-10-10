package l2tpdriver

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// Servers is every address a customer can dial for this tunnel: the
// interface's own first, then its hosts in their order, each once.
//
// L2TP has no port to choose -- the clients use 500, 4500 and 1701 whatever
// they are told -- so a host is only its address.
func Servers(iface *model.Interface) []string {
	var out []string
	seen := map[string]bool{}
	add := func(a string) {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	add(iface.EndpointHost)
	hosts := append([]model.Host(nil), iface.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].Priority < hosts[j].Priority })
	for _, h := range hosts {
		if h.Enabled {
			add(h.Address)
		}
	}
	return out
}

// Profile is what a customer is handed for one device: there is no file an
// L2TP client imports, so it is the four things they type in, and where, for
// every system and router that has an L2TP client of its own.
//
// What each is told comes from what each does: Windows needs a registry value
// before it will talk to a server through NAT (error 809); iPhone and macOS
// route only the tunnel's own network unless "send all traffic" is on;
// Android dropped L2TP in 12; MikroTik, Keenetic and TP-Link Omada routers
// have L2TP with IPsec, ASUS and OpenWrt only without -- so those two are
// listed only on a tunnel that accepts it.
func Profile(acc *model.Account, iface *model.Interface) string {
	var b strings.Builder
	servers := Servers(iface)
	psk := iface.L2TP.V.PSK
	fmt.Fprintf(&b, "L2TP/IPsec — %s\n\n", iface.Name)
	if len(servers) > 0 {
		fmt.Fprintf(&b, "Server:          %s\n", servers[0])
		for _, s := range servers[1:] {
			fmt.Fprintf(&b, "                 %s (if the first does not answer)\n", s)
		}
	}
	fmt.Fprintf(&b, "Pre-shared key:  %s\n", psk)
	fmt.Fprintf(&b, "Username:        %s\n", acc.Username)
	fmt.Fprintf(&b, "Password:        %s\n", acc.Secret)
	server := "SERVER"
	if len(servers) > 0 {
		server = servers[0]
	}
	b.WriteString(`
Windows:  Settings > Network & internet > VPN > Add VPN. VPN provider:
          Windows (built-in); VPN type: L2TP/IPsec with pre-shared key;
          sign-in info: User name and password.
          Error 809: run once as administrator, then restart:
          reg add HKLM\SYSTEM\CurrentControlSet\Services\PolicyAgent /v AssumeUDPEncapsulationContextOnSendRule /t REG_DWORD /d 2 /f
iPhone:   Settings > General > VPN & Device Management > VPN > Add VPN
          Configuration > Type: L2TP. Keep Send All Traffic on.
macOS:    System Settings > VPN > Add VPN Configuration > L2TP over IPSec.
          Turn on Send all traffic over VPN connection.
Android:  Android 11 and earlier: Settings > VPN > Add > L2TP/IPSec PSK.
          Android 12 and later no longer offer L2TP.
Linux:    NetworkManager with the L2TP plugin: gateway, username, password;
          IPsec settings: enable IPsec, pre-shared key.
MikroTik: /interface l2tp-client add name=wui-l2tp connect-to=`)
	fmt.Fprintf(&b, "%s user=%s password=%s use-ipsec=yes ipsec-secret=%s allow=mschap2 disabled=no\n",
		rosQuote(server), rosQuote(acc.Username), rosQuote(acc.Secret), rosQuote(psk))
	b.WriteString(`Keenetic: Other connections > VPN connections > Create connection;
          Type: L2TP/IPsec; server, username, password; Secret key: the
          pre-shared key.
Omada:    VPN > VPN Client > L2TP; IPsec Encryption: Encrypted; the
          pre-shared key, username and password.
TP-Link Archer with VPN Client (AX20, AX23, AX55, AX73 and others):
          Advanced > VPN Client > Add > L2TP/IPSec; server, username,
          password, IPsec pre-shared key.
`)
	if iface.L2TP.V.AllowPlain {
		b.WriteString(`
Routers and modems without IPsec -- this server accepts L2TP without it
too. The connection is then not encrypted: use it only where nothing else
will do. One router carries the whole home on one connection.
`)
		if l2tpconf.TunnelSecretUsable(psk) {
			b.WriteString("Where a modem asks for a tunnel password or L2TP key, it is the pre-shared key.\n")
		}
		if ips := serverIPv4(server); len(ips) > 0 {
			fmt.Fprintf(&b, "Server IP, for a router that takes no name: %s\n", strings.Join(ips, " or "))
		}
		b.WriteString(`Irancell TF-i60 (G1, B1, H1), GP-2101: Settings > VPN > L2TP; server,
          username, password; Tunnel Password: the pre-shared key.
Huawei 4G/5G (B311, B316, B525, B535, B612 and others): Advanced >
          Router > VPN > L2TP VPN client; LNS address: the server;
          Hostname: anything; Tunnel password: the pre-shared key; PPP user
          name and password.
TP-Link:  Advanced > Network > Internet (older panels: Network > WAN) >
          Internet Connection Type: L2TP (or Russia L2TP); Secondary
          connection: Dynamic IP; server, username, password. The router's
          WAN port goes to the modem, and reaches the server through it.
D-Link:   Setup > Internet > Manual Internet Connection Setup > L2TP
          (Dual Access); Address Mode: Dynamic IP; server, username, password.
Tenda:    Internet Settings > Connection Type: L2TP (or VPN > PPTP/L2TP
          Client); server, username, password.
Xiaomi:   Advanced settings > VPN > Add service > Protocol: L2TP; server,
          username, password.
ASUS:     VPN > VPN Fusion (or VPN Client) > Add profile > L2TP; server,
          username, password.
OpenWrt:  Network > Interfaces > Add new interface > Protocol: L2TP
          (packages xl2tpd and ppp-mod-pppol2tp); server, username, password.
MikroTik: as above with use-ipsec=no.
ADSL and VDSL modems (TP-Link TD-W8961N, TD-W9970, D-Link DSL-124,
DSL-2750U, Utel and most others) have no L2TP client: put one of these
routers behind the modem, or connect each device itself.
`)
	}
	b.WriteString(`
One device per address: two devices behind the same router or the same
mobile carrier address cannot both use L2TP/IPsec at once -- the second
ends the first. That is L2TP/IPsec itself, on every server.
`)
	return b.String()
}

// rosQuote is a value as RouterOS's console reads it back unchanged: quoted,
// with the one character that means something inside quotes escaped. The
// rest a password or key may hold here -- no spaces, quotes or backslashes --
// is literal there.
func rosQuote(v string) string {
	return `"` + strings.ReplaceAll(v, "$", `\$`) + `"`
}

// serverIPv4 is a host name's IPv4 addresses, for the routers whose L2TP
// server field takes no names -- several TP-Link models. Nothing for an
// address already, or for a name that does not resolve in time. Remembered
// for a while: the sheet is drawn on every view of a customer's page.
var serverIPv4 = func(host string) []string {
	if host == "" {
		return nil
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	ipCache.mu.Lock()
	c, ok := ipCache.by[host]
	ipCache.mu.Unlock()
	if ok && time.Since(c.at) < ipCacheFor {
		return c.ips
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	var ips []string
	if err == nil {
		for _, a := range addrs {
			ips = append(ips, a.Unmap().String())
		}
		sort.Strings(ips)
	}
	ipCache.mu.Lock()
	ipCache.by[host] = ipEntry{ips: ips, at: time.Now()}
	ipCache.mu.Unlock()
	return ips
}

const ipCacheFor = 10 * time.Minute

type ipEntry struct {
	ips []string
	at  time.Time
}

var ipCache = struct {
	mu sync.Mutex
	by map[string]ipEntry
}{by: map[string]ipEntry{}}
