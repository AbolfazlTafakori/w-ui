//go:build !linux

package wgdriver

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/wgconf"
)

// Driver is the non-Linux stand-in.
//
// WireGuard's kernel implementation and the netlink API it is configured
// through are Linux-only. Rendering a client profile still works, so the panel
// can be developed and its configuration output inspected anywhere; anything
// that would touch an interface says plainly that it cannot.
type Driver struct {
	log   *slog.Logger
	iface *model.Interface
}

// New builds the inert driver for this platform.
func New() *Driver { return &Driver{log: slog.Default()} }

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return model.ProtocolWireGuard }

func (d *Driver) Open(_ context.Context, iface *model.Interface) error {
	d.iface = iface
	return unsupported()
}

func (d *Driver) Sync(context.Context, []backend.DesiredAccount) (backend.SyncReport, error) {
	return backend.SyncReport{}, unsupported()
}

func (d *Driver) Stats(context.Context) ([]backend.Stat, error) { return nil, unsupported() }
func (d *Driver) Kick(context.Context, uint) error              { return backend.ErrNotSupported }
func (d *Driver) Health(context.Context) error                  { return unsupported() }
func (d *Driver) Close() error                                  { return nil }

// Render works everywhere: it is text generation, not kernel work.
func (d *Driver) Render(_ context.Context, acc *model.Account, iface *model.Interface) (backend.ClientProfile, error) {
	return backend.ClientProfile{
		Filename: acc.DeviceName + ".conf",
		MIMEType: "text/plain; charset=utf-8",
		Body:     []byte(wgconf.RenderClient(acc, iface)),
	}, nil
}

func unsupported() error {
	return fmt.Errorf("%w (running on %s)", ErrUnsupported, runtime.GOOS)
}
