package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Renewing plans that are sold by the period.
//
// A customer whose plan renews daily, weekly or monthly gets their traffic
// back at the start of each period: what they used goes to zero, and one who
// had been cut off for running out is back at once. Their allowance, their end
// date and their switch are theirs and are not touched -- a plan that renews
// monthly and ends in three months renews twice and then ends.
//
// The periods are counted from the last renewal, or, before the first, from
// when the plan started: its first connection for a plan that waits for one,
// its creation otherwise. Each renewal is written as the boundary it fell on,
// not as the moment the panel noticed, so a plan renewed a minute late, or
// after the panel was down for a week, keeps its day of the month rather than
// drifting a little later every time.

// renewEvery is how often the panel looks for plans due a renewal. A
// minute late is nothing on a plan counted in days.
const renewEvery = time.Minute

// period is one step of a renewal cycle from t; ok is false for a plan that
// does not renew.
func period(cycle model.ResetCycle, t time.Time) (time.Time, bool) {
	switch cycle {
	case model.ResetDaily:
		return t.AddDate(0, 0, 1), true
	case model.ResetWeekly:
		return t.AddDate(0, 0, 7), true
	case model.ResetMonthly:
		return t.AddDate(0, 1, 0), true
	}
	return time.Time{}, false
}

// dueRenewal is the last renewal boundary at or before now for a plan counted
// from anchor, and whether one has passed since anchor at all.
func dueRenewal(cycle model.ResetCycle, anchor, now time.Time) (time.Time, bool) {
	next, ok := period(cycle, anchor)
	if !ok || next.After(now) {
		return time.Time{}, false
	}
	last := next
	for {
		after, _ := period(cycle, last)
		if after.After(now) {
			return last, true
		}
		last = after
	}
}

// maybeRenew renews the plans that are due, on a slow schedule of its own.
func (r *Reconciler) maybeRenew(ctx context.Context, now time.Time) {
	r.mu.Lock()
	due := now.Sub(r.lastRenew) >= renewEvery
	if due {
		r.lastRenew = now
	}
	r.mu.Unlock()
	if !due {
		return
	}
	if _, err := r.renew(ctx, now); err != nil {
		r.log.Warn("could not renew plans", "error", err)
	}
}

// renew gives every plan that is due a renewal its traffic back, and returns
// how many it renewed.
func (r *Reconciler) renew(ctx context.Context, now time.Time) (int, error) {
	db := r.db.WithContext(ctx)
	var plans []struct {
		ID              uint
		Name            string
		Status          model.ClientStatus
		ResetCycle      model.ResetCycle
		LastResetAt     *time.Time
		ActivatedAt     *time.Time
		StartOnFirstUse bool
		CreatedAt       time.Time
	}
	err := db.Model(&model.Client{}).
		Select("id, name, status, reset_cycle, last_reset_at, activated_at, start_on_first_use, created_at").
		Where("reset_cycle IN ?", []model.ResetCycle{model.ResetDaily, model.ResetWeekly, model.ResetMonthly}).
		Scan(&plans).Error
	if err != nil {
		return 0, fmt.Errorf("read the plans that renew: %w", err)
	}

	var renewed []string
	for _, p := range plans {
		// A plan still waiting for its first connection has not begun, and
		// its periods are counted from that connection, not from its sale.
		if p.StartOnFirstUse && p.ActivatedAt == nil {
			continue
		}
		anchor := p.CreatedAt
		switch {
		case p.LastResetAt != nil:
			anchor = *p.LastResetAt
		case p.ActivatedAt != nil:
			anchor = *p.ActivatedAt
		}
		at, ok := dueRenewal(p.ResetCycle, anchor.UTC(), now)
		if !ok {
			continue
		}
		fields := map[string]any{
			"used_bytes":    0,
			"up_bytes":      0,
			"down_bytes":    0,
			"last_reset_at": at,
		}
		if p.Status == model.StatusExhausted {
			fields["status"] = model.StatusActive
		}
		// Conditional on the renewal it was read for, so a reset made by
		// hand in between is not undone by one made on its old date.
		res := db.Model(&model.Client{}).
			Where("id = ? AND (last_reset_at IS NULL OR last_reset_at < ?)", p.ID, at).
			Updates(fields)
		if res.Error != nil {
			return len(renewed), fmt.Errorf("renew %s: %w", p.Name, res.Error)
		}
		if res.RowsAffected > 0 {
			if err := service.ZeroFileUsage(db, "id = ?", p.ID); err != nil {
				return len(renewed), fmt.Errorf("renew %s: %w", p.Name, err)
			}
			renewed = append(renewed, p.Name)
		}
	}
	if len(renewed) > 0 {
		r.log.Info("plans renewed: traffic back to zero", "count", len(renewed), "clients", strings.Join(renewed, ", "))
	}
	return len(renewed), nil
}
