package model

import (
	"testing"
	"time"
)

// Why a reseller's customers are off, in every combination the fields allow.
//
// The order matters and is part of the rule: the owner's decision first, then
// the date, then the traffic. A reseller switched off whose term has also
// ended is "switched off" -- switching them back on is the one thing that
// could bring the customers back, and it would not, which is why the second
// reason has to be the one they are told after it.
func TestPauseReasonInEveryCase(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	future := now.Add(time.Hour)
	exactly := now

	cases := []struct {
		name  string
		admin Admin
		want  string
	}{
		{"a reseller in good standing", Admin{Role: RoleReseller, Enabled: true}, ""},
		{"switched off", Admin{Role: RoleReseller}, PauseSwitchedOff},

		{"a term still running", Admin{Role: RoleReseller, Enabled: true, ExpiresAt: &future}, ""},
		{"a term that ended a second ago", Admin{Role: RoleReseller, Enabled: true, ExpiresAt: &past}, PauseTermEnded},
		{"a term that ends this instant", Admin{Role: RoleReseller, Enabled: true, ExpiresAt: &exactly}, PauseTermEnded},
		{"a term on hold, not started", Admin{Role: RoleReseller, Enabled: true, DurationDays: 30}, ""},

		{"traffic with room left", Admin{Role: RoleReseller, Enabled: true, QuotaBytes: 100, UsedBytes: 99}, ""},
		{"traffic used to the byte", Admin{Role: RoleReseller, Enabled: true, QuotaBytes: 100, UsedBytes: 100}, PauseTrafficUsed},
		{"traffic used past it", Admin{Role: RoleReseller, Enabled: true, QuotaBytes: 100, UsedBytes: 250}, PauseTrafficUsed},
		{"no traffic ceiling, any amount used", Admin{Role: RoleReseller, Enabled: true, UsedBytes: 1 << 50}, ""},

		// Precedence.
		{"switched off and out of time", Admin{Role: RoleReseller, ExpiresAt: &past}, PauseSwitchedOff},
		{"switched off and out of traffic", Admin{Role: RoleReseller, QuotaBytes: 1, UsedBytes: 1}, PauseSwitchedOff},
		{"out of time and out of traffic", Admin{Role: RoleReseller, Enabled: true, ExpiresAt: &past, QuotaBytes: 1, UsedBytes: 1}, PauseTermEnded},

		// Other roles. The owner is never paused, whatever the row says; a
		// panel administrator holds no ceiling, so only the switch applies.
		{"the owner, with the switch off", Admin{Role: RoleOwner}, ""},
		{"the owner, with a date in the past", Admin{Role: RoleOwner, Enabled: true, ExpiresAt: &past}, ""},
		{"an administrator switched off", Admin{Role: RoleAdmin}, PauseSwitchedOff},
		{"an administrator with a date in the past", Admin{Role: RoleAdmin, Enabled: true, ExpiresAt: &past}, ""},
		{"an administrator with traffic used", Admin{Role: RoleAdmin, Enabled: true, QuotaBytes: 1, UsedBytes: 5}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.admin
			if got := a.PauseReason(now); got != tc.want {
				t.Fatalf("PauseReason = %q, want %q", got, tc.want)
			}
			if got := a.Suspended(now); got != (tc.want != "") {
				t.Fatalf("Suspended = %v, disagrees with PauseReason %q", got, tc.want)
			}
		})
	}
}

// Every reason has something to say, each says something different, and no
// reason at all says nothing.
func TestEveryPauseReasonHasItsOwnMessage(t *testing.T) {
	seen := map[string]string{}
	for _, reason := range []string{PauseSwitchedOff, PauseTermEnded, PauseTrafficUsed} {
		msg := PauseMessage(reason)
		if msg == "" {
			t.Errorf("%s has no message", reason)
		}
		if other, dup := seen[msg]; dup {
			t.Errorf("%s and %s are told the same thing: %q", reason, other, msg)
		}
		seen[msg] = reason
	}
	if msg := PauseMessage(""); msg != "" {
		t.Errorf("an operator in good standing is told %q", msg)
	}
	if msg := PauseMessage("something-new"); msg != "" {
		t.Errorf("an unknown reason is told %q rather than nothing", msg)
	}
}
