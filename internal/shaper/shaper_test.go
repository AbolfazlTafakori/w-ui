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
	if !strings.Contains(got, "qdisc add dev wg0 parent 1:5 handle 5: fq_codel") {
		t.Errorf("class has no leaf qdisc:\n%s", got)
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
