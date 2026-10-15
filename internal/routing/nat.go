package routing

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Letting customers out.

// A tunnel gives a customer an address on a private subnet. Nothing on the
// internet routes 10.99.0.2, so without the source address being rewritten to
// the server's own, a customer connects, handshakes, and reaches nothing --
// which looks exactly like a broken tunnel and is the single most common way a
// working VPN appears not to work.
//
// wg-quick does this with a PostUp line. This panel creates its interfaces
// itself and never runs wg-quick, so nothing was doing it.

// NATTableName is the nftables table this file owns entirely.
//
// It is an ip table rather than a part of the inet table the rest of the
// package uses, and separate from it on purpose. nat chains in the inet family
// need a newer kernel than plain filtering does, and folding this in would mean
// a kernel that cannot do inet nat loses policy routing as well. Kept apart, an
// old kernel loses one thing, and the tunnel subnets are IPv4 anyway.
const NATTableName = "wui_nat"

// BuildNAT renders the masquerade program for a policy.
//
// The program is total: it destroys the table and rebuilds it, so applying it
// twice leaves the same thing behind and a subnet that was removed goes away.
func BuildNAT(nets []netip.Prefix, tunnels []string, redirects []PortRedirect) string {
	return buildNAT(nets, tunnels, redirects, true)
}

// BuildNATWithoutFib is BuildNAT for a kernel without nftables' fib
// expression, which a minimal or container kernel may lack: a port of a
// tunnel's own is told from a customer's traffic by its source instead --
// nothing from a tunnel's subnet is redirected -- rather than by whether it
// is addressed to this server.
func BuildNATWithoutFib(nets []netip.Prefix, tunnels []string, redirects []PortRedirect) string {
	return buildNAT(nets, tunnels, redirects, false)
}

func buildNAT(nets []netip.Prefix, tunnels []string, redirects []PortRedirect, fib bool) string {
	var b strings.Builder

	// Created before it is destroyed so the delete cannot fail on the first
	// run, which would abort the whole file.
	fmt.Fprintf(&b, "add table ip %s\n", NATTableName)
	fmt.Fprintf(&b, "delete table ip %s\n", NATTableName)
	fmt.Fprintf(&b, "table ip %s {\n", NATTableName)
	writeRedirects(&b, redirects, onlyV4(nets), fib)
	b.WriteString("\tchain postrouting {\n")
	b.WriteString("\t\ttype nat hook postrouting priority srcnat; policy accept;\n")

	v4 := onlyV4(nets)
	if len(v4) == 0 {
		// No tunnels. An empty chain rather than no table: the table still
		// exists, so the next apply replaces it instead of leaving whatever a
		// previous run put there.
		b.WriteString("\t}\n}\n")
		return b.String()
	}

	// Traffic going back into a tunnel is left alone. Rewriting it would make
	// one customer's packets arrive at another as though they came from the
	// server, and the per-peer address filter exists precisely to stop that.
	// Matched by destination as well as by device: an L2TP customer is
	// reached through a PPP device of the session's own, which is never the
	// tunnel's name.
	if len(tunnels) > 0 {
		fmt.Fprintf(&b, "\t\toifname { %s } return\n", quoted(tunnels))
	}
	fmt.Fprintf(&b, "\t\tip daddr { %s } return\n", prefixList(v4))

	fmt.Fprintf(&b, "\t\tip saddr { %s } counter masquerade\n", prefixList(v4))
	b.WriteString("\t}\n}\n")
	return b.String()
}

// writeRedirects renders the ports of tunnels that have one of their own,
// sent to the fixed port their daemon holds. Only what is addressed to this
// server is redirected: traffic a customer sends through it to the same
// port somewhere else passes untouched.
func writeRedirects(b *strings.Builder, redirects []PortRedirect, v4 []netip.Prefix, fib bool) {
	if len(redirects) == 0 {
		return
	}
	rs := append([]PortRedirect(nil), redirects...)
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Proto != rs[j].Proto {
			return rs[i].Proto < rs[j].Proto
		}
		if rs[i].From != rs[j].From {
			return rs[i].From < rs[j].From
		}
		return rs[i].Marked && !rs[j].Marked
	})
	b.WriteString("\tchain prerouting {\n")
	b.WriteString("\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	for _, r := range rs {
		if (r.Proto != "tcp" && r.Proto != "udp") || r.From == 0 || r.To == 0 || r.From == r.To {
			continue
		}
		match := "fib daddr type local"
		if !fib {
			if len(v4) == 0 {
				continue
			}
			match = fmt.Sprintf("ip saddr != { %s }", prefixList(v4))
		}
		marker := ""
		if r.Marked {
			if r.Proto != "udp" {
				continue
			}
			marker = " @th,64,32 0"
		}
		fmt.Fprintf(b, "\t\t%s %s dport %d%s counter redirect to :%d\n", match, r.Proto, r.From, marker, r.To)
	}
	b.WriteString("\t}\n")
}

// onlyV4 keeps the prefixes masquerade can act on here.
func onlyV4(in []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if p.Addr().Is4() {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// quoted renders interface names the way nftables wants them, sorted so the
// program only changes when the interfaces do.
func quoted(names []string) string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	parts := make([]string, 0, len(out))
	for _, n := range out {
		parts = append(parts, fmt.Sprintf("%q", n))
	}
	return strings.Join(parts, ", ")
}
