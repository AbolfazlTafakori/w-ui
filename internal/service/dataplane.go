package service

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The two controls an operator reaches for when something is wrong: bring
// every tunnel on this server back, and switch them all off (and on again).
//
// Restart reopens the drivers. It does not touch the database, so nobody's
// allowance, expiry or standing changes: a customer who was allowed on before
// is allowed on after, and the reconciler pushes them back within a tick.
//
// Stop switches every tunnel off, which is a decision about service rather
// than about a fault, so it is recorded as one: the tunnels are marked
// disabled and stay that way across a restart until somebody turns them back
// on. Killing the drivers and leaving the rows enabled would come back by
// itself on the next tick and look like the button had not worked.

// Tunnels is what runs this server's tunnels: the backend pool.
type Tunnels interface {
	Open(ctx context.Context, iface *model.Interface) error
	Close(id uint)
}

// DataPlane is every tunnel on this server at once.
type DataPlane struct {
	db      *gorm.DB
	tunnels Tunnels
	nodeID  uint
	log     *slog.Logger
}

// NewDataPlane builds it for the tunnels of this server's node.
func NewDataPlane(db *gorm.DB, tunnels Tunnels, localNodeID uint, log *slog.Logger) *DataPlane {
	return &DataPlane{db: db, tunnels: tunnels, nodeID: localNodeID, log: log}
}

// DataPlaneResult is what one of them did: how many tunnels it acted on,
// and the ones that would not come up, by name -- the whole content of the
// answer when a restart does not fix it.
type DataPlaneResult struct {
	Acted    int
	Failures map[string]error
}

func (d *DataPlane) local(ctx context.Context, enabled bool) ([]model.Interface, error) {
	var out []model.Interface
	err := d.db.WithContext(ctx).Where("enabled = ? AND node_id = ?", enabled, d.nodeID).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("service: list this server's tunnels: %w", err)
	}
	return out, nil
}

// RestartAll reopens every enabled tunnel on this server. One that will not
// come up does not stop the others.
func (d *DataPlane) RestartAll(ctx context.Context) (DataPlaneResult, error) {
	list, err := d.local(ctx, true)
	if err != nil {
		return DataPlaneResult{}, err
	}
	res := DataPlaneResult{Failures: map[string]error{}}
	for i := range list {
		iface := list[i]
		if err := d.tunnels.Open(ctx, &iface); err != nil {
			d.log.Warn("interface did not restart", "interface", iface.Name, "error", err)
			res.Failures[iface.Name] = err
			continue
		}
		res.Acted++
	}
	return res, nil
}

// StopAll switches every enabled tunnel on this server off, in the
// database first: a driver closed with its row still enabled is reopened by
// the reconciler within a tick.
func (d *DataPlane) StopAll(ctx context.Context) (DataPlaneResult, error) {
	list, err := d.local(ctx, true)
	if err != nil {
		return DataPlaneResult{}, err
	}
	ids := make([]uint, 0, len(list))
	for _, iface := range list {
		ids = append(ids, iface.ID)
	}
	if len(ids) > 0 {
		if err := d.db.WithContext(ctx).Model(&model.Interface{}).
			Where("id IN ?", ids).Update("enabled", false).Error; err != nil {
			return DataPlaneResult{}, fmt.Errorf("service: switch the tunnels off: %w", err)
		}
	}
	for _, id := range ids {
		d.tunnels.Close(id)
	}
	return DataPlaneResult{Acted: len(ids)}, nil
}

// StartAll switches every disabled tunnel on this server back on, because a
// stop nobody can undo from the same page is a stop nobody will press.
func (d *DataPlane) StartAll(ctx context.Context) (DataPlaneResult, error) {
	list, err := d.local(ctx, false)
	if err != nil {
		return DataPlaneResult{}, err
	}
	res := DataPlaneResult{Failures: map[string]error{}}
	for i := range list {
		iface := list[i]
		if err := d.db.WithContext(ctx).Model(&model.Interface{}).
			Where("id = ?", iface.ID).Update("enabled", true).Error; err != nil {
			res.Failures[iface.Name] = err
			continue
		}
		iface.Enabled = true
		if err := d.tunnels.Open(ctx, &iface); err != nil {
			d.log.Warn("interface did not start", "interface", iface.Name, "error", err)
			res.Failures[iface.Name] = err
			continue
		}
		res.Acted++
	}
	return res, nil
}
