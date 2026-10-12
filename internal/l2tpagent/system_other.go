//go:build !linux

package l2tpagent

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/occonf"
	"github.com/abolfazl/w-ui/internal/radiusd"
)

// errPlatform is the answer everywhere but Linux: L2TP is served by pppd and
// the kernel's L2TP sockets, which only Linux has.
var errPlatform = errors.New("l2tpagent: L2TP is only served on Linux")

type otherSystem struct{}

// NewSystem is the system on this machine, which cannot serve L2TP.
func NewSystem(*slog.Logger) System { return otherSystem{} }

// NewIPsecSystem is the same for IKEv2.
func NewIPsecSystem(*slog.Logger) IPsecSystem { return otherSystem{} }

func (otherSystem) WriteSwanctl([]ikev2conf.File) error      { return errPlatform }
func (otherSystem) LoadSwanctl(context.Context) error        { return errPlatform }
func (otherSystem) RemoveSwanctl(context.Context) error      { return nil }
func (otherSystem) SAs(context.Context) ([]IKESA, error)     { return nil, errPlatform }
func (otherSystem) Terminate(context.Context, uint) error    { return errPlatform }
func (otherSystem) ServeRadius(string, radiusd.Lookup) error { return errPlatform }
func (otherSystem) StopRadius()                              {}

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

// NewOCSystem is the same for OpenConnect.
func NewOCSystem(*slog.Logger) OCSystem { return otherSystem{} }

func (otherSystem) WriteOcserv([]occonf.File) (bool, bool, error)                { return false, false, errPlatform }
func (otherSystem) RadiusSecret() string                                         { return "" }
func (otherSystem) OcservRunning(context.Context) bool                           { return false }
func (otherSystem) StartOcserv(context.Context) error                            { return errPlatform }
func (otherSystem) RestartOcserv(context.Context) error                          { return errPlatform }
func (otherSystem) ReloadOcserv(context.Context) error                           { return errPlatform }
func (otherSystem) RemoveOcserv(context.Context) error                           { return nil }
func (otherSystem) OcservUsers(context.Context) ([]OCUser, error)                { return nil, errPlatform }
func (otherSystem) Disconnect(context.Context, int) error                        { return errPlatform }
func (otherSystem) Camouflage(context.Context) bool                              { return false }
func (otherSystem) ServeOCRadius(string, radiusd.Lookup, radiusd.OnAccept) error { return errPlatform }
func (otherSystem) StopOCRadius()                                                {}
