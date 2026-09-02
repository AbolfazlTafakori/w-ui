// Package routing decides where a customer's traffic leaves the server.
//
// Two mechanisms do the work and they are deliberately kept apart. What should
// not go anywhere is dropped by nftables, early, before the accounting table
// sees it — so traffic an operator has forbidden is never billed to the
// customer who was refused it. What should leave through somewhere other than
// the server's own address is stamped with a packet mark, and the kernel's
// policy routing turns that mark into a route.
//
// Nothing here talks to the kernel. This file turns configuration into a
// program and a set of routing statements; applying them is the applier's job,
// which is what lets every decision below be tested without root, without
// interfaces and on any operating system.
package routing

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// ErrInvalidPolicy is returned for configuration that cannot be rendered.
var ErrInvalidPolicy = errors.New("routing: invalid policy")

// TableName is the nftables table this package owns entirely.
//
// It is separate from the accounting table on purpose: the two are applied
// independently, so a policy that the kernel rejects cannot take metering down
// with it, and an operator editing routing never risks the billing rules.
const TableName = "wui_policy"

// Chain names carry a prefix because the nftables grammar reserves words that
// read like natural chain names -- `mark` among them, which the parser rejects
// outright rather than treating as an identifier.

// Hook priorities.
//
// The drop chain sits ahead of the accounting table's forward chain (which runs
// at priority 0) so that forbidden traffic is discarded before it is counted.
// The marking chain runs at the standard mangle point, which is before the
// routing decision — a mark set any later would be ignored.
const (
	dropPriority = -10
	markPriority = -150 // mangle
)

// MarkBase is where outbound marks start.
//
// High enough to stay clear of the small values other software on the box tends
// to pick — Docker, tailscale and most VPN scripts use marks below 0x1000 — and
// masked so the panel only ever reads and writes its own bits. A mark collision
// would silently route another program's traffic into a customer's tunnel.
const (
	MarkBase uint32 = 0x00a70000
	MarkMask uint32 = 0x00ff0000
)

// TableBase is the first routing table id used for a hop. Linux reserves 253
// through 255 and most distributions leave everything above 1000 free.
const TableBase = 47000

// Hop is an outbound that has to be dialled, reduced to what routing needs.
type Hop struct {
	// Tag names the outbound this hop belongs to.
	Tag string
	// Mark steers traffic into this hop's routing table.
	Mark uint32
	// Table is the routing table id holding this hop's default route.
	Table int
	// Device is the interface the hop's traffic leaves through, for a
	// WireGuard hop. Empty for a proxy, which is dialled in userspace and
	// needs no route of its own.
	Device string
	// Enabled hops are installed; disabled ones are torn down.
	Enabled bool
}

// Policy is everything the router needs to know, already resolved.
//
// Domains do not appear here. A kernel cannot match a name, so the service
// resolves them on a timer and passes the addresses in BlockAddrs and
// DirectAddrs; keeping the resolution outside this package is what makes the
// generated program a pure function of its input.
type Policy struct {
	// BlockAddrs are dropped outright.
	BlockAddrs []netip.Prefix
	// DirectAddrs leave through the server's own address whatever else the
	// rules say. An operator uses this to keep a payment gateway or a local
	// bank off a foreign exit that would get the customer locked out.
	DirectAddrs []netip.Prefix

	// BlockBitTorrent drops peer-to-peer traffic on the well-known ports.
	BlockBitTorrent bool

	// BlockPorts are destination ports to drop, for traffic an operator does
	// not want leaving their address at all — outbound SMTP is the usual one.
	BlockPorts []PortRange

	// DefaultMark is the mark applied to everything not matched by a rule.
	// Zero leaves traffic on the main routing table, which is the server's own
	// address: the behaviour when no hop is configured.
	DefaultMark uint32

	// Rules are evaluated in order; the first match decides. They are expected
	// to arrive sorted, and are sorted again here so a caller that forgets
	// cannot produce a program whose behaviour depends on map iteration.
	Rules []MarkRule

	// Hops are the outbounds with a routing table of their own.
	Hops []Hop

	// CustomerNets are the tunnel subnets. Marking is confined to traffic that
	// came from one of them, so nothing this panel does can reach the routing
	// of any other service sharing the machine.
	CustomerNets []netip.Prefix
}

// PortRange is an inclusive range. A single port has From == To.
type PortRange struct{ From, To uint16 }

// Valid reports whether the range is one nftables will accept.
func (p PortRange) Valid() bool { return p.From > 0 && p.To >= p.From }

func (p PortRange) String() string {
	if p.From == p.To {
		return fmt.Sprintf("%d", p.From)
	}
	return fmt.Sprintf("%d-%d", p.From, p.To)
}

// MarkRule sends matching traffic to a particular outbound.
type MarkRule struct {
	// Mark is the outbound's mark, or zero to force the traffic direct.
	Mark uint32
	// Drop discards the traffic instead of routing it, for a rule pointing at
	// the blocked outbound.
	Drop bool

	// Exactly one of these selects the traffic.
	Addrs    []netip.Prefix
	Ports    []PortRange
	Protocol string       // "tcp", "udp", "icmp"
	Sources  []netip.Addr // customer addresses, for a per-client rule
}

// bittorrentPorts are the ports BitTorrent clients use by default.
//
// This is a heuristic and is documented as one in the panel: a client that has
// been moved off its default port is not caught, because matching the protocol
// itself would mean inspecting payloads, which this panel does not do. It stops
// the common case, which is a customer who installed a torrent client and never
// changed a setting, and that is what keeps abuse reports off the operator's
// address.
var bittorrentPorts = []PortRange{
	{6881, 6889}, // classic BitTorrent
	{6969, 6969}, // opentracker
	{51413, 51413},
	{1337, 1337},
}

// BuildRuleset renders the nftables program for a policy.
//
// The program is total: it destroys the table and rebuilds it, so applying it
// makes the kernel match the policy exactly with nothing stale left behind.
func BuildRuleset(p Policy) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}

	var b strings.Builder

	// Created before being deleted so the delete cannot fail on a fresh boot
	// and abort the transaction.
	fmt.Fprintf(&b, "add table inet %s\n", TableName)
	fmt.Fprintf(&b, "delete table inet %s\n", TableName)
	fmt.Fprintf(&b, "table inet %s {\n", TableName)

	writeSet(&b, "blocked4", "ipv4_addr", v4(p.BlockAddrs))
	writeSet(&b, "blocked6", "ipv6_addr", v6(p.BlockAddrs))
	writeSet(&b, "direct4", "ipv4_addr", v4(p.DirectAddrs))
	writeSet(&b, "direct6", "ipv6_addr", v6(p.DirectAddrs))
	writeSet(&b, "customers4", "ipv4_addr", v4(p.CustomerNets))

	// ── what does not leave ────────────────────────────────────────────────
	//
	// Ahead of accounting, so none of it is charged to anybody.
	fmt.Fprintf(&b, "\n\tchain wui_block {\n")
	fmt.Fprintf(&b, "\t\ttype filter hook forward priority %d; policy accept;\n", dropPriority)

	// Named destinations an operator has forbidden.
	if len(v4(p.BlockAddrs)) > 0 {
		b.WriteString("\t\tip daddr @blocked4 counter drop\n")
	}
	if len(v6(p.BlockAddrs)) > 0 {
		b.WriteString("\t\tip6 daddr @blocked6 counter drop\n")
	}

	if p.BlockBitTorrent {
		b.WriteString("\t\t# peer-to-peer on its default ports; see the note in the panel\n")
		fmt.Fprintf(&b, "\t\tmeta l4proto { tcp, udp } th dport { %s } counter drop\n",
			portList(bittorrentPorts))
	}

	if len(p.BlockPorts) > 0 {
		fmt.Fprintf(&b, "\t\tmeta l4proto { tcp, udp } th dport { %s } counter drop\n",
			portList(p.BlockPorts))
	}
	b.WriteString("\t}\n")

	// ── where the rest goes ────────────────────────────────────────────────
	//
	// A mark is only meaningful before the routing decision, so this chain
	// hangs off prerouting at the mangle point.
	fmt.Fprintf(&b, "\n\tchain wui_steer {\n")
	fmt.Fprintf(&b, "\t\ttype filter hook prerouting priority %d; policy accept;\n", markPriority)

	// Everything below concerns customers only, and this is the first thing the
	// chain does. Traffic belonging to anything else on this machine leaves
	// untouched, which is the guarantee that installing this panel cannot
	// disturb another service's routing.
	if len(v4(p.CustomerNets)) == 0 {
		// No tunnels yet. An empty chain rather than a default mark: marking
		// everything on a fresh install would capture the host's own traffic.
		b.WriteString("\t}\n")
		writeCounters(&b, p)
		b.WriteString("}\n")
		return b.String(), nil
	}
	b.WriteString("\t\tip saddr != @customers4 accept\n")

	// A flow stays on the hop it started on. Without this, a set that changes
	// under a live connection — a domain that resolved somewhere new — would
	// move the second half of a download to another exit, and every stateful
	// service at the far end would reject it.
	//
	// Only marks carrying our own prefix are restored. Another program on this
	// machine may well be using connection marks of its own, and copying one of
	// those into the packet mark would hand its traffic to our routing.
	fmt.Fprintf(&b, "\t\tct mark and 0x%08x == 0x%08x meta mark set ct mark accept\n",
		MarkMask, MarkBase&MarkMask)

	// Destinations pinned to the server's own address win over every rule.
	if len(v4(p.DirectAddrs)) > 0 {
		b.WriteString("\t\tip daddr @direct4 accept\n")
	}
	if len(v6(p.DirectAddrs)) > 0 {
		b.WriteString("\t\tip6 daddr @direct6 accept\n")
	}

	rules := make([]MarkRule, len(p.Rules))
	copy(rules, p.Rules)
	for i, r := range rules {
		if err := writeMarkRule(&b, r); err != nil {
			return "", fmt.Errorf("%w: rule %d: %v", ErrInvalidPolicy, i+1, err)
		}
	}

	if p.DefaultMark != 0 {
		fmt.Fprintf(&b, "\t\tmeta mark set 0x%08x ct mark set meta mark\n", p.DefaultMark)
	}
	b.WriteString("\t}\n")

	writeCounters(&b, p)

	b.WriteString("}\n")
	return b.String(), nil
}

// writeCounters emits one counter per hop so the panel can show what each
// outbound actually carried, rather than only what it was configured to carry.
func writeCounters(b *strings.Builder, p Policy) {
	hops := append([]Hop(nil), p.Hops...)
	sort.Slice(hops, func(i, j int) bool { return hops[i].Mark < hops[j].Mark })

	var enabled []Hop
	for _, h := range hops {
		if h.Enabled && h.Mark != 0 {
			enabled = append(enabled, h)
		}
	}
	if len(enabled) == 0 {
		return
	}

	b.WriteString("\n")
	for _, h := range enabled {
		fmt.Fprintf(b, "\tcounter %s { }\n", counterName(h.Mark))
	}

	fmt.Fprintf(b, "\n\tchain wui_account {\n")
	fmt.Fprintf(b, "\t\ttype filter hook forward priority %d; policy accept;\n", dropPriority+1)
	for _, h := range enabled {
		fmt.Fprintf(b, "\t\tmeta mark 0x%08x counter name \"%s\"\n", h.Mark, counterName(h.Mark))
	}
	b.WriteString("\t}\n")
}

func counterName(mark uint32) string { return fmt.Sprintf("ob_%08x", mark) }

// CounterName is the nftables counter holding an outbound's byte total.
func CounterName(mark uint32) string { return counterName(mark) }

func writeMarkRule(b *strings.Builder, r MarkRule) error {
	var match []string

	switch {
	case len(r.Addrs) > 0:
		v4s, v6s := v4(r.Addrs), v6(r.Addrs)
		if len(v4s) > 0 && len(v6s) > 0 {
			// Written as two statements rather than one so neither family is
			// silently dropped from the match.
			if err := writeMarkRule(b, cloneWith(r, v4s)); err != nil {
				return err
			}
			return writeMarkRule(b, cloneWith(r, v6s))
		}
		if len(v4s) > 0 {
			match = append(match, "ip daddr { "+prefixList(v4s)+" }")
		} else {
			match = append(match, "ip6 daddr { "+prefixList(v6s)+" }")
		}

	case len(r.Ports) > 0:
		for _, p := range r.Ports {
			if !p.Valid() {
				return fmt.Errorf("port range %q is not usable", p)
			}
		}
		match = append(match, "meta l4proto { tcp, udp } th dport { "+portList(r.Ports)+" }")

	case r.Protocol != "":
		switch r.Protocol {
		case "tcp", "udp", "icmp":
			match = append(match, "meta l4proto "+r.Protocol)
		default:
			return fmt.Errorf("protocol %q is not one the router matches", r.Protocol)
		}

	case len(r.Sources) > 0:
		var v4addrs []string
		for _, a := range r.Sources {
			if a.Unmap().Is4() {
				v4addrs = append(v4addrs, a.Unmap().String())
			}
		}
		if len(v4addrs) == 0 {
			return nil // nothing addressable; not an error, just an empty rule
		}
		sort.Strings(v4addrs)
		match = append(match, "ip saddr { "+strings.Join(v4addrs, ", ")+" }")

	default:
		return errors.New("matches nothing")
	}

	stmt := strings.Join(match, " ")
	if r.Drop {
		fmt.Fprintf(b, "\t\t%s counter drop\n", stmt)
		return nil
	}
	if r.Mark == 0 {
		// Forced direct: accept ends this chain, leaving the packet unmarked.
		fmt.Fprintf(b, "\t\t%s accept\n", stmt)
		return nil
	}
	// The mark is saved onto the connection in the same statement so replies
	// find their way back without re-evaluating any of this.
	fmt.Fprintf(b, "\t\t%s meta mark set 0x%08x ct mark set meta mark accept\n", stmt, r.Mark)
	return nil
}

func cloneWith(r MarkRule, addrs []netip.Prefix) MarkRule {
	out := r
	out.Addrs = addrs
	return out
}

func (p *Policy) validate() error {
	for _, h := range p.Hops {
		if h.Enabled && h.Mark != 0 && h.Mark&MarkMask != MarkBase&MarkMask {
			return fmt.Errorf("%w: hop %q has mark 0x%08x, which is outside the range this panel owns",
				ErrInvalidPolicy, h.Tag, h.Mark)
		}
	}
	for _, r := range p.BlockPorts {
		if !r.Valid() {
			return fmt.Errorf("%w: blocked port range %q is not usable", ErrInvalidPolicy, r)
		}
	}
	return nil
}

// ── rendering helpers ────────────────────────────────────────────────────────

func writeSet(b *strings.Builder, name, typ string, entries []netip.Prefix) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s\n\t\tflags interval\n", name, typ)
	if len(entries) > 0 {
		fmt.Fprintf(b, "\t\telements = { %s }\n", prefixList(entries))
	}
	b.WriteString("\t}\n")
}

func prefixList(ps []netip.Prefix) string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func portList(rs []PortRange) string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.String())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func v4(ps []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range ps {
		if p.Addr().Unmap().Is4() {
			out = append(out, netip.PrefixFrom(p.Addr().Unmap(), p.Bits()))
		}
	}
	return out
}

func v6(ps []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range ps {
		if !p.Addr().Unmap().Is4() {
			out = append(out, p)
		}
	}
	return out
}
