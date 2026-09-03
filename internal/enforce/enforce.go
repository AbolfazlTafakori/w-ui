// Package enforce defines the kernel-side enforcement contract.
//
// This layer sits below the protocol drivers, not inside them. Both WireGuard
// and OpenVPN give an account an address inside the tunnel subnet, so a rule
// keyed on that address covers either protocol and is written once.
//
// It exists because polling cannot enforce a quota. A collector reading
// counters every two seconds lets a customer on a 100 Mbit link overshoot by
// 25 MB, and one on a gigabit link by 250 MB; shortening the interval shrinks
// the window without closing it. Enforcement therefore happens in the data path
// — the kernel drops the packet that crosses the limit — and the panel only
// programs the rules and reads back what happened. The overshoot becomes one
// packet.
//
// The nftables implementation lands in phase 2. Two stateful objects per
// client carry the two jobs, which must not be conflated:
//
//	quota   cumulative, seeded from the database at boot, never reset except on
//	        renewal. This is what stops traffic.
//	counter drained atomically on every collection tick and folded into the
//	        traffic history. Read-and-zero in one operation removes the need for
//	        delta arithmetic and reset detection.
//
// Rules are reached through a verdict map keyed on the tunnel address, so
// lookup cost is a hash probe and does not grow with the number of customers.
package enforce

import (
	"context"
	"errors"
	"net/netip"
)

// Errors returned by an Enforcer.
var (
	ErrUnavailable = errors.New("enforce: enforcement backend unavailable")
	ErrUnknownKey  = errors.New("enforce: unknown rule key")
	ErrInvalidRule = errors.New("enforce: invalid rule")
	// ErrDegraded means enforcement runs but not at full precision.
	ErrDegraded = errors.New("enforce: reduced enforcement")
)

// Rule is the desired kernel-side policy for one client.
type Rule struct {
	// Key is a stable identifier derived from the client, used to name
	// the kernel objects. It must survive restarts.
	Key string

	// Addrs are the tunnel addresses covered by this rule — one per device.
	// Traffic in both directions across all of them counts against one quota,
	// which is what makes the limit apply to the client rather than to
	// each device separately.
	Addrs []netip.Addr

	// QuotaBytes is the hard limit. Zero means unlimited.
	QuotaBytes uint64

	// UsedBytes seeds the kernel quota from stored usage so a reboot does not
	// hand the customer their allowance back.
	UsedBytes uint64

	// RateBitsPerSec throttles the client. Zero means unmetered.
	RateBitsPerSec uint64

	// Blocked drops all traffic regardless of quota, for a client an
	// admin switched off.
	Blocked bool
}

// Unlimited reports whether the rule imposes no volume limit.
func (r Rule) Unlimited() bool { return r.QuotaBytes == 0 }

// Usage is the byte count observed for one rule.
type Usage struct {
	Key string

	// Bytes is the total, which is what an allowance is spent from.
	Bytes uint64

	// Up and Down split that total by direction, when whatever produced this
	// could tell them apart. They sum to Bytes when they are set at all; an
	// implementation with no directional counters leaves them zero and only
	// the total is recorded.
	Up   uint64
	Down uint64
}

// Enforcer programs and reads the kernel-side policy.
//
// Apply is declarative and takes the complete desired set: rules present in the
// kernel but absent from the argument are removed. Implementations must be safe
// for concurrent use.
type Enforcer interface {
	// Apply makes the kernel state match rules exactly. It must be atomic:
	// a partially applied ruleset would leave some customers unmetered.
	Apply(ctx context.Context, rules []Rule) error

	// Usage reports cumulative bytes charged against each quota, without
	// resetting anything.
	Usage(ctx context.Context) ([]Usage, error)

	// DrainCounters reads and zeroes the reporting counters in one operation
	// and returns what they held. The atomicity is the point: bytes that
	// arrive between the read and the reset would otherwise be lost.
	DrainCounters(ctx context.Context) ([]Usage, error)

	// ResetQuota clears the cumulative quota for the given keys, used on
	// renewal.
	ResetQuota(ctx context.Context, keys []string) error

	// Health reports whether the enforcement backend is usable.
	Health(ctx context.Context) error

	// Close releases resources without tearing down the applied rules, so that
	// customers keep their service across a panel restart.
	Close() error
}
