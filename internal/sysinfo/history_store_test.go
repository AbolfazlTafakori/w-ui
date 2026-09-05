package sysinfo

import (
	"path/filepath"
	"testing"
	"time"
)

// A week of two-second samples is two hundred thousand points per series. The
// ladder is what makes a week affordable, and the property that matters is that
// asking for a long span still answers — from a coarser tier — rather than
// returning the last hour and calling it a week.
func TestALongSpanIsAnsweredFromACoarserTier(t *testing.T) {
	s := NewStore("")
	now := time.Now()

	// Six hours of samples, one a minute. Too old for the two-second tier,
	// which only reaches back an hour.
	for i := 0; i < 6*60; i++ {
		at := now.Add(-time.Duration(6*60-i) * time.Minute)
		s.Add("cpu", at, float64(i%100))
	}

	if got := s.Range("cpu", time.Hour, 60); len(got) == 0 {
		t.Error("an hour of history came back empty")
	}
	six := s.Range("cpu", 6*time.Hour, 240)
	if len(six) == 0 {
		t.Fatal("six hours of history came back empty; the coarse tier was not used")
	}

	// And it really does reach further back than the fine tier could.
	oldest := six[0].T
	if now.Unix()-oldest < 3*3600 {
		t.Errorf("the six-hour answer only reaches back %d seconds", now.Unix()-oldest)
	}
}

// Averaging is the whole point of a coarse tier, so it has to actually average
// rather than keep the last value in the bucket.
func TestACoarseBucketHoldsTheMeanNotTheLast(t *testing.T) {
	s := NewStore("")
	base := time.Now().Add(-30 * time.Minute).Truncate(time.Minute)

	// Thirty samples inside one minute: 0..29, mean 14.5.
	for i := 0; i < 30; i++ {
		s.Add("cpu", base.Add(time.Duration(i*2)*time.Second), float64(i))
	}
	// A later sample so the minute bucket is closed.
	s.Add("cpu", base.Add(2*time.Minute), 100)

	got := s.Range("cpu", time.Hour, 60)
	if len(got) == 0 {
		t.Fatal("nothing came back")
	}
	// The first bucket covers the run of thirty.
	if got[0].V < 14 || got[0].V > 15 {
		t.Errorf("the bucket holds %.2f; the mean of 0..29 is 14.5", got[0].V)
	}
}

// The newest reading has to be visible before its bucket closes, or a chart of
// the last week ends up to ten minutes in the past and looks stalled.
func TestTheOpenBucketIsIncluded(t *testing.T) {
	s := NewStore("")
	s.Add("cpu", time.Now(), 42)

	got := s.Range("cpu", time.Hour, 60)
	if len(got) != 1 {
		t.Fatalf("got %d points, want the one just added", len(got))
	}
	if got[0].V != 42 {
		t.Errorf("the open bucket reads %.0f, want 42", got[0].V)
	}
}

// Nothing recorded is an empty list, not a nil that a page would render as a
// broken chart, and not an error.
func TestAnUnknownMetricIsEmptyRatherThanMissing(t *testing.T) {
	s := NewStore("")
	got := s.Range("nothing-here", time.Hour, 60)
	if got == nil {
		t.Error("an unknown metric returned nil rather than an empty list")
	}
	if len(got) != 0 {
		t.Errorf("an unknown metric returned %d points", len(got))
	}
}

// Anything older than the span asked for stays out of the answer, or a chart
// labelled "last 5 minutes" quietly shows an hour.
func TestOlderSamplesAreLeftOut(t *testing.T) {
	s := NewStore("")
	now := time.Now()

	s.Add("cpu", now.Add(-50*time.Minute), 99)
	s.Add("cpu", now, 1)

	got := s.Range("cpu", 5*time.Minute, 60)
	for _, p := range got {
		if p.V > 50 {
			t.Errorf("a sample from 50 minutes ago appeared in a five-minute range: %+v", p)
		}
	}
}

// ── across a restart ────────────────────────────────────────────────────────

// The history exists so somebody can look at last night. A restart is the most
// likely thing to have happened in between, so losing it then would leave the
// feature answering only the question the short in-memory window already did.
func TestHistorySurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.gob")
	base := time.Now().Add(-20 * time.Minute)

	before := NewStore(path)
	for i := 0; i < 60; i++ {
		before.Add("cpu", base.Add(time.Duration(i)*time.Second*2), float64(i))
	}
	// Close the open bucket of the fine tier by moving past it.
	before.Add("cpu", base.Add(10*time.Minute), 7)
	if err := before.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after := NewStore(path)
	if err := after.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := after.Range("cpu", time.Hour, 240)
	if len(got) == 0 {
		t.Fatal("the history was empty after a restart")
	}
	if len(after.Metrics()) != 1 || after.Metrics()[0] != "cpu" {
		t.Errorf("metrics after a restart: %v", after.Metrics())
	}
}

// A first start has no file, which is not a failure.
func TestAFirstStartHasNoHistoryAndDoesNotComplain(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "does-not-exist.gob"))
	if err := s.Load(); err != nil {
		t.Errorf("a missing history file was reported as an error: %v", err)
	}
}

// A store with nowhere to write is what a test uses, and must not try.
func TestAStoreWithNoPathDoesNotWrite(t *testing.T) {
	s := NewStore("")
	s.Add("cpu", time.Now(), 1)
	if err := s.Save(); err != nil {
		t.Errorf("a store with no path tried to save: %v", err)
	}
}

// The ring is bounded, or a server left running for a year holds a year.
func TestTheFineTierStaysBounded(t *testing.T) {
	s := NewStore("")
	base := time.Now().Add(-4 * time.Hour)

	// Two hours of two-second samples: more than the fine tier's capacity.
	for i := 0; i < 4000; i++ {
		s.Add("cpu", base.Add(time.Duration(i*2)*time.Second), float64(i%50))
	}

	s.mu.Lock()
	fine := s.series["cpu"].Tiers[0]
	held := len(fine.Samples)
	capacity := fine.Capacity
	s.mu.Unlock()

	if held > capacity {
		t.Errorf("the fine tier holds %d samples, above its capacity of %d", held, capacity)
	}
}
