package routing

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Named address groups an operator can use instead of typing prefixes.
//
// These exist because the lists that matter are the ones people get wrong. An
// operator who means "stop customers reaching the rest of my rack" will write
// 192.168.0.0/16 and forget the other two private ranges, link-local, and the
// loopback that a misconfigured service on the host answers on. Naming the set
// makes the intent one word and the coverage complete.
const (
	GroupPrivate   = "private"
	GroupLoopback  = "loopback"
	GroupLinkLocal = "link-local"
	GroupCGNAT     = "cgnat"
	GroupMulticast = "multicast"
	GroupBogon     = "bogon"
)

var groups = map[string][]string{
	GroupPrivate: {
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	},
	GroupLoopback: {
		"127.0.0.0/8",
		"::1/128",
	},
	GroupLinkLocal: {
		"169.254.0.0/16",
		"fe80::/10",
	},
	// Carrier-grade NAT. Worth its own name because it is not private space and
	// an operator blocking "private" will not have covered it.
	GroupCGNAT: {
		"100.64.0.0/10",
	},
	GroupMulticast: {
		"224.0.0.0/4",
		"ff00::/8",
	},
}

// GroupNames lists the groups an operator can pick, in a stable order.
func GroupNames() []string {
	out := make([]string, 0, len(groups)+1)
	for name := range groups {
		out = append(out, name)
	}
	out = append(out, GroupBogon)
	sort.Strings(out)
	return out
}

// ExpandGroup returns the prefixes a named group stands for.
//
// The bogon group is the union of everything a packet from the internet has no
// business carrying as a destination. It is the one most operators actually
// want: blocking it keeps a customer from using the tunnel to reach the
// operator's own infrastructure, which is the single most common way a shared
// VPN server becomes a way into the rack behind it.
func ExpandGroup(name string) ([]netip.Prefix, bool) {
	name = strings.ToLower(strings.TrimSpace(name))

	if name == GroupBogon {
		var all []netip.Prefix
		for _, g := range []string{GroupPrivate, GroupLoopback, GroupLinkLocal, GroupCGNAT, GroupMulticast} {
			p, _ := ExpandGroup(g)
			all = append(all, p...)
		}
		return all, true
	}

	raw, ok := groups[name]
	if !ok {
		return nil, false
	}
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out, true
}

// ParseTarget turns one entry an operator typed into prefixes.
//
// It accepts a group name, a CIDR, or a bare address, so the same field takes
// "private", "10.0.0.0/8" and "1.1.1.1" without the operator having to know
// which form the panel wanted. Anything else is reported with the text that was
// not understood, because "invalid input" on a list of forty entries tells
// nobody which one to fix.
func ParseTarget(s string) ([]netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	if p, ok := ExpandGroup(s); ok {
		return p, nil
	}

	if p, err := netip.ParsePrefix(s); err == nil {
		return []netip.Prefix{p.Masked()}, nil
	}

	if a, err := netip.ParseAddr(s); err == nil {
		bits := 32
		if !a.Unmap().Is4() {
			bits = 128
		}
		return []netip.Prefix{netip.PrefixFrom(a.Unmap(), bits)}, nil
	}

	return nil, fmt.Errorf("%w: %q is not an address, a range, or one of the named groups (%s)",
		ErrInvalidPolicy, s, strings.Join(GroupNames(), ", "))
}

// ParseTargets expands a whole list, keeping the first failure.
func ParseTargets(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	seen := map[netip.Prefix]bool{}
	for _, e := range entries {
		ps, err := ParseTarget(e)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// ParsePorts turns "25", "6881-6889" and mixed lists into ranges.
func ParsePorts(entries []string) ([]PortRange, error) {
	var out []PortRange
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		lo, hi := e, e
		if from, to, ok := strings.Cut(e, "-"); ok {
			lo, hi = strings.TrimSpace(from), strings.TrimSpace(to)
		}
		var a, b int
		if _, err := fmt.Sscanf(lo, "%d", &a); err != nil {
			return nil, fmt.Errorf("%w: %q is not a port number", ErrInvalidPolicy, e)
		}
		if _, err := fmt.Sscanf(hi, "%d", &b); err != nil {
			return nil, fmt.Errorf("%w: %q is not a port number", ErrInvalidPolicy, e)
		}
		if a < 1 || a > 65535 || b < 1 || b > 65535 {
			return nil, fmt.Errorf("%w: %q is outside 1-65535", ErrInvalidPolicy, e)
		}
		if b < a {
			return nil, fmt.Errorf("%w: %q ends before it starts", ErrInvalidPolicy, e)
		}
		out = append(out, PortRange{From: uint16(a), To: uint16(b)})
	}
	return out, nil
}
