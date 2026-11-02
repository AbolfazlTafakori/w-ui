package backend

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

const downProto = model.Protocol("downtest")

// downDriver is an open driver that records the teardown calls made on it, so a
// test can tell Close (release the handle, leave the device up) from Destroy
// (the device actually goes).
type downDriver struct {
	closes, destroys int
}

func (d *downDriver) Protocol() model.Protocol                     { return downProto }
func (d *downDriver) Open(context.Context, *model.Interface) error { return nil }
func (d *downDriver) Sync(context.Context, []DesiredAccount) (SyncReport, error) {
	return SyncReport{}, nil
}
func (d *downDriver) Stats(context.Context) ([]Stat, error) { return nil, nil }
func (d *downDriver) Kick(context.Context, uint) error      { return ErrNotSupported }
func (d *downDriver) Render(context.Context, *model.Account, *model.Interface) (ClientProfile, error) {
	return ClientProfile{}, nil
}
func (d *downDriver) Health(context.Context) error  { return nil }
func (d *downDriver) Close() error                  { d.closes++; return nil }
func (d *downDriver) Destroy(context.Context) error { d.destroys++; return nil }

// A disabled interface has its device taken down -- the driver torn down and the
// device removed by name -- but is not marked destroyed, so enabling it again
// brings it back on the very next tick rather than waiting out the window that
// keeps a just-deleted interface from being reopened.
func TestDownTakesTheDeviceDownWithoutBlockingAReEnable(t *testing.T) {
	p := NewPool(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var removed int
	RegisterRemover(downProto, func(context.Context, *model.Interface) error {
		removed++
		return nil
	})

	iface := model.Interface{ID: 3, Name: "wg9", Protocol: downProto, Enabled: true,
		CreatedAt: time.Now().Add(-time.Hour)}
	drv := &downDriver{}
	p.Put(&iface, drv)

	p.Down(context.Background(), &iface)

	if _, open := p.Get(3); open {
		t.Fatal("Down left the driver in the pool")
	}
	if drv.destroys == 0 {
		t.Fatal("Down only released the handle; the device was left up")
	}
	if drv.closes == 0 {
		t.Error("Down did not close the driver")
	}
	if removed == 0 {
		t.Error("Down did not remove the device by name")
	}
	if len(p.destroyed) != 0 {
		t.Fatal("Down marked the interface destroyed; a re-enable would be made to wait")
	}

	// Enabled again: the next sync tries to bring it back at once. (The open
	// fails because this protocol has no registered driver, but what matters is
	// that the attempt was made -- a deleted interface would have been skipped.)
	p.Sync(context.Background(), []model.Interface{iface})
	if _, tried := p.open[3]; !tried {
		t.Fatal("a re-enabled interface was not brought back on the next tick")
	}
}
