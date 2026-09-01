package api

import (
	"testing"
	"time"
)

func TestOrdinaryTyposAreNotPunished(t *testing.T) {
	th := newThrottle()
	now := time.Now()

	// An operator getting their own password wrong a few times must not be
	// locked out of their own panel. That is the attack, achieved for free.
	for i := 0; i < freeAttempts; i++ {
		if wait := th.fail("ip:1.2.3.4", now); wait != 0 {
			t.Fatalf("locked out after %d attempts", i+1)
		}
	}
	if wait := th.retryAfter("ip:1.2.3.4", now); wait != 0 {
		t.Errorf("retryAfter = %s after only the free attempts", wait)
	}
}

func TestSustainedGuessingIsSlowedDown(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	for i := 0; i < freeAttempts; i++ {
		th.fail("ip:1.2.3.4", now)
	}

	first := th.fail("ip:1.2.3.4", now)
	if first <= 0 {
		t.Fatal("no wait after exceeding the free attempts")
	}
	second := th.fail("ip:1.2.3.4", now)
	if second <= first {
		t.Errorf("the wait did not grow: %s then %s", first, second)
	}
}

func TestTheWaitIsCapped(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	var wait time.Duration
	for i := 0; i < 40; i++ {
		wait = th.fail("ip:1.2.3.4", now)
	}
	// An attacker who can hold an account locked indefinitely has taken the
	// panel from its operator, which is the attack rather than the defence.
	if wait > maxLockout {
		t.Errorf("wait grew to %s, past the %s cap", wait, maxLockout)
	}
}

func TestSigningInClearsTheHistory(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	for i := 0; i < freeAttempts+2; i++ {
		th.fail("ip:1.2.3.4", now)
	}
	th.succeed("ip:1.2.3.4")
	if wait := th.retryAfter("ip:1.2.3.4", now); wait != 0 {
		t.Errorf("still locked out after a correct password: %s", wait)
	}
}

func TestAQuietHourIsForgiven(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	for i := 0; i < freeAttempts+3; i++ {
		th.fail("ip:1.2.3.4", now)
	}
	// Someone who got it wrong this morning should not start the evening
	// already being punished.
	later := now.Add(forgetAfter + time.Minute)
	if wait := th.retryAfter("ip:1.2.3.4", later); wait != 0 {
		t.Errorf("a lockout survived %s of quiet: %s", forgetAfter, wait)
	}
}

func TestAddressesAndAccountsAreCountedApart(t *testing.T) {
	th := newThrottle()
	now := time.Now()

	// Tracking only the address lets a botnet spread the guessing across it;
	// tracking only the account lets anyone lock an operator out on purpose.
	for i := 0; i < freeAttempts+2; i++ {
		th.fail("ip:1.2.3.4", now)
	}
	if wait := th.retryAfter("user:admin", now); wait != 0 {
		t.Error("locking one address also locked the account")
	}
	if wait := th.retryAfter("ip:5.6.7.8", now); wait != 0 {
		t.Error("locking one address also locked a different one")
	}
}

func TestTheMessageDoesNotConfirmTheAccountExists(t *testing.T) {
	msg := lockoutMessage(90 * time.Second)
	for _, leak := range []string{"admin", "user", "account", "exists"} {
		if contains(msg, leak) {
			t.Errorf("the lockout message mentions %q: %s", leak, msg)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) > 0 && indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
