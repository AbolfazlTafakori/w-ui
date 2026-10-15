package shaper

import (
	"strings"
	"testing"
)

func TestClassNumberComesFromTheClientID(t *testing.T) {
	// The class has to be derivable from the key alone and be the same after a
	// restart, or an adopted hierarchy would be rebuilt from scratch and every
	// customer's queue reset.
	for _, c := range []struct {
		key  string
		want uint16
	}{{"c1", 1}, {"c42", 42}, {"c65534", 65534}} {
		got, err := Minor(c.key)
		if err != nil {
			t.Errorf("%s: %v", c.key, err)
			continue
		}
		if got != c.want {
			t.Errorf("Minor(%q) = %d, want %d", c.key, got, c.want)
		}
	}
}

func TestKeysThatCannotBeClassedAreRefused(t *testing.T) {
	// 0xffff is the catch-all class. Handing it to a customer would put every
	// unshaped packet into their limit.
	for _, key := range []string{"", "c", "c0", "c65535", "c99999", "nonsense", "42"} {
		if _, err := Minor(key); err == nil {
			t.Errorf("Minor(%q) was accepted", key)
		}
	}
}

func TestClassIDMatchesWhatTCWrites(t *testing.T) {
	// tc prints handles in hex. A decimal id here would silently address a
	// different class than the one nftables stamps.
	if got := ClassID(10); got != "1:a" {
		t.Errorf("ClassID(10) = %q, want 1:a", got)
	}
	if got := ClassID(255); got != "1:ff" {
		t.Errorf("ClassID(255) = %q, want 1:ff", got)
	}
}

func TestUnlimitedClientsStayOutOfTheHierarchy(t *testing.T) {
	// A class with a huge rate would still queue and schedule. Leaving them out
	// entirely means their packets take the default path and are not shaped.
	got, problems := toDesired([]Client{
		{Key: "c1", RateBitsPerSec: 0},
		{Key: "c2", RateBitsPerSec: 1_000_000},
	})
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(got) != 1 {
		t.Fatalf("desired = %v, want only the limited client", got)
	}
	if got[2] != 1_000_000 {
		t.Errorf("rate = %d, want 1000000", got[2])
	}
}

func TestAnUnschedulableRateIsRaisedRatherThanDropped(t *testing.T) {
	// Refusing it would leave the customer unlimited, which is the wrong way
	// round to fail on a limit someone is paying for.
	got, _ := toDesired([]Client{{Key: "c1", RateBitsPerSec: 10}})
	if got[1] != minRateBits {
		t.Errorf("rate = %d, want it raised to %d", got[1], minRateBits)
	}
}

func TestOneBadClientDoesNotLoseTheRest(t *testing.T) {
	got, problems := toDesired([]Client{
		{Key: "broken", RateBitsPerSec: 1_000_000},
		{Key: "c7", RateBitsPerSec: 2_000_000},
	})
	if len(problems) != 1 {
		t.Errorf("problems = %v, want one", problems)
	}
	if len(got) != 1 || got[7] != 2_000_000 {
		t.Errorf("desired = %v, want the good client kept", got)
	}
}

func TestNewClassIsAdded(t *testing.T) {
	d := computeDiff(desired{5: 1_000_000}, map[uint16]uint64{})
	if len(d.add) != 1 || d.add[0] != 5 {
		t.Fatalf("add = %v, want [5]", d.add)
	}
	if len(d.change)+len(d.remove) != 0 {
		t.Error("nothing should change or be removed on an empty device")
	}
}

func TestUnchangedClassIsLeftAlone(t *testing.T) {
	// The reconciler runs every couple of seconds. Rewriting a class each time
	// would reset the customer's queue continuously and wreck their throughput.
	d := computeDiff(desired{5: 1_000_000}, map[uint16]uint64{5: 1_000_000})
	if !d.empty() {
		t.Errorf("an unchanged device produced work: %+v", d)
	}
}

func TestChangedRateIsAlteredNotRecreated(t *testing.T) {
	// Deleting a class throws away the packets queued in it, so a customer
	// whose plan was edited would see a blip for no reason.
	d := computeDiff(desired{5: 2_000_000}, map[uint16]uint64{5: 1_000_000})
	if len(d.change) != 1 || d.change[0] != 5 {
		t.Fatalf("change = %v, want [5]", d.change)
	}
	if len(d.add)+len(d.remove) != 0 {
		t.Error("a rate change should not add or remove a class")
	}
}

func TestStaleClassIsRemovedButTheDefaultSurvives(t *testing.T) {
	d := computeDiff(desired{}, map[uint16]uint64{9: 1_000_000, defaultMinor: 10_000_000_000})
	if len(d.remove) != 1 || d.remove[0] != 9 {
		t.Fatalf("remove = %v, want only the stale class", d.remove)
	}
}

func TestScriptGivesEveryClassALeafQueue(t *testing.T) {
	want := desired{5: 1_000_000}
	got := BuildScript("wg0", want, computeDiff(want, map[uint16]uint64{}))

	if !strings.Contains(got, "class add dev wg0 parent 1: classid 1:5 htb rate 1000000bit ceil 1000000bit") {
		t.Errorf("class was not added with a ceiling:\n%s", got)
	}
	// Without this the class queues in a plain FIFO and a customer at their
	// ceiling builds a queue deep enough to make everything else unusable.
	if !strings.Contains(got, "qdisc add dev wg0 parent 1:5 fq_codel\n") {
		t.Errorf("class has no leaf qdisc:\n%s", got)
	}
}

// The first customer's class is 1:1, and its queue is never given the
// root's handle, 1: -- which the kernel refused, and every class on the
// device with it.
func TestTheFirstCustomersQueueIsNotTheRoot(t *testing.T) {
	want := desired{1: 20_000_000, 2: 5_000_000}
	got := BuildScript("wg0", want, computeDiff(want, map[uint16]uint64{}))
	if strings.Contains(got, "handle 1:") || !strings.Contains(got, "qdisc add dev wg0 parent 1:1 fq_codel") {
		t.Fatalf("%s", got)
	}
}

// What iproute2 6.19 prints -- the kind under "class", the rate in bytes a
// second -- is read as the classes it is, so a class already there is not
// added again, and a rate already right is not changed every pass.
func TestClassesAsIproute2_6_19PrintsThem(t *testing.T) {
	out := `[{"class":"htb","handle":"1:ffff","root":true,"prio":0,"rate":1250000000,"ceil":1250000000},` +
		`{"class":"htb","handle":"1:1","root":true,"prio":0,"rate":2500000,"ceil":2500000}]`
	have, err := classesFromJSON([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if have[1] != 20_000_000 || have[defaultMinor] != 10_000_000_000 {
		t.Fatalf("%v", have)
	}
	want := desired{1: 20_000_000}
	if d := computeDiff(want, have).withLeaves(want, have, map[uint16]bool{1: true}); !d.empty() {
		t.Fatalf("a hierarchy already right is changed: %+v", d)
	}
	// A class a failed batch left without its queue gets one.
	d := computeDiff(want, have).withLeaves(want, have, map[uint16]bool{})
	if s := BuildScript("wg0", want, d); s != "qdisc add dev wg0 parent 1:1 fq_codel\n" {
		t.Fatalf("%q", s)
	}
}

func TestRemovalTakesTheLeafFirst(t *testing.T) {
	got := BuildScript("wg0", desired{}, computeDiff(desired{}, map[uint16]uint64{5: 1_000_000}))

	qdisc := strings.Index(got, "qdisc del")
	class := strings.Index(got, "class del")
	if qdisc < 0 || class < 0 {
		t.Fatalf("removal is incomplete:\n%s", got)
	}
	// A class that still has a qdisc attached cannot be deleted.
	if qdisc > class {
		t.Errorf("the class is deleted before its leaf:\n%s", got)
	}
}

func TestRootIsReplaceableSoReRunningIsSafe(t *testing.T) {
	got := RootScript("wg0")
	if !strings.Contains(got, "qdisc replace dev wg0 root handle 1: htb default ffff") {
		t.Errorf("root qdisc is not idempotent:\n%s", got)
	}
	// Everything unclassified lands here; a real link's worth of rate would
	// throttle the panel's own traffic and every unlimited customer.
	if !strings.Contains(got, "classid 1:ffff htb rate 10gbit") {
		t.Errorf("no wide default class:\n%s", got)
	}
}

func TestRateStringsFromEitherTCVersionAreUnderstood(t *testing.T) {
	// tc reports a rate as a number on some builds and a string on others.
	// Guessing wrong would make every class look changed on every tick.
	for _, c := range []struct {
		in   string
		want uint64
	}{{"1Mbit", 1_000_000}, {"512Kbit", 512_000}, {"2Gbit", 2_000_000_000}, {"8000bit", 8000}} {
		got, ok := parseRateString(c.in)
		if !ok || got != c.want {
			t.Errorf("parseRateString(%q) = %d, %v; want %d", c.in, got, ok, c.want)
		}
	}
}

// An iproute2 that ignores -j on classes prints the plain listing; it is
// read rather than reported as broken JSON.
func TestPlainClassListingIsRead(t *testing.T) {
	out := "class htb 1:ffff root prio 0 rate 10Gbit ceil 10Gbit burst 1680b cburst 1680b \n" +
		"class htb 1:2a parent 1:ffff prio 0 rate 5Mbit ceil 5Mbit burst 1600b cburst 1600b \n"
	have, ok := parseClassText(out)
	if !ok {
		t.Fatal("listing not recognised")
	}
	if have[0xffff] != 10_000_000_000 || have[0x2a] != 5_000_000 {
		t.Fatalf("classes = %v", have)
	}
	if _, ok := parseClassText("Error: Cannot find device"); ok {
		t.Fatal("an error was read as a listing")
	}
	if _, ok := parseClassText(""); !ok {
		t.Fatal("an empty listing is a device with no classes")
	}
}

// A rate as tc prints it: in JSON a number of bytes a second, or text with a
// unit in bits.
func TestParseRate(t *testing.T) {
	for _, c := range []struct {
		in   any
		want uint64
		ok   bool
	}{
		{float64(125_000), 1_000_000, true},
		{"10Gbit", 10_000_000_000, true},
		{"2.5mbit", 2_500_000, true},
		{" 800 Kbit ", 800_000, true},
		{"64bit", 64, true},
		{"fast", 0, false},
		{nil, 0, false},
	} {
		got, ok := parseRate(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseRate(%v) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
