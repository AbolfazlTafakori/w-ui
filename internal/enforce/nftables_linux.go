//go:build linux

package enforce

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/abolfazl/w-ui/internal/nftstate"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// nftBinary is the tool the enforcer drives.
//
// The panel shells out to `nft` rather than speaking netlink directly. `nft -f`
// applies a whole program in one transaction, which is the property that
// matters most here: a partially applied ruleset would leave some customers
// metered and others running free. It also means the exact text being applied
// can be logged and diffed, which a netlink call cannot offer.
const nftBinary = "nft"

// nftTimeout bounds every invocation. A hung firewall call must not stall the
// reconciler, because that would stop quota collection for every client.
const nftTimeout = 10 * time.Second

// NFTables enforces quotas in the kernel's packet path.
type NFTables struct {
	log *slog.Logger

	mu      sync.Mutex
	applied string // the last ruleset written, to skip no-op applies
	// appliedKeys is who that ruleset covered, so a drained tick can tell
	// whether the kernel still holds it or something has cleared it since.
	appliedKeys map[string]struct{}
	ready       bool // a ruleset has been written at least once; never cleared
	lastErr     error
	caps        Caps
	probed      bool
	// netlinkSaid is that reading over netlink failed and was said once.
	netlinkSaid bool
	// state is what the table holds, customer by customer, as last
	// written; nil when that is not known and the next apply writes it
	// whole (incremental.go).
	state tableState
	// applies counts incremental applies, for reading the quotas' tallies
	// every driftEvery of them.
	applies uint64
}

// driftEvery is how many applies apart the quotas' tallies are read for
// drift: about half a minute at the two-second tick.
const driftEvery = 15

// quotasIf reads the quotas' tallies when asked to, and nothing otherwise.
func (n *NFTables) quotasIf(read bool) ([]namedValue, error) {
	if !read {
		return nil, nil
	}
	return n.objects(nftObjectQuota, false)
}

// probeCaps finds out what this kernel supports, once.
//
// It creates a throwaway table with a quota object and deletes it again. A
// kernel without nft_quota rejects that, and since `nft -f` is atomic, quietly
// emitting quota objects anyway would make every apply fail and leave the
// server with no enforcement whatsoever.
func (n *NFTables) probeCaps(ctx context.Context) Caps {
	n.mu.Lock()
	if n.probed {
		c := n.caps
		n.mu.Unlock()
		return c
	}
	n.mu.Unlock()

	const probeTable = "wui_probe"
	caps := Caps{}

	_, _ = n.run(ctx, "", "delete", "table", "inet", probeTable)
	script := fmt.Sprintf(
		"add table inet %s\nadd quota inet %s probe { over 1000 bytes }\n",
		probeTable, probeTable)
	if _, err := n.run(ctx, script, "-f", "-"); err == nil {
		caps.Quota = true
	}
	_, _ = n.run(ctx, "", "delete", "table", "inet", probeTable)

	n.mu.Lock()
	n.caps = caps
	n.probed = true
	n.lastErr = nil // the probe's own failures are not a panel fault
	n.mu.Unlock()

	if !caps.Quota {
		n.log.Warn("this kernel has no nft_quota support",
			"consequence", "volume limits are enforced by the panel a tick late, not by the kernel per packet",
			"fix", "use a kernel with CONFIG_NFT_QUOTA (most stock distro kernels have it)")
	}
	return caps
}

// Caps reports what the kernel supports, for the settings page.
func (n *NFTables) Caps(ctx context.Context) Caps { return n.probeCaps(ctx) }

// NewNFTables builds the Linux enforcer.
func NewNFTables(log *slog.Logger) *NFTables {
	return &NFTables{log: log}
}

// Apply makes the kernel match rules exactly.
//
// The first time, and whenever the table is gone or what was last written
// is no longer known, it writes the whole table. Every other time it changes
// only what differs from what it wrote (incremental.go): a steady server
// sends the kernel nothing, and a customer's traffic is never a change.
func (n *NFTables) Apply(ctx context.Context, rules []Rule) error {
	caps := n.probeCaps(ctx)
	want, err := desiredState(rules, caps)
	if err != nil {
		return err
	}

	n.mu.Lock()
	have := n.state
	// Not the state: any failed nft call clears that, the counter drain
	// that finds the table gone included, and runs before this each tick.
	written := n.ready
	n.mu.Unlock()
	present := !written || n.tablePresent(ctx)
	if !present {
		// Written before and gone now: something else on this server
		// replaced the whole ruleset. Put back at once -- every limit and
		// every switched-off customer depends on it.
		n.log.Warn("the panel's firewall table was removed by something else on this server; putting it back",
			"table", "inet "+TableName,
			"likely", "a firewall reload that flushes the whole ruleset, such as nftables.service with flush ruleset in /etc/nftables.conf")
		have = nil
	}

	if have != nil {
		// What each quota has used, for the ones whose tally has drifted
		// from the stored usage; nil when it cannot be read, and then only
		// a limit that changed or usage that went down writes one again.
		// Read every driftEvery applies, not every one: drift is usage on
		// other servers or an operator's edit, which a quota may trail by
		// half a minute, while a renewal -- which may not wait -- is seen
		// in the stored usage alone.
		var kernelUsed map[string]uint64
		n.mu.Lock()
		n.applies++
		readDrift := n.applies%driftEvery == 0
		n.mu.Unlock()
		// Not read, nothing is compared: changeScript then reseeds only on
		// a changed limit or a fall in stored usage.
		if objs, err := n.quotasIf(readDrift); err == nil && readDrift {
			kernelUsed = make(map[string]uint64, len(objs))
			for _, o := range objs {
				kernelUsed[o.name] = o.value
			}
		}
		script, next := changeScript(have, want, kernelUsed)
		if script == "" {
			n.mu.Lock()
			n.state = next
			n.mu.Unlock()
			return nil
		}
		if _, err := n.run(ctx, script, "-f", "-"); err == nil {
			n.mu.Lock()
			n.state = next
			n.appliedKeys = ruleKeys(rules)
			n.lastErr = nil
			n.mu.Unlock()
			n.log.Debug("enforcement changed", "lines", strings.Count(script, "\n"))
			return nil
		} else {
			// Whatever the kernel holds now, the whole table makes it right.
			n.log.Warn("could not change the firewall table in place; writing it whole", "error", err)
		}
	}

	script, err := BuildRulesetWithCaps(rules, caps)
	if err != nil {
		return err
	}
	if _, err := n.run(ctx, script, "-f", "-"); err != nil {
		return fmt.Errorf("enforce: apply ruleset: %w", err)
	}

	n.mu.Lock()
	n.applied = script
	n.state = want
	n.appliedKeys = ruleKeys(rules)
	n.ready = true
	n.lastErr = nil
	n.mu.Unlock()

	n.log.Info("enforcement ruleset applied", "clients", len(rules))
	return nil
}

// tablePresent reports whether the panel's table is still in the kernel. When
// it cannot tell, it says yes: a failed look is no reason to rewrite the
// firewall every two seconds.
func (n *NFTables) tablePresent(ctx context.Context) bool {
	out, err := nftstate.ListTables()
	if err != nil {
		out, err = n.run(ctx, "", "list", "tables")
	}
	if err != nil {
		return true
	}
	return nftstate.HasTable(out, "inet", TableName)
}

// missingTable reports whether an error is nft saying the table is not there.
//
// On a fresh install the first tick reads counters before anything has been
// applied. Treating that as a failure aborts the tick before the apply that
// would have created the table, so enforcement never starts at all — the loop
// deadlocks on its own first run. No table simply means nothing has flowed.
func missingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "No such file or directory")
}

// Usage reports cumulative bytes charged against each quota, without resetting.
func (n *NFTables) Usage(ctx context.Context) ([]Usage, error) {
	if objs, err := n.objects(nftObjectQuota, false); err == nil {
		return usageOfQuotas(objs), nil
	} else if errors.Is(err, errNoTable) {
		return nil, nil
	}
	out, err := n.run(ctx, "", "-j", "list", "quotas", "table", "inet", TableName)
	if missingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enforce: list quotas: %w", err)
	}
	return parseObjects(out, func(e struct {
		Counter *nftObject `json:"counter"`
		Quota   *nftObject `json:"quota"`
	}) (*nftObject, uint64, bool) {
		if e.Quota == nil {
			return nil, 0, false
		}
		return e.Quota, e.Quota.Used, true
	}, "q_")
}

// DrainCounters reads and zeroes the reporting counters in one operation.
//
// `nft reset` prints each object's value and clears it in the same command, so
// bytes arriving between a read and a separate reset cannot be lost. Doing it
// as two calls would quietly under-bill every customer under load.
func (n *NFTables) DrainCounters(ctx context.Context) ([]Usage, error) {
	if objs, err := n.objects(nftObjectCounter, true); err == nil {
		usage := usageOfCounters(objs)
		n.verify(usage)
		return usage, nil
	} else if errors.Is(err, errNoTable) {
		return nil, nil
	}
	out, err := n.run(ctx, "", "-j", "reset", "counters", "table", "inet", TableName)
	if missingTable(err) {
		// Nothing counted, and nothing to invalidate: any failing nft call
		// already drops the cached script, so the next tick rewrites.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enforce: drain counters: %w", err)
	}

	usage, err := drainedUsage(out)
	if err != nil {
		return nil, err
	}
	n.verify(usage)
	return usage, nil
}

// objects reads the table's counters or quotas over netlink, saying once
// when it cannot and the nft tool is used instead.
func (n *NFTables) objects(objType uint32, reset bool) ([]namedValue, error) {
	objs, err := dumpObjects(objType, reset)
	if err != nil && !errors.Is(err, errNoTable) {
		n.mu.Lock()
		said := n.netlinkSaid
		n.netlinkSaid = true
		n.mu.Unlock()
		if !said {
			n.log.Warn("counters are read with the nft tool, which is slower on a large table",
				"reason", err)
		}
	}
	return objs, err
}

// verify checks the drained counters against what was last applied.
//
// The case this exists for is the quiet one. A table that has been deleted
// outright makes the next nft call fail, and a failing call already drops the
// cached script, so that recovers on its own. A table that is still there but
// no longer holds our rules -- another writer replacing its contents, a second
// panel on the same host, a restore of someone else's saved ruleset -- fails
// nothing: the drain succeeds, returns no counters, and the cache goes on
// saying the ruleset is applied while nothing is enforced.
//
// The counters are drained every tick anyway and a counter is listed whether
// or not a byte has passed through it, so noticing costs nothing extra.
func (n *NFTables) verify(seen []Usage) {
	n.mu.Lock()
	gone := missingKeys(n.appliedKeys, seen)
	n.mu.Unlock()
	if len(gone) == 0 {
		return
	}
	n.forget(fmt.Sprintf("%d of the rules we wrote are missing (%s)",
		len(gone), strings.Join(gone, " ")))
}

// forget drops the cached ruleset so the next tick rewrites it.
func (n *NFTables) forget(why string) {
	n.mu.Lock()
	had := n.applied != ""
	n.applied = ""
	n.state = nil
	n.appliedKeys = nil
	n.mu.Unlock()
	if !had {
		return
	}
	n.log.Warn("something cleared our ruleset; rewriting it",
		"detail", why,
		"consequence", "traffic between then and now was neither counted nor capped")
}

// QuotaSpent asks the kernel, quota by quota, which of these keys it holds
// spent -- what it has used has reached its limit, and it drops their
// traffic -- and what each has used. Asked of a few customers near their
// limit, not of every one.
func (n *NFTables) QuotaSpent(_ context.Context, keys []string) (map[string]uint64, error) {
	out := map[string]uint64{}
	for _, k := range keys {
		if !validKey(k) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRule, k)
		}
		limit, used, found, err := quotaState(quotaName(k))
		if err != nil {
			return nil, err
		}
		if found && limit > 0 && used >= limit {
			out[k] = used
		}
	}
	return out, nil
}

// ResetQuota clears the cumulative quota for the given keys, used on renewal.
func (n *NFTables) ResetQuota(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if !validKey(k) {
			return fmt.Errorf("%w: %q", ErrInvalidRule, k)
		}
		if _, err := n.run(ctx, "", "reset", "quota", "inet", TableName, quotaName(k)); err != nil {
			// A renewed client whose quota object has since been rebuilt is not
			// an error worth failing the batch for; the next apply reseeds it.
			n.log.Warn("could not reset quota", "key", k, "error", err)
		}
	}
	// The quotas were reset under what was written, so the next apply
	// writes the whole table, seeding them from stored usage.
	n.mu.Lock()
	n.applied = ""
	n.state = nil
	n.appliedKeys = nil
	n.mu.Unlock()
	return nil
}

// Health reports whether the enforcer can actually reach the kernel.
func (n *NFTables) Health(ctx context.Context) error {
	n.mu.Lock()
	err := n.lastErr
	n.mu.Unlock()
	if err != nil {
		return err
	}

	if _, err := exec.LookPath(nftBinary); err != nil {
		return fmt.Errorf("%w: nft not found on PATH; install nftables", ErrUnavailable)
	}
	if _, err := n.run(ctx, "", "list", "tables"); err != nil {
		return fmt.Errorf("%w: cannot read the ruleset (needs CAP_NET_ADMIN): %v",
			ErrUnavailable, err)
	}

	// Reported rather than hidden. An operator selling a 50 GB plan deserves to
	// know the ceiling is enforced a couple of seconds late by the panel
	// instead of instantly by the kernel.
	if !n.probeCaps(ctx).Quota {
		return fmt.Errorf(
			"%w: this kernel has no nft_quota, so volume limits are applied by the panel "+
				"on its next tick rather than per packet; everything else works",
			ErrDegraded)
	}
	return nil
}

// Close leaves the applied rules in place.
//
// Tearing them down on shutdown would take every customer's limit off the
// moment the panel restarts, turning a routine upgrade into a window of
// unmetered traffic.
func (n *NFTables) Close() error { return nil }

func (n *NFTables) run(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, nftTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, nftBinary, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		wrapped := fmt.Errorf("nft %s: %s", strings.Join(args, " "), msg)

		n.mu.Lock()
		// A not-yet-created table is an expected state, not a fault, and must
		// not be latched into the health report.
		if !strings.Contains(msg, "No such file or directory") {
			n.lastErr = wrapped
		}
		// Drop the cache so the next attempt re-applies rather than assuming
		// the kernel still holds what we last wrote.
		n.applied = ""
		n.state = nil
		n.mu.Unlock()

		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w (timed out after %s)", wrapped, nftTimeout)
		}
		return nil, wrapped
	}
	return out.Bytes(), nil
}
