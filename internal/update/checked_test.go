package update

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A stand-in repository that counts the questions it is asked.
func stubRepository(t *testing.T, answer func() (*Release, error)) *int {
	t.Helper()
	asked := 0
	saved := fetchLatest
	fetchLatest = func(context.Context) (*Release, error) {
		asked++
		return answer()
	}
	reset := func() {
		checked.Lock()
		checked.rel, checked.err, checked.at = nil, nil, time.Time{}
		checked.Unlock()
	}
	reset()
	t.Cleanup(func() {
		fetchLatest = saved
		reset()
	})
	return &asked
}

// age makes the last answer look as old as d.
func age(d time.Duration) {
	checked.Lock()
	checked.at = time.Now().Add(-d)
	checked.Unlock()
}

func TestAnAnswerStandsForHalfAnHour(t *testing.T) {
	asked := stubRepository(t, func() (*Release, error) { return &Release{Version: "2.3.0"}, nil })
	ctx := context.Background()

	for range 5 {
		rel, isNewer, err := Checked(ctx, "2.2.2", false)
		if err != nil || rel.Version != "2.3.0" || !isNewer {
			t.Fatalf("Checked = %v, %v, %v", rel, isNewer, err)
		}
	}
	if *asked != 1 {
		t.Errorf("five visits asked the repository %d times, want once", *asked)
	}

	age(answerFor - time.Minute)
	Checked(ctx, "2.2.2", false)
	if *asked != 1 {
		t.Error("an answer younger than half an hour was asked again")
	}
	age(answerFor)
	Checked(ctx, "2.2.2", false)
	if *asked != 2 {
		t.Error("an answer half an hour old was not asked again")
	}
}

func TestAFreshCheckAsksAndIsKept(t *testing.T) {
	latest := "2.3.0"
	asked := stubRepository(t, func() (*Release, error) { return &Release{Version: latest}, nil })
	ctx := context.Background()

	Checked(ctx, "2.2.2", false)
	latest = "2.4.0"
	rel, _, _ := Checked(ctx, "2.2.2", true)
	if *asked != 2 || rel.Version != "2.4.0" {
		t.Fatalf("a fresh check asked %d times and saw %s", *asked, rel.Version)
	}
	// The next visit sees what the fresh check found, without asking.
	rel, _, _ = Checked(ctx, "2.2.2", false)
	if *asked != 2 || rel.Version != "2.4.0" {
		t.Errorf("the visit after a fresh check asked %d times and saw %s", *asked, rel.Version)
	}
}

func TestAFailureIsKeptForLess(t *testing.T) {
	asked := stubRepository(t, func() (*Release, error) { return nil, errors.New("could not reach the release list") })
	ctx := context.Background()

	if _, _, err := Checked(ctx, "2.2.2", false); err == nil {
		t.Fatal("a failed check reported no error")
	}
	Checked(ctx, "2.2.2", false)
	if *asked != 1 {
		t.Error("a server that cannot reach GitHub asked again on the next visit")
	}
	age(failureFor)
	Checked(ctx, "2.2.2", false)
	if *asked != 2 {
		t.Error("a failure five minutes old was not asked again")
	}
}

func TestAnAbandonedCheckIsNotKept(t *testing.T) {
	asked := stubRepository(t, func() (*Release, error) { return nil, context.Canceled })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	Checked(ctx, "2.2.2", false)
	Checked(context.Background(), "2.2.2", false)
	if *asked != 2 {
		t.Error("a check cut short by the page going away was kept as the answer")
	}
}
