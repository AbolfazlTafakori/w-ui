package service

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A server's own transfer allowance, which is a different thing from a
// customer's.
//
// A host sells a machine with a monthly cap and either bills for the overage or
// cuts the port to a crawl. Neither is something an operator wants to discover
// from an invoice, and neither is visible from the customer side of the panel:
// a hundred customers well inside their own allowances can still take a server
// past what its host allows.
//
// Counted in what actually crossed the wire, before the node's usage
// coefficient. The coefficient is a price; the host bills bytes.

// RecordNodeTraffic adds what a node carried and rolls the month over when due.
//
// One statement, and the addition is done in SQL rather than read-modify-write,
// because the local reconciler and the node syncer both call this and a lost
// update here is a server quietly running past its cap.
func RecordNodeTraffic(ctx context.Context, db *gorm.DB, nodeID uint, raw uint64, now time.Time) error {
	if nodeID == 0 {
		return nil
	}

	var node model.Node
	if err := db.WithContext(ctx).First(&node, nodeID).Error; err != nil {
		// A node deleted while a round was in flight is not an error worth
		// failing a tick for.
		return nil
	}

	updates := map[string]any{}
	if resetDue(node.ResetDay, node.UsageResetAt, now) {
		// The rollover lands before this tick's bytes, so traffic that arrived
		// after the boundary is counted against the new month rather than
		// discarded with the old one.
		updates["used_bytes"] = raw
		updates["usage_reset_at"] = now
	} else if raw > 0 {
		updates["used_bytes"] = gorm.Expr("used_bytes + ?", raw)
	}
	if len(updates) == 0 {
		return nil
	}

	if err := db.WithContext(ctx).Model(&model.Node{}).
		Where("id = ?", nodeID).Updates(updates).Error; err != nil {
		return fmt.Errorf("service: record node traffic: %w", err)
	}
	return nil
}

// resetDue reports whether the host's allowance has started again since the
// counter was last cleared.
//
// Day zero means a counter only an operator clears, which is what a machine on
// an unmetered line wants. A counter that has never been cleared is due now, so
// that the first tick after the setting is turned on establishes the boundary
// rather than leaving one that never arrives.
func resetDue(day int, last *time.Time, now time.Time) bool {
	if day <= 0 {
		return false
	}
	if last == nil {
		return true
	}
	boundary := lastBoundary(day, now)
	return last.Before(boundary)
}

// lastBoundary is the most recent arrival of the reset day, at or before now.
func lastBoundary(day int, now time.Time) time.Time {
	now = now.UTC()
	this := time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.UTC)
	if !this.After(now) {
		return this
	}
	return this.AddDate(0, -1, 0)
}
