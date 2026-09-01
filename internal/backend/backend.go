// Package backend defines the contract every VPN protocol driver implements.
//
// The panel above this layer is protocol-agnostic: quotas, expiry, the device
// limit, traffic history and the admin API are written once and work for any
// backend. A driver's job is narrow — translate a desired set of accounts into
// live server state, report what it sees, and render a client profile.
//
// Byte accounting and quota cutoff are deliberately NOT part of this interface.
// Both protocols give an account an address inside the tunnel subnet, so
// enforcement is done once against those addresses by package enforce, below
// this layer rather than inside each driver.
package backend

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Errors a driver may return.
var (
	ErrNotSupported  = errors.New("backend: operation not supported by this protocol")
	ErrNotOpen       = errors.New("backend: interface not open")
	ErrUnknownAcct   = errors.New("backend: unknown account")
	ErrWrongProtocol = errors.New("backend: interface protocol does not match driver")
)

// DesiredAccount is one account as the panel wants it to exist on the server.
// It is a flat projection of model.Account so drivers do not reach into the
// database layer.
type DesiredAccount struct {
	ID         uint
	DeviceName string
	IP         netip.Addr

	// WireGuard.
	PublicKey    string
	PresharedKey string

	// OpenVPN.
	Username string
	Secret   string
}

// Stat is what a driver observes for one account since the last call.
//
// RX and TX are raw counters as the data plane reports them, not deltas. They
// reset whenever the interface restarts or a peer is re-added, so the caller
// treats a value lower than the previous reading as a reset and takes the new
// value as the delta.
type Stat struct {
	AccountID     uint
	RX            uint64
	TX            uint64
	LastHandshake time.Time
	// Endpoint is the account's public source address. A single account whose
	// endpoint hops between several addresses in a short window is being shared
	// across devices.
	Endpoint string
}

// Online reports whether the account handshook recently enough to be considered
// connected. WireGuard has no session concept, so presence is inferred from the
// last handshake; three minutes is the interval after which the protocol itself
// considers a session stale.
func (s Stat) Online(now time.Time) bool {
	return !s.LastHandshake.IsZero() && now.Sub(s.LastHandshake) < 3*time.Minute
}

// SyncReport summarises what a Sync call changed.
type SyncReport struct {
	Added     int
	Removed   int
	Updated   int
	Unchanged int
}

// Changed reports whether the server state was modified at all.
func (r SyncReport) Changed() bool { return r.Added+r.Removed+r.Updated > 0 }

// ClientProfile is a rendered configuration ready to hand to a customer.
type ClientProfile struct {
	Filename string
	MIMEType string
	Body     []byte
}

// Backend drives one protocol on one interface.
//
// Implementations must be safe for concurrent use and every method must be
// idempotent: the reconciler calls Sync with the full desired set on every
// tick, and relies on repeated identical calls being free.
type Backend interface {
	// Protocol reports which protocol this driver serves.
	Protocol() model.Protocol

	// Open binds the driver to an interface and brings it up if needed.
	Open(ctx context.Context, iface *model.Interface) error

	// Sync reconciles live server state with desired, adding, updating and
	// removing accounts so that afterwards the server holds exactly desired.
	//
	// Accounts absent from desired are removed. Removal is how a customer is
	// cut off, and it takes effect immediately without disturbing anyone else:
	// neither protocol needs a restart to change its account set.
	Sync(ctx context.Context, desired []DesiredAccount) (SyncReport, error)

	// Stats reports current counters for every account the driver knows about.
	Stats(ctx context.Context) ([]Stat, error)

	// Kick terminates an account's active session. OpenVPN can drop a session
	// outright; WireGuard is connectionless and returns ErrNotSupported, where
	// removing the account through Sync is the equivalent action.
	Kick(ctx context.Context, accountID uint) error

	// Render produces the client profile for an account.
	Render(ctx context.Context, acc *model.Account, iface *model.Interface) (ClientProfile, error)

	// Health reports whether the underlying data plane is reachable.
	Health(ctx context.Context) error

	// Close releases driver resources. It does not tear down the interface.
	Close() error
}
