package logger

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestNewestEntriesComeFirst(t *testing.T) {
	r := NewRing()
	base := time.Now()
	for i := 0; i < 5; i++ {
		r.Add(Entry{Time: base.Add(time.Duration(i) * time.Second), Message: string(rune('a' + i))})
	}

	got := r.Recent(10, "")
	if len(got) != 5 {
		t.Fatalf("got %d entries, want 5", len(got))
	}
	// Reading a log means reading what just happened, so the caller should not
	// have to reverse it.
	if got[0].Message != "e" || got[4].Message != "a" {
		t.Errorf("order is %s..%s, want newest first", got[0].Message, got[4].Message)
	}
}

func TestTheRingDropsTheOldestRatherThanGrowing(t *testing.T) {
	r := NewRing()
	base := time.Now()
	for i := 0; i < ringSize+50; i++ {
		r.Add(Entry{Time: base.Add(time.Duration(i) * time.Millisecond), Message: "line"})
	}

	// A panel that runs for months must not accumulate its whole log in memory.
	got := r.Recent(ringSize*2, "")
	if len(got) != ringSize {
		t.Errorf("held %d entries, want the ring to cap at %d", len(got), ringSize)
	}
}

func TestFilteringByLevelKeepsTheMoreSevere(t *testing.T) {
	r := NewRing()
	now := time.Now()
	r.Add(Entry{Time: now, Level: "DEBUG", Message: "d"})
	r.Add(Entry{Time: now, Level: "INFO", Message: "i"})
	r.Add(Entry{Time: now, Level: "WARN", Message: "w"})
	r.Add(Entry{Time: now, Level: "ERROR", Message: "e"})

	got := r.Recent(10, "WARN")
	if len(got) != 2 {
		t.Fatalf("got %d entries at WARN and above, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Level == "INFO" || e.Level == "DEBUG" {
			t.Errorf("%s leaked through a WARN filter", e.Level)
		}
	}
}

func TestAnEmptyRingReturnsNothingRatherThanBlanks(t *testing.T) {
	// Zero-valued slots must not be reported as entries with no time and no
	// message, which would fill the page with empty rows on a fresh panel.
	if got := NewRing().Recent(10, ""); len(got) != 0 {
		t.Errorf("a fresh ring returned %d entries: %+v", len(got), got)
	}
}

func TestHandlerCapturesWhatIsLogged(t *testing.T) {
	r := NewRing()
	log := slog.New(Tee(slog.NewTextHandler(discard{}, nil), r))

	log.With("interface", "wg0").Warn("something happened", "count", 3)

	got := r.Recent(10, "")
	if len(got) != 1 {
		t.Fatalf("captured %d entries, want 1", len(got))
	}
	if got[0].Message != "something happened" || got[0].Level != "WARN" {
		t.Errorf("entry = %+v", got[0])
	}
	// Attributes attached with With must survive, or every line logged by a
	// component would lose the field that says which one it was.
	if got[0].Fields["interface"] != "wg0" {
		t.Errorf("fields = %v, want the interface carried through", got[0].Fields)
	}
	if got[0].Fields["count"] != int64(3) {
		t.Errorf("count = %#v, want 3", got[0].Fields["count"])
	}
}

func TestAnErrorFieldIsStoredAsItsMessage(t *testing.T) {
	r := NewRing()
	log := slog.New(Tee(slog.NewTextHandler(discard{}, nil), r))

	// An error does not survive JSON as itself, and a log endpoint that fails
	// to encode is useless at exactly the moment it is needed.
	log.Error("it broke", "error", errors.New("the reason"))

	got := r.Recent(1, "")
	if len(got) != 1 || got[0].Fields["error"] != "the reason" {
		t.Errorf("fields = %+v, want the error rendered as text", got)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

var _ = context.Background
