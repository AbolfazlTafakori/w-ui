package service

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Working on several addresses at once, and putting them in order.
//
// A tunnel's hosts are the spares: the addresses a customer's app tries when
// the first one stops answering. They are added in a hurry, when something is
// already blocked, and they are edited in a hurry for the same reason — an
// operator who has just been given four new addresses should not have to open
// four dialogs, and one who has worked out which of them is fastest should be
// able to move it to the front without doing arithmetic on priority numbers.

// HostBulkAction is what to do with the selected addresses.
type HostBulkAction string

const (
	HostEnable  HostBulkAction = "enable"
	HostDisable HostBulkAction = "disable"
	HostDelete  HostBulkAction = "delete"
)

// Bulk applies an action to several hosts and reports how many changed.
func (s *Hosts) Bulk(ctx context.Context, action HostBulkAction, ids []uint) (int64, error) {
	ids = dedupe(ids)
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: no addresses selected", ErrInvalid)
	}

	db := s.db.WithContext(ctx)
	var res *gorm.DB

	switch action {
	case HostEnable, HostDisable:
		res = db.Model(&model.Host{}).Where("id IN ?", ids).
			Updates(map[string]any{
				"enabled":    action == HostEnable,
				"updated_at": time.Now().UTC(),
			})
	case HostDelete:
		res = db.Where("id IN ?", ids).Delete(&model.Host{})
	default:
		return 0, fmt.Errorf("%w: %q is not something that can be done to an address",
			ErrInvalid, action)
	}

	if res.Error != nil {
		return 0, fmt.Errorf("service: %s hosts: %w", action, res.Error)
	}
	s.log.Info("addresses changed in bulk", "action", action, "count", res.RowsAffected)
	return res.RowsAffected, nil
}

// Reorder writes the priority of every listed host from its position.
//
// The order is sent whole rather than as "move this one up", because two
// requests that each nudge one row race each other into an order neither
// operator asked for. The list the page is showing is the intended answer, and
// storing exactly that cannot be half applied.
func (s *Hosts) Reorder(ctx context.Context, ids []uint) (int, error) {
	ids = dedupe(ids)
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: no order was given", ErrInvalid)
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			// Numbered from one and left with no gaps, so the next insert has
			// somewhere obvious to go and the numbers stay readable.
			if err := tx.Model(&model.Host{}).Where("id = ?", id).
				Updates(map[string]any{
					"priority":   i + 1,
					"updated_at": time.Now().UTC(),
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("service: reorder hosts: %w", err)
	}

	s.log.Info("addresses reordered", "count", len(ids))
	return len(ids), nil
}
