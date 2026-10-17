package agentkit

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Servers is every address a customer can dial for a tunnel: its own
// first, then its hosts in their order, each once. These tunnels have no
// port to choose -- the built-in clients use their protocol's -- so a host
// is only its address.
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

// ServerIPv4 is a host name's IPv4 addresses, for the routers whose server
// field takes no names -- several TP-Link models. Nothing for an address
// already, or for a name that does not resolve in time. Remembered for a
// while: the sheet is drawn on every view of a customer's page.
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

// lookupIPv4 asks DNS; this package's tests ask something else.
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
