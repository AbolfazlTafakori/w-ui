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
// L2TP client imports, so it is the four things they type in, and where.
func Profile(acc *model.Account, iface *model.Interface) string {
	var b strings.Builder
	servers := Servers(iface)
	fmt.Fprintf(&b, "L2TP/IPsec — %s\n\n", iface.Name)
	if len(servers) > 0 {
		fmt.Fprintf(&b, "Server:          %s\n", servers[0])
		for _, s := range servers[1:] {
			fmt.Fprintf(&b, "                 %s (if the first does not answer)\n", s)
		}
	}
	fmt.Fprintf(&b, "Pre-shared key:  %s\n", iface.L2TP.V.PSK)
	fmt.Fprintf(&b, "Username:        %s\n", acc.Username)
	fmt.Fprintf(&b, "Password:        %s\n", acc.Secret)
	b.WriteString(`
Windows:  Settings > Network & internet > VPN > Add VPN. VPN provider:
          Windows (built-in); VPN type: L2TP/IPsec with pre-shared key;
          sign-in info: User name and password.
iPhone:   Settings > VPN > Add VPN Configuration > Type: L2TP.
macOS:    System Settings > VPN > Add VPN Configuration > L2TP over IPSec.
Android:  Android 11 and earlier: Settings > VPN > Add > L2TP/IPSec PSK.
          Android 12 and later no longer offer L2TP.
`)
	return b.String()
}
