//go:build !linux

package routing

import (
	"context"
	"log/slog"
	"time"
)

// HopSpec is an upstream WireGuard tunnel this server dials.
type HopSpec struct {
	Device       string
	Mark         uint32
	PrivateKey   string
	PeerPubKey   string
	PresharedKey string
	Endpoint     string
	Address      string
	MTU          int
	AllowedIPs   []string
	Keepalive    int
	Kind         string
	Config       string
	Username     string
	Password     string
	SocksPort    int
	TunAddress   string
}

// HopWorkDir is where process-backed hops would keep their files.
var HopWorkDir = ""

// HopStatus is what an operator is shown about a process-backed hop.
type HopStatus struct {
	Device string
	Kind   string
	Alive  bool
	Since  time.Time
	Last   string
}

// Statuses reports nothing: no hop runs here.
func (m *HopManager) Statuses() []HopStatus { return nil }

// HopManager is the non-Linux stand-in. WireGuard interfaces are a Linux
// kernel feature, so elsewhere there is nothing to bring up.
type HopManager struct{ log *slog.Logger }

// NewHopManager builds the inert manager for this platform.
func NewHopManager(log *slog.Logger) *HopManager { return &HopManager{log: log} }

func (m *HopManager) Sync(context.Context, []HopSpec) error { return nil }
func (m *HopManager) Teardown(context.Context)              {}
