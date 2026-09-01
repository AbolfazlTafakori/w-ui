package reconciler

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// Starting a plan when the customer first uses it.
//
// A reseller hands out ten configurations on Monday. Without this they all
// expire on the same Wednesday, whether or not anyone connected — so a customer
// who travelled for a week has paid for a week they could not use, and the
// reseller hears about it.
//
// A client marked StartOnFirstUse carries a duration instead of an expiry date.
// The first handshake turns that duration into a real date, once.

// activate starts the clock for clients that have just been used.
//
// It is driven from the accounts' handshake times rather than from a separate
// event, because that is the one signal both protocols already produce and it
// survives a panel restart: the handshake is stored, so a client whose first
// connection happened while the panel was down is still activated on the next
// tick rather than never.
func (r *Reconciler) activate(ctx context.Context, now time.Time) (int64, error) {
	db := r.db.WithContext(ctx)

	// Which waiting clients have a device that has ever handshaken.
	var pending []model.Client
	err := db.Where(`start_on_first_use = ?
		         AND activated_at IS NULL
		         AND duration_days > 0
		         AND id IN (SELECT client_id FROM accounts WHERE last_handshake IS NOT NULL)`,
		true).Find(&pending).Error
	if err != nil {
		return 0, fmt.Errorf("find clients to activate: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	var started int64
	var names []string
	for _, c := range pending {
		expires := now.AddDate(0, 0, c.DurationDays)
		res := db.Model(&model.Client{}).
			// Guarded on activated_at still being null, so two ticks racing
			// cannot move an expiry date that has already been set.
			Where("id = ? AND activated_at IS NULL", c.ID).
			Updates(map[string]any{
				"activated_at": now,
				"expires_at":   expires,
			})
		if res.Error != nil {
			return started, fmt.Errorf("activate client %d: %w", c.ID, res.Error)
		}
		if res.RowsAffected == 0 {
			continue
		}
		started += res.RowsAffected
		if len(names) < 20 {
			names = append(names, c.Name)
		}
	}

	if started > 0 {
		r.log.Info("clients started their plan on first use", "count", started)
		r.announce(notify.KindPanel, "Plan started", names,
			"connected for the first time; their time now runs")
	}
	return started, nil
}

// PendingActivation counts clients whose plan has not started yet, for the
// clients page to label them rather than showing a blank expiry.
func PendingActivation(ctx context.Context, db *gorm.DB) (int64, error) {
	var n int64
	err := db.WithContext(ctx).Model(&model.Client{}).
		Where("start_on_first_use = ? AND activated_at IS NULL", true).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count pending activations: %w", err)
	}
	return n, nil
}
