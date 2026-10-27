package stealthgw

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const t0 = 1_700_000_000

// check judges an opening and records it when the rule would take it, as
// the gateway does for one its limits have room for.
func (r *replay) check(salt [32]byte, ts, now uint64) (verdict, uint64) {
	v, raise := r.judge(salt, ts, now)
	if v == accepted || v == aboveLimit {
		r.record(salt, ts)
	}
	return v, raise
}

func salt(b byte) (s [32]byte) {
	s[0], s[31] = b, ^b
	return s
}

// started is a rule whose limit was written once at t0.
func started(t *testing.T) *replay {
	t.Helper()
	path := filepath.Join(t.TempDir(), "replay")
	if err := writeLimit(path, t0-100); err != nil {
		t.Fatal(err)
	}
	r, err := openReplay(path, t0)
	if err != nil {
		t.Fatal(err)
	}
	if l := r.due(t0, 0); l != t0+limitStep || r.write(l) != nil {
		t.Fatalf("the first raise was %d", l)
	}
	return r
}

func TestTheRuleTakesASaltOnceInsideTheWindow(t *testing.T) {
	r := started(t)
	for _, tc := range []struct {
		what string
		s    byte
		ts   uint64
		want verdict
	}{
		{"now", 1, t0, accepted},
		{"the same salt again", 1, t0, seenBefore},
		{"the same salt with another timestamp", 1, t0 - 5, seenBefore},
		{"a minute old", 2, t0 - window, accepted},
		{"a minute and a second old", 3, t0 - window - 1, outsideWindow},
		{"a minute and a second ahead", 4, t0 + window + 1, outsideWindow},
		{"at the limit", 7, t0 + limitStep, accepted},
	} {
		if v, _ := r.check(salt(tc.s), tc.ts, t0); v != tc.want {
			t.Errorf("%s: %v, want %v", tc.what, v, tc.want)
		}
	}
}

func TestAClientAheadOfTheLimitWaitsForTheWriteAndCannotBeReplayed(t *testing.T) {
	r := started(t)
	v, raise := r.check(salt(1), t0+30, t0)
	if v != aboveLimit || raise != t0+30+limitStep {
		t.Fatalf("above the limit: %v, raise %d", v, raise)
	}
	// The packet that asked is spent, though it opened nothing.
	if l := r.due(t0, raise); l != raise || r.write(l) != nil {
		t.Fatalf("the raise written was %d", l)
	}
	if v, _ := r.check(salt(1), t0+30, t0+1); v != seenBefore {
		t.Fatalf("the packet that asked, again: %v", v)
	}
	if v, _ := r.check(salt(2), t0+31, t0+1); v != accepted {
		t.Fatalf("the client's next opening: %v", v)
	}
	// No client can push the limit further than the window and a step.
	if v, raise := r.check(salt(3), t0+window, t0); v != aboveLimit || r.due(t0, raise+1000) != t0+window+limitStep {
		t.Fatal("a raise went past the window")
	}
}

func TestARestartTakesNothingTheLastRunMayHaveTaken(t *testing.T) {
	r := started(t)
	if v, _ := r.check(salt(1), t0+limitStep, t0+5); v != accepted {
		t.Fatal(v)
	}
	// A kill: nothing of the cache is kept, only the limit on disk.
	again, err := openReplay(r.path, t0+6)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := again.check(salt(1), t0+limitStep, t0+6); v != atFloor {
		t.Fatalf("a salt taken before the restart: %v", v)
	}
	// Honest clients wait until the clock passes the old limit.
	again.write(again.due(t0+limitStep+1, 0))
	if v, _ := again.check(salt(2), t0+limitStep+1, t0+limitStep+1); v != accepted {
		t.Fatalf("after the floor: %v", v)
	}
}

func TestWithoutAFileTheFloorCoversWhatALostFileMightHave(t *testing.T) {
	dir := t.TempDir()
	damaged := filepath.Join(dir, "damaged")
	os.WriteFile(damaged, []byte("WUIRPLY1 and then something else entirely, 48 b"), 0o600)
	for _, path := range []string{filepath.Join(dir, "missing"), damaged} {
		r, err := openReplay(path, t0)
		if !errors.Is(err, errNoLimit) {
			t.Fatalf("%s: %v", path, err)
		}
		if v, _ := r.check(salt(1), t0+window, t0+window); v != atFloor {
			t.Fatalf("%s: a timestamp a lost file might have covered: %v", path, v)
		}
		if l := r.due(t0+window+1, 0); l != t0+window+1+limitStep {
			t.Fatalf("%s: raise %d", path, l)
		}
	}
}

func TestALimitIsOnlyInForceOnceItIsWritten(t *testing.T) {
	r := started(t)
	l := r.due(t0+15, 0)
	if l != t0+15+limitStep || r.due(t0+15, 0) != 0 {
		t.Fatal("a second raise was handed out while one was being written")
	}
	// A write that fails leaves the old limit.
	r.path = filepath.Join(r.path, "not a directory", "replay")
	if r.write(l) == nil {
		t.Fatal("a write into a file's path did not fail")
	}
	if v, _ := r.check(salt(1), t0+limitStep+1, t0+15); v != aboveLimit {
		t.Fatalf("above a limit that was not written: %v", v)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(r.path))), "replay"))
	if len(b) != 48 {
		t.Fatal("the old file was damaged")
	}
}

func TestNothingIsSpentThatIsNotRecorded(t *testing.T) {
	r := started(t)
	if v, _ := r.judge(salt(1), t0, t0); v != accepted {
		t.Fatal(v)
	}
	if v, _ := r.judge(salt(1), t0, t0); v != accepted {
		t.Fatal("a salt that was only judged was spent")
	}
	r.record(salt(1), t0)
	if v, _ := r.judge(salt(1), t0, t0); v != seenBefore {
		t.Fatal("a recorded salt was taken again")
	}
}

func TestOldSaltsLeaveTheCache(t *testing.T) {
	r := started(t)
	for i := range 10 {
		r.check(salt(byte(i)), t0-uint64(i), t0)
	}
	r.expire(t0 + window - 4)
	if _, _, n := r.state(); n != 5 {
		t.Fatalf("%d salts left, want the 5 the window still admits", n)
	}
}
