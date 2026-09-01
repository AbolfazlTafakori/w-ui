//go:build !linux

package ovpndriver

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
)

// DataRoot is where interface directories would be created. It exists on this
// platform only so that configuration shared with the Linux build compiles.
var DataRoot = "/var/lib/wui"

// Driver is the non-Linux stand-in.
//
// The server process, its management socket and the tun device it needs are all
// Linux-specific here. Rendering a client profile still works, so configuration
// output can be inspected anywhere; anything that would touch a running server
// says plainly that it cannot.
type Driver struct {
	log   *slog.Logger
	iface *model.Interface
}

// New builds the inert driver for this platform.
func New() *Driver { return &Driver{log: slog.Default()} }

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return model.ProtocolOpenVPN }

func (d *Driver) Open(_ context.Context, iface *model.Interface) error {
	d.iface = iface
	return unsupported()
}

func (d *Driver) Sync(context.Context, []backend.DesiredAccount) (backend.SyncReport, error) {
	return backend.SyncReport{}, unsupported()
}

func (d *Driver) Stats(context.Context) ([]backend.Stat, error) { return nil, unsupported() }
func (d *Driver) Kick(context.Context, uint) error              { return unsupported() }
func (d *Driver) Health(context.Context) error                  { return unsupported() }
func (d *Driver) Close() error                                  { return nil }

// Render works everywhere: it is text generation, not process management.
func (d *Driver) Render(_ context.Context, acc *model.Account, iface *model.Interface) (backend.ClientProfile, error) {
	return backend.ClientProfile{
		Filename: acc.DeviceName + ".ovpn",
		MIMEType: "application/x-openvpn-profile",
		Body:     []byte(ovpnconf.RenderClient(acc, iface)),
	}, nil
}

func unsupported() error {
	return fmt.Errorf("%w (running on %s)", ErrUnsupported, runtime.GOOS)
}
