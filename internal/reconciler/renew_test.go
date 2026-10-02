package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The boundaries a plan renews on: counted from its anchor, one period at a
// time, so a renewal noticed late -- or after the panel was down for weeks --
// keeps the plan's own day rather than drifting.
func TestRenewalsFallOnThePlansOwnBoundaries(t *testing.T) {
	jan31 := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)
	day := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		cycle  model.ResetCycle
		anchor time.Time
		now    time.Time
		want   time.Time
		due    bool
	}{
		"daily, not a day yet":    {model.ResetDaily, day, day.Add(23 * time.Hour), time.Time{}, false},
		"daily, a day":            {model.ResetDaily, day, day.Add(24 * time.Hour), day.Add(24 * time.Hour), true},
		"daily, three days late":  {model.ResetDaily, day, day.Add(75 * time.Hour), day.Add(72 * time.Hour), true},
		"weekly, six days":        {model.ResetWeekly, day, day.AddDate(0, 0, 6), time.Time{}, false},
		"weekly, nine days":       {model.ResetWeekly, day, day.AddDate(0, 0, 9), day.AddDate(0, 0, 7), true},
		"monthly, a month":        {model.ResetMonthly, day, day.AddDate(0, 1, 0), day.AddDate(0, 1, 0), true},
		"monthly, the day before": {model.ResetMonthly, day, day.AddDate(0, 1, -1), time.Time{}, false},
		"monthly, after a long outage": {model.ResetMonthly, day, day.AddDate(0, 3, 5),
			day.AddDate(0, 3, 0), true},
		"monthly from the 31st": {model.ResetMonthly, jan31, time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
			jan31.AddDate(0, 1, 0), true},
		"never renews": {model.ResetNone, day, day.AddDate(1, 0, 0), time.Time{}, false},
	} {
		got, due := dueRenewal(c.cycle, c.anchor, c.now)
		if due != c.due || !got.Equal(c.want) {
			t.Errorf("%s: due=%v at %s, want due=%v at %s", name, due, got, c.due, c.want)
		}
	}
}

// A plan that renews gets its traffic back when its period comes round: used
// goes to zero and one cut off for running out is back on. Its allowance, its
// date and its switch are its own and stay. A plan whose period has not come,
// one that does not renew, one switched off and one still waiting for its
// first connection are each left as they are -- the switched-off one still
// renews, since what it used is the period's, but stays off.
func TestPlansRenewWhenTheirPeriodComes(t *testing.T) {
	db := newTestDB(t)
	r := &Reconciler{db: db, log: quietLog()}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	monthAgo := now.AddDate(0, -1, 0).Add(-time.Hour)
	hourAgo := now.Add(-time.Hour)
	ends := now.AddDate(0, 2, 0)

	mk := func(c model.Client) model.Client {
		t.Helper()
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		// CreatedAt is set by the database; the test's clock is not "now".
		if err := db.Model(&model.Client{}).Where("id = ?", c.ID).Update("created_at", c.CreatedAt).Error; err != nil {
			t.Fatal(err)
		}
		return c
	}
	out := mk(model.Client{Name: "out", Status: model.StatusExhausted, ResetCycle: model.ResetMonthly,
		QuotaBytes: 50 << 30, UsedBytes: 50 << 30, UpBytes: 10 << 30, DownBytes: 40 << 30, ExpiresAt: &ends, CreatedAt: monthAgo})
	running := mk(model.Client{Name: "running", Status: model.StatusActive, ResetCycle: model.ResetMonthly,
		QuotaBytes: 50 << 30, UsedBytes: 20 << 30, CreatedAt: monthAgo})
	notYet := mk(model.Client{Name: "not-yet", Status: model.StatusActive, ResetCycle: model.ResetMonthly,
		QuotaBytes: 50 << 30, UsedBytes: 20 << 30, CreatedAt: hourAgo})
	never := mk(model.Client{Name: "never", Status: model.StatusExhausted, ResetCycle: model.ResetNone,
		QuotaBytes: 50 << 30, UsedBytes: 50 << 30, CreatedAt: monthAgo})
	off := mk(model.Client{Name: "off", Status: model.StatusDisabled, ResetCycle: model.ResetMonthly,
		QuotaBytes: 50 << 30, UsedBytes: 30 << 30, CreatedAt: monthAgo})
	waiting := mk(model.Client{Name: "waiting", Status: model.StatusActive, ResetCycle: model.ResetDaily,
		StartOnFirstUse: true, DurationDays: 30, CreatedAt: monthAgo})

	n, err := r.renew(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("renewed %d plans, want 3: out, running and off", n)
	}

	get := func(c model.Client) model.Client {
		t.Helper()
		var got model.Client
		if err := db.First(&got, c.ID).Error; err != nil {
			t.Fatal(err)
		}
		return got
	}
	if g := get(out); g.UsedBytes != 0 || g.UpBytes != 0 || g.DownBytes != 0 || g.Status != model.StatusActive ||
		g.QuotaBytes != 50<<30 || g.ExpiresAt == nil || !g.ExpiresAt.Equal(ends) {
		t.Errorf("the plan that ran out: %+v", g)
	}
	if g := get(out); g.LastResetAt == nil || !g.LastResetAt.Equal(monthAgo.AddDate(0, 1, 0)) {
		t.Errorf("renewed at %v, want the boundary %s, not the moment it was noticed", g.LastResetAt, monthAgo.AddDate(0, 1, 0))
	}
	if g := get(running); g.UsedBytes != 0 || g.Status != model.StatusActive {
		t.Errorf("the running plan: used=%d status=%s", g.UsedBytes, g.Status)
	}
	if g := get(notYet); g.UsedBytes != 20<<30 || g.LastResetAt != nil {
		t.Errorf("a plan an hour into its month was renewed: %+v", g)
	}
	if g := get(never); g.UsedBytes != 50<<30 || g.Status != model.StatusExhausted {
		t.Errorf("a plan that does not renew was: %+v", g)
	}
	if g := get(off); g.UsedBytes != 0 || g.Status != model.StatusDisabled {
		t.Errorf("a switched-off plan: used=%d status=%s, want renewed and still off", g.UsedBytes, g.Status)
	}
	if g := get(waiting); g.LastResetAt != nil {
		t.Errorf("a plan waiting for its first connection was renewed: %+v", g)
	}

	// Asked again at once, nothing more is due.
	if n, err := r.renew(context.Background(), now); err != nil || n != 0 {
		t.Errorf("a second look renewed %d (%v), want none", n, err)
	}
	// A reset made by hand after the boundary is not undone by it.
	manual := now.Add(-10 * time.Minute)
	if err := db.Model(&model.Client{}).Where("id = ?", running.ID).
		Updates(map[string]any{"used_bytes": 5 << 30, "last_reset_at": manual}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.renew(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if g := get(running); g.UsedBytes != 5<<30 {
		t.Errorf("a renewal undid a reset made by hand: used=%d", g.UsedBytes)
	}
}
