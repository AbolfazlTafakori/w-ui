package routing

import (
	"net/netip"
	"strings"
	"testing"
)

func nets(t *testing.T, in ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// Without this rule a customer connects, completes a handshake, and reaches
// nothing — which looks like a broken tunnel and is the most common way a
// working VPN appears not to work.
func TestCustomerTrafficIsTranslatedOnTheWayOut(t *testing.T) {
	got := BuildNAT(nets(t, "10.99.0.0/24", "10.77.0.0/24"), []string{"wgir", "wgtest"}, nil)

	if !strings.Contains(got, "masquerade") {
		t.Fatalf("nothing rewrites the source address:\n%s", got)
	}
	for _, want := range []string{"10.99.0.0/24", "10.77.0.0/24"} {
		if !strings.Contains(got, want) {
			t.Errorf("subnet %s is not translated", want)
		}
	}
	if !strings.Contains(got, "type nat hook postrouting") {
		t.Error("the rule is not on the postrouting hook, so it never runs")
	}
}

// One customer's packets must not arrive at another as though the server sent
// them. The per-peer address filter exists to stop exactly that.
func TestTrafficBackIntoATunnelIsLeftAlone(t *testing.T) {
	got := BuildNAT(nets(t, "10.99.0.0/24"), []string{"wgir", "wgtest"}, nil)

	ret := strings.Index(got, "return")
	masq := strings.Index(got, "masquerade")
	if ret < 0 {
		t.Fatalf("tunnel-bound traffic is not excluded:\n%s", got)
	}
	if ret > masq {
		t.Error("the exclusion comes after the masquerade, so it never applies")
	}
	if !strings.Contains(got, `"wgir"`) || !strings.Contains(got, `"wgtest"`) {
		t.Errorf("not every tunnel is excluded:\n%s", got)
	}
}

// Applying it twice must leave the same thing behind, and a subnet that was
// removed must go away rather than linger.
func TestTheProgramReplacesWhateverWasThere(t *testing.T) {
	got := BuildNAT(nets(t, "10.99.0.0/24"), nil, nil)

	add := strings.Index(got, "add table ip "+NATTableName)
	del := strings.Index(got, "delete table ip "+NATTableName)
	if add < 0 || del < 0 {
		t.Fatalf("the table is not rebuilt from scratch:\n%s", got)
	}
	// Created before it is deleted, or the first run fails on a table that is
	// not there yet and the whole file is rejected.
	if add > del {
		t.Error("the delete comes first, so a first run would fail")
	}
}

// A fresh install has no tunnels. It must still produce a table, so the next
// apply replaces it rather than adding to whatever a previous run left.
func TestNoTunnelsStillLeavesAnEmptyTable(t *testing.T) {
	got := BuildNAT(nil, nil, nil)

	if !strings.Contains(got, "table ip "+NATTableName) {
		t.Errorf("no table at all:\n%s", got)
	}
	if strings.Contains(got, "masquerade") {
		t.Errorf("an install with no tunnels rewrites something:\n%s", got)
	}
}

// The program must only change when the tunnels do, or every tick rewrites the
// kernel's rules and resets the counters.
func TestTheProgramIsStable(t *testing.T) {
	a := BuildNAT(nets(t, "10.99.0.0/24", "10.77.0.0/24"), []string{"b", "a"}, nil)
	b := BuildNAT(nets(t, "10.77.0.0/24", "10.99.0.0/24"), []string{"a", "b"}, nil)
	if a != b {
		t.Errorf("the same tunnels in another order gave a different program:\n%s\n---\n%s", a, b)
	}
}

// Traffic to another customer is left alone by its destination too: an L2TP
// customer is reached through a PPP device that is never a tunnel's name.
func TestTrafficToACustomerIsLeftAloneByAddress(t *testing.T) {
	got := BuildNAT(nets(t, "10.99.0.0/24", "10.77.0.0/24"), []string{"wg0"}, nil)
	ret := strings.Index(got, "ip daddr { 10.77.0.0/24, 10.99.0.0/24 } return")
	masq := strings.Index(got, "masquerade")
	if ret < 0 || ret > masq {
		t.Fatalf("customer-bound traffic is not excluded by address before the masquerade:\n%s", got)
	}
}

// A tunnel's own port is redirected to its daemon's, for what is addressed
// to this server only; a redirect to itself or of no transport is left out.
func TestPortsOfTheirOwnAreRedirected(t *testing.T) {
	got := BuildNAT(nets(t, "10.99.0.0/24"), []string{"ike0"}, []PortRedirect{
		{Proto: "udp", From: 41500, To: 500},
		{Proto: "udp", From: 41500, To: 4500, Marked: true},
		{Proto: "tcp", From: 41723, To: 1723},
		{Proto: "udp", From: 1701, To: 1701},
		{Proto: "sctp", From: 9, To: 10},
	})
	for _, want := range []string{
		"type nat hook prerouting priority dstnat;",
		"fib daddr type local tcp dport 41723 counter redirect to :1723",
		"fib daddr type local udp dport 41500 @th,64,32 0 counter redirect to :4500\n\t\tfib daddr type local udp dport 41500 counter redirect to :500",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "dport 1701") || strings.Contains(got, "sctp") {
		t.Fatalf("a redirect that should be left out:\n%s", got)
	}
	if strings.Contains(BuildNAT(nets(t, "10.99.0.0/24"), nil, nil), "prerouting") {
		t.Fatal("a prerouting chain with nothing to redirect")
	}
}

// Without fib, a redirect is told from a customer's own traffic by source.
func TestRedirectsWithoutFibSpareTheCustomers(t *testing.T) {
	got := BuildNATWithoutFib(nets(t, "10.99.0.0/24"), nil, []PortRedirect{{Proto: "udp", From: 41500, To: 4500}})
	if !strings.Contains(got, "ip saddr != { 10.99.0.0/24 } udp dport 41500 counter redirect to :4500") || strings.Contains(got, "fib") {
		t.Fatalf("%s", got)
	}
}
