package api

import (
	"fmt"
	"sync"
	"time"
)

// Slowing down guessing at the password.
//
// bcrypt already makes each attempt cost something, but a few a second is still
// hundreds of thousands a day against a panel reachable from the internet. The
// point here is to make sustained guessing pointless without locking out the
// operator who typed their own password wrong twice.
//
// So: a handful of free attempts, then a wait that doubles. The wait is capped,
// because an attacker who can hold an account locked forever has denied the
// operator their own panel — which is the attack, only cheaper.

const (
	// freeAttempts before any delay. Enough for an ordinary run of typos.
	freeAttempts = 5

	// baseLockout after the free attempts run out, doubling each time.
	baseLockout = 30 * time.Second

	// maxLockout caps the wait. Beyond a few minutes the attacker has already
	// lost, and the operator is the only one still being punished.
	maxLockout = 15 * time.Minute

	// forgetAfter clears a record that has been quiet. Someone who got it wrong
	// this morning should not be starting from a penalty tonight.
	forgetAfter = time.Hour
)

// attemptRecord is one key's recent history.
type attemptRecord struct {
	failures int
	last     time.Time
	until    time.Time
}

// throttle tracks failed sign-ins.
//
// Keys are tracked independently: the address someone is coming from, and the
// account they are aiming at. Tracking only the address would let a botnet
// spread the guessing across it; tracking only the account would let anyone
// lock an operator out by guessing at their username on purpose. Both, and
// neither alone, is what makes it work.
type throttle struct {
	mu      sync.Mutex
	records map[string]*attemptRecord
}

func newThrottle() *throttle {
	return &throttle{records: map[string]*attemptRecord{}}
}

// retryAfter reports how long a key must wait, and zero when it may proceed.
func (t *throttle) retryAfter(key string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	rec := t.records[key]
	if rec == nil {
		return 0
	}
	if now.Sub(rec.last) > forgetAfter {
		delete(t.records, key)
		return 0
	}
	if now.Before(rec.until) {
		return rec.until.Sub(now)
	}
	return 0
}

// fail records a failed attempt and returns the wait it earned.
func (t *throttle) fail(key string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	rec := t.records[key]
	if rec == nil || now.Sub(rec.last) > forgetAfter {
		rec = &attemptRecord{}
		t.records[key] = rec
	}
	rec.failures++
	rec.last = now

	if rec.failures <= freeAttempts {
		return 0
	}

	wait := baseLockout << (rec.failures - freeAttempts - 1)
	if wait > maxLockout || wait <= 0 {
		wait = maxLockout
	}
	rec.until = now.Add(wait)

	// Bounded so a flood of invented usernames cannot grow this without limit.
	t.sweep(now)
	return wait
}

// succeed clears a key's history.
func (t *throttle) succeed(key string) {
	t.mu.Lock()
	delete(t.records, key)
	t.mu.Unlock()
}

// sweep drops records nobody has touched recently. Called under the lock.
func (t *throttle) sweep(now time.Time) {
	if len(t.records) < 1024 {
		return
	}
	for k, rec := range t.records {
		if now.Sub(rec.last) > forgetAfter {
			delete(t.records, k)
		}
	}
}

// lockoutMessage says how long to wait, without confirming the account exists.
func lockoutMessage(wait time.Duration) string {
	seconds := int(wait.Round(time.Second).Seconds())
	if seconds < 60 {
		return fmt.Sprintf("too many attempts. Try again in %d seconds", seconds)
	}
	return fmt.Sprintf("too many attempts. Try again in %d minutes",
		int(wait.Round(time.Minute).Minutes()))
}
