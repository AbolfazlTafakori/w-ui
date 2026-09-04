package enforce

import (
	"reflect"
	"testing"
)

// The whole point of the check: a table that was cleared behind the panel's
// back must be noticed. Skipping the apply because the text has not changed is
// only safe while the kernel still holds what that text described.
func TestMissingKeysNoticesAClearedTable(t *testing.T) {
	applied := ruleKeys([]Rule{{Key: "c1"}, {Key: "c2"}, {Key: "c3"}})

	got := missingKeys(applied, nil)
	want := []string{"c1", "c2", "c3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missingKeys() on an empty kernel = %v, want %v", got, want)
	}
}

// A counter exists from the moment it is created, whether or not a byte has
// passed through it. An idle customer is present, not missing -- reporting them
// as gone would make the panel rewrite the ruleset every tick on a quiet
// server, which is the cost the cache exists to avoid.
func TestMissingKeysLeavesIdleCustomersAlone(t *testing.T) {
	applied := ruleKeys([]Rule{{Key: "c1"}, {Key: "c2"}})
	seen := []Usage{{Key: "c1"}, {Key: "c2"}}

	if got := missingKeys(applied, seen); got != nil {
		t.Errorf("missingKeys() called an idle customer missing: %v", got)
	}
}

// Half a ruleset is the case that actually happened: something wrote the table
// while ours was in it. The customers still there are not the problem; the one
// that is gone is.
func TestMissingKeysFindsThePartialCase(t *testing.T) {
	applied := ruleKeys([]Rule{{Key: "c1"}, {Key: "c7"}})
	seen := []Usage{{Key: "c1", Bytes: 4096}}

	got := missingKeys(applied, seen)
	if !reflect.DeepEqual(got, []string{"c7"}) {
		t.Errorf("missingKeys() = %v, want [c7]", got)
	}
}

// Nothing applied, nothing to miss. A panel with no customers must not report
// a problem every two seconds for the rest of its life.
func TestMissingKeysSaysNothingWhenNothingWasApplied(t *testing.T) {
	if got := missingKeys(nil, nil); got != nil {
		t.Errorf("missingKeys() with no rules = %v, want nothing", got)
	}
	if got := missingKeys(map[string]struct{}{}, []Usage{{Key: "c9"}}); got != nil {
		t.Errorf("missingKeys() complained about a key it never applied: %v", got)
	}
}

// Somebody else's counters in the same table are not ours to account for, and
// their absence is not our ruleset being cleared.
func TestMissingKeysIgnoresKeysWeNeverApplied(t *testing.T) {
	applied := ruleKeys([]Rule{{Key: "c1"}})
	seen := []Usage{{Key: "c1"}, {Key: "c42"}}

	if got := missingKeys(applied, seen); got != nil {
		t.Errorf("missingKeys() = %v, want nothing", got)
	}
}
