package routing

import (
	"net/netip"
	"strings"
	"testing"
)

func mustPrefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatalf("bad test prefix %q: %v", s, err)
		}
		out = append(out, p)
	}
	return out
}

// customerPolicy is a panel with one tunnel and nothing else configured.
func customerPolicy() Policy {
	return Policy{CustomerNets: []netip.Prefix{netip.MustParsePrefix("10.66.0.0/16")}}
}

// ── the guarantees that matter to somebody else's software ───────────────────

func TestNothingButCustomerTrafficIsEverMarked(t *testing.T) {
	// The panel shares a machine with other things. If marking were not fenced
	// to the tunnel subnets, a mark meant for a customer would land on a
	// neighbouring service's packets and route them into somebody's VPN.
	p := customerPolicy()
	p.DefaultMark = MarkBase | 3

	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ip saddr != @customers4 accept") {
		t.Fatal("the marking chain does not exclude traffic from outside the tunnels")
	}
	// The exclusion has to come before anything that sets a mark, or it
	// excludes nothing. "meta mark set 0x" is a literal mark being applied;
	// the conntrack restore also contains "meta mark set" and is fenced
	// separately, so matching the bare phrase would prove nothing.
	guard := strings.Index(out, "ip saddr != @customers4")
	mark := strings.Index(out, "meta mark set 0x")
	if guard == -1 || mark == -1 || guard > mark {
		t.Fatalf("the guard is not ahead of the marking (guard=%d mark=%d)", guard, mark)
	}
}

func TestWithNoTunnelsNothingIsMarkedAtAll(t *testing.T) {
	// A fresh install has no interfaces. Marking everything by default would
	// capture the host's own traffic.
	p := Policy{DefaultMark: MarkBase | 1}
	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "meta mark set 0x") {
		t.Fatal("traffic is being marked with no customer subnets configured")
	}
}

func TestBlockedTrafficIsDroppedBeforeItIsBilled(t *testing.T) {
	// The accounting table hooks forward at priority 0. Anything the operator
	// forbade has to be discarded ahead of that, or the customer is charged for
	// bytes that were never delivered.
	if dropPriority >= 0 {
		t.Fatalf("the drop chain runs at priority %d, which is not ahead of accounting", dropPriority)
	}
	p := customerPolicy()
	p.BlockAddrs = mustPrefixes(t, "192.168.0.0/16")

	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ip daddr @blocked4 counter drop") {
		t.Fatal("blocked destinations are not dropped")
	}
}

func TestRepliesGoBackOutTheSameHop(t *testing.T) {
	// A stateful service at the far end rejects an answer arriving from a
	// different address than the request. Restoring the connection's mark is
	// what keeps both halves on one exit, and it has to be the first thing the
	// chain does.
	p := customerPolicy()
	p.DefaultMark = MarkBase | 2

	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	restore := strings.Index(out, "meta mark set ct mark accept")
	if restore == -1 {
		t.Fatal("the connection's mark is never restored; a flow could change exit mid-stream")
	}
	if set := strings.Index(out, "ct mark set meta mark"); set == -1 {
		t.Fatal("the mark is never saved onto the connection, so there is nothing to restore")
	} else if restore > set {
		t.Fatal("the mark is restored after it is set rather than before")
	}
}

func TestWeDoNotRestoreAnotherProgramsConnectionMark(t *testing.T) {
	// Other software on the box uses connection marks too. Copying one of
	// theirs into the packet mark would route their traffic through a
	// customer's hop, which is the loudest possible way to break a neighbour.
	p := customerPolicy()
	p.DefaultMark = MarkBase | 2

	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ct mark != 0x0 meta mark set") {
		t.Fatal("every connection mark is restored, including ones we did not set")
	}
	if !strings.Contains(out, "ct mark and") {
		t.Fatal("the connection mark is restored without checking it is one of ours")
	}
}

func TestPinnedDestinationsBeatEveryRule(t *testing.T) {
	// An operator pins a bank so a customer on a foreign exit can still log in.
	// A rule later in the list must not be able to take it back.
	p := customerPolicy()
	p.DirectAddrs = mustPrefixes(t, "5.6.7.0/24")
	p.Rules = []MarkRule{{Mark: MarkBase | 4, Addrs: mustPrefixes(t, "0.0.0.0/0")}}

	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	direct := strings.Index(out, "ip daddr @direct4 accept")
	rule := strings.Index(out, "0.0.0.0/0")
	if direct == -1 || rule == -1 || direct > rule {
		t.Fatalf("pinned destinations are not evaluated first (direct=%d rule=%d)", direct, rule)
	}
}

func TestAMarkOutsideOurRangeIsRefused(t *testing.T) {
	// Marks are the one thing that can reach out of this panel and affect
	// another program. Generating one we do not own is refused rather than
	// applied and apologised for.
	p := customerPolicy()
	p.Hops = []Hop{{Tag: "elsewhere", Mark: 0x00010001, Table: TableBase + 1, Enabled: true}}

	if _, err := BuildRuleset(p); err == nil {
		t.Fatal("a mark outside the panel's range was accepted")
	}
}

func TestOutputIsDeterministic(t *testing.T) {
	// Two runs of the same configuration must produce the same program, or the
	// applier reloads the kernel on every tick for no reason.
	p := customerPolicy()
	p.BlockAddrs = mustPrefixes(t, "192.168.0.0/16", "10.0.0.0/8", "172.16.0.0/12")
	p.DirectAddrs = mustPrefixes(t, "1.1.1.1/32", "8.8.8.8/32")
	p.Hops = []Hop{
		{Tag: "b", Mark: MarkBase | 2, Table: TableBase + 2, Device: "wgh2", Enabled: true},
		{Tag: "a", Mark: MarkBase | 1, Table: TableBase + 1, Device: "wgh1", Enabled: true},
	}

	first, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := BuildRuleset(p)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatal("the same policy rendered two different programs")
		}
	}
}

func TestBitTorrentBlockingCoversTheDefaultPorts(t *testing.T) {
	p := customerPolicy()
	p.BlockBitTorrent = true

	out, err := BuildRuleset(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"6881-6889", "6969", "51413"} {
		if !strings.Contains(out, want) {
			t.Errorf("port %s is not blocked", want)
		}
	}
	if !strings.Contains(out, "drop") {
		t.Error("the ports are matched but nothing is dropped")
	}
}

// ── marks and tables ─────────────────────────────────────────────────────────

func TestMarksAreStableAndUnique(t *testing.T) {
	// Derived from the id, so a restart does not re-point a hop at another
	// hop's table.
	seenMark := map[uint32]uint{}
	seenTable := map[int]uint{}
	for id := uint(1); id <= 500; id++ {
		mark, table, err := AllocateMark(id)
		if err != nil {
			t.Fatalf("id %d: %v", id, err)
		}
		if prev, dup := seenMark[mark]; dup {
			t.Fatalf("ids %d and %d share mark 0x%08x", prev, id, mark)
		}
		if prev, dup := seenTable[table]; dup {
			t.Fatalf("ids %d and %d share table %d", prev, id, table)
		}
		seenMark[mark], seenTable[table] = id, id

		if !OwnsMark(mark) {
			t.Fatalf("mark 0x%08x for id %d is not recognised as ours", mark, id)
		}
		if !OwnsTable(table) {
			t.Fatalf("table %d for id %d is not recognised as ours", table, id)
		}

		again, _, _ := AllocateMark(id)
		if again != mark {
			t.Fatalf("id %d produced two different marks", id)
		}
	}
}

func TestWeDoNotClaimOtherProgramsMarks(t *testing.T) {
	// The values other software on a VPN box actually uses.
	for _, foreign := range []uint32{0x0, 0x1, 0x64, 0x1000, 0x2000, 0xca6c, 0x80000} {
		if OwnsMark(foreign) {
			t.Errorf("0x%08x belongs to something else but is claimed as ours", foreign)
		}
	}
	for _, foreign := range []int{0, 1, 100, 253, 254, 255, 1000} {
		if OwnsTable(foreign) {
			t.Errorf("table %d belongs to something else but is claimed as ours", foreign)
		}
	}
}

func TestEveryHopIsTornDownBeforeItIsBuilt(t *testing.T) {
	// Applying twice must not stack duplicate rules, which is how a
	// policy-routing setup quietly stops working.
	hops := []Hop{{Tag: "de", Mark: MarkBase | 1, Table: TableBase + 1, Device: "wgh1", Enabled: true}}
	plan := BuildPlan(hops)

	if len(plan.Remove) == 0 {
		t.Fatal("nothing is removed before the rules are added")
	}
	found := false
	for _, s := range plan.Remove {
		if s.Args[0] == "rule" && s.Args[1] == "del" {
			found = true
		}
	}
	if !found {
		t.Fatal("the old rule is never deleted, so applies stack up")
	}
}

func TestADisabledHopLosesItsRuleRatherThanKeepingAnEmptyTable(t *testing.T) {
	// A rule pointing at a table with no default route is a black hole, and it
	// looks to a customer exactly like the internet being down.
	hops := []Hop{{Tag: "off", Mark: MarkBase | 5, Table: TableBase + 5, Device: "wgh5", Enabled: false}}
	plan := BuildPlan(hops)

	for _, s := range plan.Add {
		if s.Args[0] == "rule" && s.Args[1] == "add" {
			t.Fatal("a disabled hop still gets a routing rule")
		}
	}
	flushed := false
	for _, s := range plan.Remove {
		if s.Args[0] == "route" && s.Args[1] == "flush" {
			flushed = true
		}
	}
	if !flushed {
		t.Fatal("a disabled hop's table is left populated")
	}
}

func TestNoStatementIsEverAShellString(t *testing.T) {
	// Arguments go to exec directly. A tag an operator typed must never end up
	// somewhere a shell would split it.
	hops := []Hop{{Tag: "a; rm -rf /", Mark: MarkBase | 1, Table: TableBase + 1, Device: "wgh1", Enabled: true}}
	plan := BuildPlan(hops)

	for _, s := range append(plan.Add, plan.Remove...) {
		for _, a := range s.Args {
			if strings.ContainsAny(a, ";|&$`") {
				t.Fatalf("argument %q carries shell metacharacters", a)
			}
		}
	}
}

// ── operator input ───────────────────────────────────────────────────────────

func TestNamedGroupsCoverWhatTheyClaim(t *testing.T) {
	private, ok := ExpandGroup(GroupPrivate)
	if !ok {
		t.Fatal("the private group is missing")
	}
	// The mistake this exists to prevent: remembering one of the three.
	want := map[string]bool{"10.0.0.0/8": false, "172.16.0.0/12": false, "192.168.0.0/16": false}
	for _, p := range private {
		if _, ok := want[p.String()]; ok {
			want[p.String()] = true
		}
	}
	for cidr, found := range want {
		if !found {
			t.Errorf("the private group does not cover %s", cidr)
		}
	}

	bogon, ok := ExpandGroup(GroupBogon)
	if !ok || len(bogon) <= len(private) {
		t.Fatal("the bogon group should be a superset of the private one")
	}
}

func TestOperatorInputAcceptsEveryReasonableForm(t *testing.T) {
	for _, in := range []string{"private", "10.0.0.0/8", "1.1.1.1", " 8.8.8.8 ", "PRIVATE"} {
		if _, err := ParseTarget(in); err != nil {
			t.Errorf("%q was refused: %v", in, err)
		}
	}
}

func TestABadEntrySaysWhichOneItWas(t *testing.T) {
	// A list of forty entries and a message of "invalid input" tells nobody
	// what to fix.
	_, err := ParseTargets([]string{"10.0.0.0/8", "not-an-address", "1.1.1.1"})
	if err == nil {
		t.Fatal("a bad entry was accepted")
	}
	if !strings.Contains(err.Error(), "not-an-address") {
		t.Fatalf("the message does not name the offending entry: %v", err)
	}
}

func TestPortRangesAreCheckedBothWays(t *testing.T) {
	if _, err := ParsePorts([]string{"6889-6881"}); err == nil {
		t.Error("a backwards range was accepted")
	}
	if _, err := ParsePorts([]string{"70000"}); err == nil {
		t.Error("a port above 65535 was accepted")
	}
	if _, err := ParsePorts([]string{"0"}); err == nil {
		t.Error("port zero was accepted")
	}
	got, err := ParsePorts([]string{"25", "6881-6889"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (PortRange{25, 25}) || got[1] != (PortRange{6881, 6889}) {
		t.Fatalf("ranges parsed wrong: %+v", got)
	}
}
