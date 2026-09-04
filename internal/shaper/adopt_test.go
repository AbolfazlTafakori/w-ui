package shaper

import (
	"strings"
	"testing"
)

// A hierarchy this panel already built must be adoptable, not rebuilt.
//
// The kernel carries out `tc qdisc replace` over an existing htb as a change,
// and htb has no change operation, so the whole batch fails on its first line.
// That is what happened on a live server after an ordinary restart: the shaper
// tried to write its root again every two seconds, every attempt failed, and
// so no customer class was ever added — every speed limit on that device did
// nothing, silently, while the panel logged a failure it kept retrying.
func TestAdoptingAnExistingRootDoesNotRewriteTheQdisc(t *testing.T) {
	got := RootClassScript("wg0")

	if strings.Contains(got, "qdisc") {
		t.Errorf("adopting an existing hierarchy still writes a qdisc:\n%s", got)
	}
	if !strings.Contains(got, "class replace dev wg0") {
		t.Errorf("adopting an existing hierarchy skips the catch-all class:\n%s", got)
	}
}

// A device seen for the first time still gets the whole thing, root included.
func TestAFreshDeviceGetsTheRootAndTheClass(t *testing.T) {
	got := RootScript("wg0")

	if !strings.Contains(got, "qdisc replace dev wg0 root handle 1: htb") {
		t.Errorf("a fresh device did not get a root qdisc:\n%s", got)
	}
	if !strings.Contains(got, "class replace dev wg0") {
		t.Errorf("a fresh device did not get the catch-all class:\n%s", got)
	}
}

// The two must not drift apart. Whatever the catch-all class is, a fresh
// device and an adopted one have to end up with the same one, or a device's
// unclassified traffic would be treated differently depending on whether the
// panel had been restarted.
func TestBothPathsAgreeOnTheCatchAllClass(t *testing.T) {
	adopted := RootClassScript("wg0")
	if !strings.Contains(RootScript("wg0"), adopted) {
		t.Errorf("the fresh and adopted hierarchies disagree:\nfresh:\n%s\nadopted:\n%s",
			RootScript("wg0"), adopted)
	}
}
