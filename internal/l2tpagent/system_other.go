//go:build !linux

package l2tpagent

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// errPlatform is the answer everywhere but Linux: L2TP is served by pppd and
// the kernel's L2TP sockets, which only Linux has.
var errPlatform = errors.New("l2tpagent: L2TP is only served on Linux")

type otherSystem struct{}

// NewSystem is the system on this machine, which cannot serve L2TP.
func NewSystem(*slog.Logger) System { return otherSystem{} }

func (otherSystem) EnsurePackages(context.Context, string) error { return errPlatform }
func (otherSystem) WriteSecrets(string, *l2tpconf.State) error   { return errPlatform }
func (otherSystem) WriteRun(string, string) error                { return errPlatform }
func (otherSystem) RemoveRun(...string) error                    { return nil }
func (otherSystem) LoadIPsec(context.Context, string) error      { return errPlatform }
func (otherSystem) UnloadIPsec(context.Context) error            { return nil }
func (otherSystem) ApplyFirewall(context.Context, string) error  { return errPlatform }
func (otherSystem) Start(context.Context, string) error          { return errPlatform }
func (otherSystem) Stop(context.Context, string) error           { return nil }
func (otherSystem) Running(string) bool                          { return false }
func (otherSystem) Sessions(netip.Addr) ([]Live, error)          { return nil, errPlatform }
func (otherSystem) EndSession(int) error                         { return errPlatform }
