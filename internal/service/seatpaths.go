package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Every way of making, extending, growing or removing customers, each put
// through withSeats so its users are settled in its own transaction. The
// bodies are the *Raw methods beside these; a method that only hands each
// customer to another of these (a status-based delete to Bulk, a batch to
// Create) is not wrapped twice, so nothing is counted twice.

func (s *Clients) Create(ctx context.Context, in CreateInput) (*model.Client, error) {
	var out *model.Client
	err := s.withSeats(ctx, nil, false, func(v *Clients) ([]uint, error) {
		c, err := v.createRaw(ctx, in)
		if err != nil {
			return nil, err
		}
		out = c
		return []uint{c.ID}, nil
	})
	return out, err
}

// CreateBatch makes every customer or none: each is made, and settled, by
// Create inside the one transaction, and the first refused undoes the rest.
func (s *Clients) CreateBatch(ctx context.Context, in BatchInput) ([]model.Client, error) {
	var out []model.Client
	err := s.withSeats(ctx, nil, false, func(v *Clients) ([]uint, error) {
		var err error
		out, err = v.createBatchRaw(ctx, in)
		return nil, err
	})
	return out, err
}

func (s *Clients) Update(ctx context.Context, id uint, in UpdateInput) (*model.Client, error) {
	var out *model.Client
	err := s.withSeats(ctx, []uint{id}, false, func(v *Clients) ([]uint, error) {
		var err error
		out, err = v.updateRaw(ctx, id, in)
		return nil, err
	})
	return out, err
}

func (s *Clients) ResetTraffic(ctx context.Context, id uint) (*model.Client, error) {
	var out *model.Client
	err := s.withSeats(ctx, []uint{id}, false, func(v *Clients) ([]uint, error) {
		var err error
		out, err = v.resetTrafficRaw(ctx, id)
		return nil, err
	})
	return out, err
}

func (s *Clients) Delete(ctx context.Context, id uint) error {
	return s.withSeats(ctx, []uint{id}, false, func(v *Clients) ([]uint, error) {
		return nil, v.deleteRaw(ctx, id)
	})
}

func (s *Clients) Bulk(ctx context.Context, action BulkAction, ids []uint) (int64, error) {
	var n int64
	err := s.withSeats(ctx, ids, false, func(v *Clients) ([]uint, error) {
		var err error
		n, err = v.bulkRaw(ctx, action, ids)
		return nil, err
	})
	return n, err
}

func (s *Clients) Adjust(ctx context.Context, in AdjustInput) (int64, error) {
	var n int64
	err := s.withSeats(ctx, in.IDs, false, func(v *Clients) ([]uint, error) {
		var err error
		n, err = v.adjustRaw(ctx, in)
		return nil, err
	})
	return n, err
}

func (s *Clients) Extend(ctx context.Context, in ExtendInput) (*ExtendResult, error) {
	if in.DryRun {
		return s.previewExtend(ctx, in)
	}
	var out *ExtendResult
	err := s.withSeats(ctx, in.IDs, false, func(v *Clients) ([]uint, error) {
		var err error
		out, err = v.extendRaw(ctx, in)
		return nil, err
	})
	return out, err
}

// ResetAllTraffic starts every customer of the caller's again: an extension
// of each, settled together, all or none.
func (s *Clients) ResetAllTraffic(ctx context.Context) (int64, error) {
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.Client{}).Pluck("id", &ids).Error; err != nil {
		return 0, fmt.Errorf("service: which customers to reset: %w", err)
	}
	var n int64
	err := s.withSeats(ctx, ids, false, func(v *Clients) ([]uint, error) {
		var err error
		n, err = v.resetAllTrafficRaw(ctx)
		return nil, err
	})
	return n, err
}

// ApplyToGroup settles every member of the group together. Its delete goes
// to bulkRaw, inside this same settling.
func (s *Clients) ApplyToGroup(ctx context.Context, op GroupOp) (int64, error) {
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("id IN (SELECT client_id FROM client_groups WHERE name = ?)", op.Group).
		Pluck("id", &ids).Error; err != nil {
		return 0, fmt.Errorf("service: the group's customers: %w", err)
	}
	var n int64
	err := s.withSeats(ctx, ids, false, func(v *Clients) ([]uint, error) {
		var err error
		n, err = v.applyToGroupRaw(ctx, op)
		return nil, err
	})
	return n, err
}

// Import settles the customers it replaces here; the ones it makes are
// made, and settled, by Create inside the same transaction.
func (s *Clients) Import(ctx context.Context, in ImportInput) (*ImportReport, error) {
	names := make([]string, 0, len(in.Clients))
	for _, c := range in.Clients {
		if n := strings.TrimSpace(c.Name); n != "" {
			names = append(names, n)
		}
	}
	var existing []uint
	for start := 0; start < len(names); start += 900 {
		var part []uint
		if err := s.db.WithContext(ctx).Model(&model.Client{}).Where("name IN ?", names[start:min(start+900, len(names))]).
			Pluck("id", &part).Error; err != nil {
			return nil, fmt.Errorf("service: the customers an import replaces: %w", err)
		}
		existing = append(existing, part...)
	}
	var out *ImportReport
	err := s.withSeats(ctx, existing, false, func(v *Clients) ([]uint, error) {
		var err error
		out, err = v.importRaw(ctx, in)
		return nil, err
	})
	return out, err
}

// previewExtend is what an extension would do, and what it would take from
// the caller's users, before they confirm it.
//
// Run for real -- the same change and the same settling as the extension
// itself -- inside a transaction that is always rolled back, so the units
// shown are the units it takes, not a second sum that could disagree with
// the first. A change that would be refused for want of users is refused
// here too, with the same sentence.
func (s *Clients) previewExtend(ctx context.Context, in ExtendInput) (*ExtendResult, error) {
	sc := ScopeOf(ctx)
	run := in
	run.DryRun, run.measuring = false, true
	var out *ExtendResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		before, err := callerUsers(tx, sc)
		if err != nil {
			return err
		}
		view := s.seatsView(tx, &ipJournal{})
		view.changes = nil
		if out, err = view.Extend(ctx, run); err != nil {
			return err
		}
		after, err := callerUsers(tx, sc)
		if err != nil {
			return err
		}
		out.Units = after - before
		return errDryRun
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return nil, err
	}
	return out, nil
}

// callerUsers is how much of the caller's users is in use, or 0 for one
// with no ceiling.
func callerUsers(tx *gorm.DB, sc Scope) (int64, error) {
	if !sc.Capped {
		return 0, nil
	}
	acc, err := capacity.Account(tx, sc.AdminID, model.CapacityUsers)
	if err != nil || acc == nil {
		return 0, err
	}
	return acc.Used, nil
}
