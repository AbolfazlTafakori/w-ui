package api

import (
	"net"
	"net/http"
	"strings"
)

// Working out who is actually calling.
//
// The panel is normally reached through a proxy on the same machine — that is
// what the installer sets up, because the proxy is what holds the certificate.
// Every request therefore arrives from 127.0.0.1, and taking RemoteAddr at face
// value has two consequences, both real:
//
// The sign-in throttle keys on the caller. With one address for the whole
// internet it becomes a single shared bucket, so anyone at all can fail five
// sign-ins and lock the operator out of their own panel for up to fifteen
// minutes, again and again. A remote, unauthenticated denial of service by
// design rather than by bug.
//
// And the address recorded against a sign-in — the one thing that says where a
// session came from — is the proxy every time, so a compromise cannot be traced
// to anywhere.
//
// The fix is not to believe X-Forwarded-For: any client can send that, and
// believing it hands an attacker a throttle they can step around with a header
// and an audit log they can write. It is believed only when the machine that
// actually opened the connection is one we were told to trust.

// trustedProxies is who may speak for somebody else.
//
// Loopback by default and nothing else. A request whose peer is loopback either
// came from the proxy this panel was installed behind, or from something
// already running on the server — and in the second case the headers are the
// least of the problem. Everything else is taken at face value, which is the
// safe answer for a panel exposed directly.
var trustedProxies = mustCIDRs(
	"127.0.0.0/8",
	"::1/128",
)

// SetTrustedProxies replaces the set, for a deployment whose proxy is on
// another machine. An unparseable entry is reported and the rest are kept: a
// typo in one line should not silently drop the panel back to trusting nothing,
// nor to trusting everything.
func SetTrustedProxies(entries []string) []string {
	var bad []string
	var nets []*net.IPNet

	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if n, ok := parseCIDR(e); ok {
			nets = append(nets, n)
			continue
		}
		bad = append(bad, e)
	}
	if len(nets) > 0 {
		trustedProxies = nets
	}
	return bad
}

func parseCIDR(e string) (*net.IPNet, bool) {
	if _, n, err := net.ParseCIDR(e); err == nil {
		return n, true
	}
	// A bare address is the same thing with every bit fixed, and is what an
	// operator writes when they mean one machine.
	if ip := net.ParseIP(e); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, true
	}
	return nil, false
}

func mustCIDRs(entries ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		if n, ok := parseCIDR(e); ok {
			out = append(out, n)
		}
	}
	return out
}

func trusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// peerIP is the machine that actually opened the connection. Nothing a client
// sends can change it.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientIP is the address to throttle and to record.
//
// X-Forwarded-For is a chain, appended to by each proxy in turn, so the entry
// nearest the client is on the left and the least trustworthy. It is read from
// the right instead, stepping past the proxies we trust, and the first address
// that is not one of ours is the furthest point we have any reason to believe.
// Anything left of it was written by somebody we do not know.
func clientIP(r *http.Request) string {
	peer := peerIP(r)
	if !trusted(net.ParseIP(peer)) {
		return peer
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		// A proxy that sends only X-Real-IP. It carries one address rather than
		// a chain, so there is nothing to walk.
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			if ip := net.ParseIP(real); ip != nil {
				return ip.String()
			}
		}
		return peer
	}

	hops := strings.Split(forwarded, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		// A proxy may write "ip:port", and IPv6 may arrive in brackets.
		if h, _, err := net.SplitHostPort(hop); err == nil {
			hop = h
		}
		hop = strings.Trim(hop, "[]")

		ip := net.ParseIP(hop)
		if ip == nil {
			// Something unparseable was appended. Everything left of it is
			// whatever that thing chose to say, so this is as far as we go.
			return peer
		}
		if trusted(ip) {
			continue
		}
		return ip.String()
	}

	// Every hop was one of ours, which means the client is too.
	return peer
}

// clientScheme reports whether the browser's own connection was encrypted.
//
// The panel usually terminates no TLS of its own: the proxy holds the
// certificate and speaks plain HTTP to us. Reading only r.TLS therefore leaves
// a properly encrypted deployment looking unencrypted from in here, which is
// how HSTS came never to be sent on a panel that had been served over HTTPS
// since the day it was installed.
func clientScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if trusted(net.ParseIP(peerIP(r))) {
		if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
			// The first entry: the same left-is-nearest-the-client convention.
			if i := strings.IndexByte(proto, ','); i >= 0 {
				proto = strings.TrimSpace(proto[:i])
			}
			return strings.ToLower(proto)
		}
	}
	return "http"
}
