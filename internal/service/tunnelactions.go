package service

import (
	"context"
	"fmt"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The two things done to a whole tunnel rather than to one customer.
//
// Both exist because the alternative is selecting the right subset of a
// customer list by hand and hoping none was missed — and the cost of missing
// one is either a customer billed for traffic on a server that was reset around
// them, or a customer left on a tunnel that is being taken out of service.

// ResetTunnelUsage sets the usage of every customer on a tunnel back to zero.
//
// The customers themselves are untouched: their allowance, their date and their
// standing stay exactly as they were. This is for a tunnel that was carrying
// traffic nobody should have been charged for — a test, a misconfiguration, a
// month that is being written off.
func (s *Interfaces) ResetTunnelUsage(ctx context.Context, id uint) (int64, error) {
	var iface model.Interface
	if err := s.db.WithContext(ctx).First(&iface, id).Error; err != nil {
		return 0, fmt.Errorf("%w: no interface %d", ErrNotFound, id)
	}

	// Every customer with a device on this tunnel, and no one else. A customer
	// on three tunnels has one total, so resetting here resets all of it — said
	// plainly on the page rather than discovered afterwards.
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.Account{}).
		Where("interface_id = ?", id).Distinct().Pluck("client_id", &ids).Error; err != nil {
		return 0, fmt.Errorf("service: find customers on %s: %w", iface.Name, err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	res := s.db.WithContext(ctx).Model(&model.Client{}).Where("id IN ?", ids).
		Updates(map[string]any{
			"used_bytes": 0,
			"up_bytes":   0,
			"down_bytes": 0,
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return 0, fmt.Errorf("service: reset usage on %s: %w", iface.Name, res.Error)
	}

	s.log.Warn("usage reset for everyone on a tunnel",
		"interface", iface.Name, "customers", res.RowsAffected)
	return res.RowsAffected, nil
}

// CustomersOn lists the customers with a device on a tunnel.
//
// Used to take them all off it before it is removed, which is the one thing
// that has to happen first and the one thing easiest to leave half done.
func (s *Interfaces) CustomersOn(ctx context.Context, id uint) ([]uint, error) {
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.Account{}).
		Where("interface_id = ?", id).Distinct().Pluck("client_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("service: find customers on interface %d: %w", id, err)
	}
	return ids, nil
}
