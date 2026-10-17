package l2tpdriver

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/devguide"
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
// L2TP client imports, so it is what they type in, and where, on every system,
// router and modem the tunnel serves -- the same catalogue the customer's page
// shows one device of at a time.
func Profile(acc *model.Account, iface *model.Interface) string {
	return devguide.PlainText(GuideValues(acc, iface))
}

// WithheldProfile is Profile without the sign-in, for what the subscription
// link hands to whoever fetches it rather than to the customer's page.
func WithheldProfile(acc *model.Account, iface *model.Interface) string {
	return devguide.WithheldText(GuideValues(acc, iface))
}

// GuideValues are a customer's own values for the device guides: the
// tunnel's addresses and key, and this device's sign-in.
func GuideValues(acc *model.Account, iface *model.Interface) devguide.Values {
	v := devguide.Values{
		Protocol:   string(iface.Protocol),
		Tunnel:     iface.Name,
		PSK:        iface.L2TP.V.PSK,
		Username:   acc.Username,
		Password:   acc.Secret,
		AllowPlain: iface.L2TP.V.AllowPlain,
	}
	if std := standardPort(iface.Protocol); iface.ListenPort != 0 && iface.ListenPort != std {
		v.OwnPort = iface.ListenPort
	}
	servers := Servers(iface)
	if len(servers) > 0 {
		v.Server = servers[0]
		v.Others = servers[1:]
		// Numbers for the routers whose field takes no name: Omada's, which
		// has IPsec and is listed on every tunnel, as well as the others.
		if ips := ServerIPv4(v.Server); len(ips) > 0 {
			v.ServerIP = ips[0]
		}
	}
	return v
}

// ServerIPv4 is a host name's IPv4 addresses, for the routers whose
// server field takes no names -- several TP-Link models. Nothing for an
// address already, or for a name that does not resolve in time. Remembered
// for a while: the sheet is drawn on every view of a customer's page.
func ServerIPv4(host string) []string {
	if host == "" {
		return nil
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	ipCache.mu.Lock()
	c, ok := ipCache.by[host]
	ipCache.mu.Unlock()
	// A name that did not resolve is asked again soon: a page drawn while
	// DNS was down should not go without the numbers for ten minutes.
	keep := ipCacheFor
	if len(c.ips) == 0 {
		keep = ipRetryAfter
	}
	if ok && time.Since(c.at) < keep {
		return c.ips
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := lookupIPv4(ctx, host)
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

// lookupIPv4 asks DNS; a test of this package asks something else.
var lookupIPv4 = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
}

const (
	ipCacheFor   = 10 * time.Minute
	ipRetryAfter = time.Minute
)

type ipEntry struct {
	ips []string
	at  time.Time
}

var ipCache = struct {
	mu sync.Mutex
	by map[string]ipEntry
}{by: map[string]ipEntry{}}

// standardPort is the port the protocol's daemon holds.
func standardPort(p model.Protocol) int {
	if p == model.ProtocolPPTP {
		return l2tpconf.PortPPTP
	}
	return l2tpconf.PortL2TP
}
