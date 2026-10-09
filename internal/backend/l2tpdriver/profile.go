package l2tpdriver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
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
`)
	if iface.L2TP.V.AllowPlain {
		b.WriteString(`
Routers without IPsec -- this server accepts L2TP without it too. The
connection is then not encrypted: use it only where nothing else will do.
ASUS:     VPN > VPN Fusion (or VPN Client) > Add profile > L2TP; server,
          username, password.
OpenWrt:  Network > Interfaces > Add new interface > Protocol: L2TP
          (packages xl2tpd and ppp-mod-pppol2tp); server, username, password.
MikroTik: as above with use-ipsec=no.
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
