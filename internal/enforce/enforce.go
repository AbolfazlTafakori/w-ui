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
// Two kinds of stateful object carry the two jobs, which must not be
// conflated:
//
//	quota   one per client, cumulative, seeded from the database at boot,
//	        never reset except on renewal. This is what stops traffic.
//	counter one per file and direction, drained atomically on every
//	        collection tick. Read-and-zero in one operation removes the need
//	        for delta arithmetic and reset detection.
//
// A client's usage is the sum of its files' counters, never a figure of its
// own: every packet the client is charged for passes exactly one file's
// counter, so what each device carried adds up to what the client used by
// construction rather than by estimate.
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

// File is one of a client's devices on this server: the account it belongs
// to and the tunnel address its traffic is recognised by.
type File struct {
	Account uint
	Addr    netip.Addr
}

// Rule is the desired kernel-side policy for one client.
type Rule struct {
	// Key is a stable identifier derived from the client, used to name
	// the kernel objects. It must survive restarts.
	Key string

	// Files are the client's devices, one tunnel address each. Traffic in
	// both directions across all of them counts against one quota, which is
	// what makes the limit apply to the client rather than to each device
	// separately; each is also counted on its own.
	Files []File

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

// tallyOnly is the kernel quota an unlimited customer is given: a limit no
// one reaches, kept for its running total alone. That total is what a
// panel killed mid-flight recovers from at start (the reconciler's
// recoverKernelUsage) -- the bytes it had drained but not yet written. An
// unlimited customer had none, and lost up to five seconds of usage
// (measured on the test server: 13.4 MB of a 100 MB download at 5 MB/s).
const tallyOnly uint64 = 1 << 62

// kernelQuota is the limit of the customer's quota in the kernel: their
// allowance, or tallyOnly when they have none.
func (r Rule) kernelQuota() uint64 {
	if r.Unlimited() {
		return tallyOnly
	}
	return r.QuotaBytes
}

// FullDrainByNameAbove is how many counters a table holds past which every
// counter is read by name rather than dumped. A dump costs the kernel more
// than in proportion to the table, measured on arm64: 46 ms for 10,000
// counters, 977 ms for 50,000, 27.8 s for 200,000; by name it is 27 µs a
// counter, 5.4 s for 200,000. They cross between 50,000 and 200,000.
const FullDrainByNameAbove = 60_000

// FileRef names one file's counters: the customer's rule key and the
// file's account.
type FileRef struct {
	Key     string
	Account uint
}

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

	// Files is what each of the client's files carried, from the same
	// packets as the total, so the files sum to Bytes exactly. Empty when
	// nothing was measured per file -- the counters of a ruleset written by
	// an older panel, still in the kernel for the first tick after an
	// update -- in which case no file is charged rather than one guessed at.
	Files []FileUsage
}

// FileUsage is what one file carried, as the customer sees it: Up is what
// they sent, Down what they received.
type FileUsage struct {
	Account uint
	Up      uint64
	Down    uint64
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

	// DrainFiles is DrainCounters for these files alone: read and zeroed
	// the same way, at a cost that grows with the files asked for rather
	// than with the table (measured on arm64: 46 ms for all of 10,000
	// counters and 977 ms for 50,000, against 27 µs a counter asked by
	// name). A file whose counters are not there is left out.
	DrainFiles(ctx context.Context, files []FileRef) ([]Usage, error)

	// ResetQuota clears the cumulative quota for the given keys, used on
	// renewal.
	ResetQuota(ctx context.Context, keys []string) error

	// QuotaSpent says which of these keys' quotas the kernel holds spent,
	// and so is dropping their traffic, each with what its quota has used.
	// The packet that crosses a quota is dropped before any counter sees
	// it, so stored usage stops just short of the limit: the kernel is what
	// knows a customer is out.
	QuotaSpent(ctx context.Context, keys []string) (map[string]uint64, error)

	// Health reports whether the enforcement backend is usable.
	Health(ctx context.Context) error

	// Close releases resources without tearing down the applied rules, so that
	// customers keep their service across a panel restart.
	Close() error
}
