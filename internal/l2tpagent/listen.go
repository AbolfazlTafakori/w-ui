package l2tpagent

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// listenAddr picks the address xl2tpd answers from: the one of the tunnel's
// endpoints that is an address of this server, when exactly one is. With
// none -- a cloud server whose public address is translated to a private
// one -- or with several, xl2tpd listens on every address, as before:
// binding one would cut off devices dialing the others.
//
// So it does when an endpoint is another server's: a relay in front of
// this one, such as a server inside Iran forwarding UDP 500 and 4500 here.
// A device dialing the relay arrives from wherever the relay hands its
// traffic over -- often this server's own loopback -- and with xl2tpd
// bound to the public address its L2TP never reaches it, though IPsec
// came up.
//
// Names are looked up at most once a minute; the agent runs every second.
type listenAddr struct {
	local   func() []netip.Addr
	resolve func(ctx context.Context, host string) []netip.Addr

	key     string
	at      time.Time
	decided string
}

func (l *listenAddr) addr(endpoints []string) string {
	key := strings.Join(endpoints, ",")
	if key == l.key && time.Since(l.at) < time.Minute {
		return l.decided
	}
	l.key, l.at = key, time.Now()
	l.decided = pickListen(endpoints, l.localAddrs(), l.lookup)
	return l.decided
}

func (l *listenAddr) localAddrs() []netip.Addr {
	if l.local != nil {
		return l.local()
	}
	var out []netip.Addr
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr().Unmap())
		}
	}
	return out
}

func (l *listenAddr) lookup(host string) []netip.Addr {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if l.resolve != nil {
		return l.resolve(ctx, host)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return nil
	}
	return ips
}

func pickListen(endpoints []string, local []netip.Addr, lookup func(string) []netip.Addr) string {
	var found []netip.Addr
	relayed := false
	for _, e := range endpoints {
		var addrs []netip.Addr
		if a, err := netip.ParseAddr(strings.Trim(e, "[]")); err == nil {
			addrs = []netip.Addr{a.Unmap()}
		} else {
			addrs = lookup(e)
		}
		for _, a := range addrs {
			if !a.Is4() || a.IsLoopback() {
				continue
			}
			if !slices.Contains(local, a) {
				relayed = true
			} else if !slices.Contains(found, a) {
				found = append(found, a)
			}
		}
	}
	if len(found) != 1 || relayed {
		return ""
	}
	return found[0].String()
}
