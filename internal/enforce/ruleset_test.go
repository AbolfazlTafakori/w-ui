package enforce

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func build(t *testing.T, rules ...Rule) string {
	t.Helper()
	s, err := BuildRuleset(rules)
	if err != nil {
		t.Fatalf("build ruleset: %v", err)
	}
	return s
}

func TestRulesetFlushesTableBeforeRebuilding(t *testing.T) {
	got := build(t)

	// Creating before deleting is what makes the flush work on a fresh host,
	// where deleting a table that does not exist would abort the transaction.
	addIdx := strings.Index(got, "add table inet wui")
	delIdx := strings.Index(got, "delete table inet wui")
	if addIdx < 0 || delIdx < 0 {
		t.Fatalf("missing flush preamble:\n%s", got)
	}
	if addIdx > delIdx {
		t.Error("delete comes before add; this fails on a host with no table yet")
	}
}

func TestLimitedClientDropsThenCounts(t *testing.T) {
	got := build(t, Rule{
		Key:        "c7",
		Addrs:      addrs("10.66.0.5"),
		QuotaBytes: 1000,
		UsedBytes:  400,
	})

	if !strings.Contains(got, `quota q_c7 { over 1000 bytes used 400 bytes }`) {
		t.Errorf("quota object not seeded from stored usage:\n%s", got)
	}

	chain := chainBody(t, got, "cl_c7")
	dropIdx := strings.Index(chain, "quota name")
	countIdx := strings.Index(chain, "counter name")
	if dropIdx < 0 || countIdx < 0 {
		t.Fatalf("chain missing quota or counter:\n%s", chain)
	}
	// If the counter ran first, bytes dropped for being over the limit would
	// still be billed as usage.
	if dropIdx > countIdx {
		t.Error("counter runs before the quota drop; dropped bytes would be counted as usage")
	}
}

func TestUnlimitedClientHasNoQuotaObject(t *testing.T) {
	got := build(t, Rule{Key: "c1", Addrs: addrs("10.66.0.2"), QuotaBytes: 0})

	if strings.Contains(got, "quota q_c1") {
		t.Error("an unlimited client should not get a quota object")
	}
	if !strings.Contains(got, `counter n_c1`) {
		t.Error("an unlimited client should still be counted for reporting")
	}
	if !strings.Contains(chainBody(t, got, "cl_c1"), "counter name") {
		t.Error("unlimited chain should count")
	}
}

func TestBlockedClientDropsUnconditionally(t *testing.T) {
	got := build(t, Rule{
		Key:        "c9",
		Addrs:      addrs("10.66.0.9"),
		QuotaBytes: 5000,
		Blocked:    true,
	})

	chain := chainBody(t, got, "cl_c9")
	if strings.TrimSpace(chain) != "drop" {
		t.Errorf("a blocked client should drop outright, got:\n%s", chain)
	}
}

func TestSeededUsageNeverExceedsTheQuota(t *testing.T) {
	// A client recorded as over their allowance must not produce a quota whose
	// `used` is above `over`; nft rejects that and the whole apply would fail,
	// taking every other customer's enforcement down with it.
	got := build(t, Rule{
		Key:        "c3",
		Addrs:      addrs("10.66.0.3"),
		QuotaBytes: 100,
		UsedBytes:  999,
	})
	if !strings.Contains(got, "quota q_c3 { over 100 bytes used 100 bytes }") {
		t.Errorf("seeded usage was not clamped to the quota:\n%s", got)
	}
}

func TestBothDirectionsShareOneChain(t *testing.T) {
	got := build(t, Rule{Key: "c4", Addrs: addrs("10.66.0.4"), QuotaBytes: 10})

	dl := mapBody(t, got, "dl")
	ul := mapBody(t, got, "ul")
	want := "10.66.0.4 : jump cl_c4"

	if !strings.Contains(dl, want) {
		t.Errorf("download map missing the client:\n%s", dl)
	}
	if !strings.Contains(ul, want) {
		t.Errorf("upload map missing the client:\n%s", ul)
	}
	// Sharing the chain is what makes the allowance apply to the customer
	// rather than separately to each direction.
	if strings.Count(got, "chain cl_c4 {") != 1 {
		t.Error("the two directions should reach the same chain, not two copies")
	}
}

func TestMultiDeviceClientMapsEveryAddressToOneChain(t *testing.T) {
	got := build(t, Rule{
		Key:        "c5",
		Addrs:      addrs("10.66.0.10", "10.66.0.11", "10.66.0.12"),
		QuotaBytes: 999,
	})

	dl := mapBody(t, got, "dl")
	for _, a := range []string{"10.66.0.10", "10.66.0.11", "10.66.0.12"} {
		if !strings.Contains(dl, a+" : jump cl_c5") {
			t.Errorf("address %s not routed to the client's chain:\n%s", a, dl)
		}
	}
	if strings.Count(got, "quota q_c5") != 1 {
		t.Error("three devices must share one quota, not get one each")
	}
}

func TestIPv6AddressesAreSkipped(t *testing.T) {
	// The maps are typed ipv4_addr; letting a v6 address through would produce
	// an element list nft refuses, failing the whole apply.
	got := build(t, Rule{
		Key:        "c6",
		Addrs:      addrs("10.66.0.6", "fd00::1"),
		QuotaBytes: 10,
	})
	if strings.Contains(got, "fd00::1") {
		t.Error("an IPv6 address reached an ipv4_addr map")
	}
	if !strings.Contains(got, "10.66.0.6 : jump cl_c6") {
		t.Error("the v4 address of the same client was dropped too")
	}
}

func TestEmptyRulesetIsStillValid(t *testing.T) {
	got := build(t)

	// A server with no clients must still produce a loadable program, or the
	// first apply after deleting everyone would fail.
	if !strings.Contains(got, "chain forward {") {
		t.Error("forward chain missing")
	}
	if strings.Contains(got, "elements = {  }") {
		t.Error("empty maps should omit the elements line entirely")
	}
	if !strings.Contains(got, "map dl {") || !strings.Contains(got, "map ul {") {
		t.Error("both maps should exist even with no clients")
	}
}

func TestForwardChainUsesVerdictMaps(t *testing.T) {
	got := build(t, Rule{Key: "c1", Addrs: addrs("10.66.0.2"), QuotaBytes: 1})

	for _, want := range []string{
		"type filter hook forward priority filter; policy accept;",
		"ip daddr vmap @dl",
		"ip saddr vmap @ul",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("forward chain missing %q:\n%s", want, got)
		}
	}
	// A rule per client would be a linear scan on every packet; the map is a
	// hash probe whose cost does not grow with the customer count.
	if strings.Contains(got, "ip daddr 10.66.0.2") {
		t.Error("per-client match in the hot chain instead of a map lookup")
	}
}

func TestOutputIsDeterministic(t *testing.T) {
	rules := []Rule{
		{Key: "c3", Addrs: addrs("10.66.0.3"), QuotaBytes: 30},
		{Key: "c1", Addrs: addrs("10.66.0.1"), QuotaBytes: 10},
		{Key: "c2", Addrs: addrs("10.66.0.2"), QuotaBytes: 20},
	}
	first := build(t, rules...)

	reordered := []Rule{rules[1], rules[2], rules[0]}
	second := build(t, reordered...)

	// Identical desired state must render identically, so an unchanged ruleset
	// can be recognised and skipped instead of reapplied every tick.
	if first != second {
		t.Error("ruleset depends on input order; unchanged state would look changed")
	}
}

func TestRejectsForeignKeys(t *testing.T) {
	// Keys are generated from numeric ids. Anything else means a caller built
	// one by hand from user input, which is how injection would reach the
	// generated script.
	for _, bad := range []string{"", "x1", "c", "c1; drop", "c-1", "cAB"} {
		if _, err := BuildRuleset([]Rule{{Key: bad, QuotaBytes: 1}}); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("key %q was accepted, want ErrInvalidRule", bad)
		}
	}
}

func TestKeyMatchesWhatTheBuilderAccepts(t *testing.T) {
	k := Key(42)
	if k != "c42" {
		t.Errorf("Key(42) = %q, want c42", k)
	}
	if _, err := BuildRuleset([]Rule{{Key: k, Addrs: addrs("10.0.0.2"), QuotaBytes: 1}}); err != nil {
		t.Errorf("a key from Key() was rejected by the builder: %v", err)
	}
}

// chainBody returns the statements inside a named chain.
func chainBody(t *testing.T, ruleset, chain string) string {
	t.Helper()
	start := strings.Index(ruleset, "chain "+chain+" {")
	if start < 0 {
		t.Fatalf("chain %s not found in:\n%s", chain, ruleset)
	}
	rest := ruleset[start:]
	end := strings.Index(rest, "\n\t}")
	if end < 0 {
		t.Fatalf("chain %s not closed", chain)
	}
	body := rest[strings.Index(rest, "{")+1 : end]
	return strings.TrimSpace(body)
}

// mapBody returns the elements line of a named map.
func mapBody(t *testing.T, ruleset, name string) string {
	t.Helper()
	start := strings.Index(ruleset, "map "+name+" {")
	if start < 0 {
		t.Fatalf("map %s not found", name)
	}
	rest := ruleset[start:]
	end := strings.Index(rest, "\n\t}")
	if end < 0 {
		t.Fatalf("map %s not closed", name)
	}
	return rest[:end]
}
