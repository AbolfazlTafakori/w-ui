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
	got := BuildNAT(nets(t, "10.99.0.0/24", "10.77.0.0/24"), []string{"wgir", "wgtest"})

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
	got := BuildNAT(nets(t, "10.99.0.0/24"), []string{"wgir", "wgtest"})

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
	got := BuildNAT(nets(t, "10.99.0.0/24"), nil)

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
	got := BuildNAT(nil, nil)

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
	a := BuildNAT(nets(t, "10.99.0.0/24", "10.77.0.0/24"), []string{"b", "a"})
	b := BuildNAT(nets(t, "10.77.0.0/24", "10.99.0.0/24"), []string{"a", "b"})
	if a != b {
		t.Errorf("the same tunnels in another order gave a different program:\n%s\n---\n%s", a, b)
	}
}
