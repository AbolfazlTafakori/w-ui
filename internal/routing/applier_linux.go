//go:build linux

package routing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/abolfazl/w-ui/internal/nftstate"
	"log/slog"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	nftBinary  = "nft"
	ipBinary   = "ip"
	cmdTimeout = 10 * time.Second
)

// ErrUnavailable is returned when the kernel cannot do policy routing here.
var ErrUnavailable = errors.New("routing: policy routing unavailable")

// Applier puts a policy into the kernel.
//
// Two things are written and they can fail independently: the nftables program
// that marks and drops, and the ip rules that turn a mark into a route. The
// program is written first. A mark with no rule behind it routes normally,
// which is the safe direction to fail — the reverse would send traffic to a
// table that does not exist yet and black-hole it.
type Applier struct {
	log *slog.Logger

	mu         sync.Mutex
	applied    string // last nft program written
	appliedNAT string // last masquerade program written
	prunedKey  string // the hop set the stale rules were last swept for
	appliedP   string // fingerprint of the last routing plan
	lastErr    error
	ready      bool
}

// NewApplier builds an applier for this host.
func NewApplier(log *slog.Logger) *Applier { return &Applier{log: log} }

// Apply makes the kernel match p.
func (a *Applier) Apply(ctx context.Context, p Policy) error {
	script, err := BuildRuleset(p)
	if err != nil {
		return err
	}

	a.mu.Lock()
	sameScript := script == a.applied
	a.mu.Unlock()
	if sameScript && !a.hasTable(ctx, "inet", TableName) {
		a.log.Warn("the panel's routing table was removed by something else on this server; putting it back",
			"table", "inet "+TableName)
		sameScript = false
	}

	if !sameScript {
		if _, err := a.runNFT(ctx, script, "-f", "-"); err != nil {
			return fmt.Errorf("routing: apply policy: %w", err)
		}
		a.mu.Lock()
		a.applied = script
		a.mu.Unlock()
		a.log.Info("routing policy applied",
			"rules", len(p.Rules), "hops", len(p.Hops))
	}

	if err := a.applyNAT(ctx, p); err != nil {
		return err
	}

	plan := BuildPlan(p.Hops)
	fp := planFingerprint(plan)

	// Rules of hops that are gone are swept whenever the set of hops
	// changes -- including to none, which no plan expresses -- and once on
	// the first apply after a start, for whatever the last run left.
	hopsKey := hopsFingerprint(p.Hops)
	a.mu.Lock()
	samePlan := fp == a.appliedP
	prune := hopsKey != a.prunedKey
	a.prunedKey = hopsKey
	a.mu.Unlock()
	if prune {
		a.pruneRules(ctx, p.Hops)
	}
	if samePlan && !a.rulesPresent(ctx, plan) {
		a.log.Warn("the panel's routing rules were removed by something else on this server; putting them back",
			"hops", len(p.Hops))
		samePlan = false
	}
	if samePlan {
		return nil
	}

	// Removals are expected to fail when there is nothing to remove — a fresh
	// boot, or a hop that was never installed. That is not an error worth
	// reporting, and treating it as one would make every first apply look
	// broken.
	for _, s := range plan.Remove {
		_, _ = a.runIP(ctx, s.Args...)
	}
	for _, s := range plan.Add {
		if _, err := a.runIP(ctx, s.Args...); err != nil {
			a.mu.Lock()
			a.lastErr = err
			a.appliedP = "" // force a full re-apply next tick
			a.mu.Unlock()
			return fmt.Errorf("routing: could not %s: %w", s.Describe, err)
		}
	}

	a.mu.Lock()
	a.appliedP = fp
	a.ready = true
	a.lastErr = nil
	a.mu.Unlock()
	return nil
}

// pruneRules removes the ip rules left behind by hops that no longer
// exist. The plan only knows the hops it was given; a hop deleted from the
// panel is in no plan, so its rule -- one of our marks, pointing at an
// emptied table -- would otherwise stay until reboot, sending anything that
// still carried that mark into nothing.
func (a *Applier) pruneRules(ctx context.Context, hops []Hop) {
	out, err := a.runIP(ctx, "rule", "show")
	if err != nil {
		return
	}
	want := map[uint32]bool{}
	for _, h := range hops {
		want[h.Mark] = true
	}
	for m, table := range nftstate.RuleMarks(out) {
		// Both halves have to be ours. A mark in our range pointing at a
		// table outside it -- or at a table another program named in
		// rt_tables -- is someone else's rule, and flushing its table would
		// take that program's routes with it.
		tableID, err := strconv.Atoi(table)
		if err != nil || !OwnsTable(tableID) || !OwnsMark(m) || want[m] {
			continue
		}
		mark := fmt.Sprintf("0x%x", m)
		_, _ = a.runIP(ctx, "rule", "del", "fwmark", mark, "table", table)
		_, _ = a.runIP(ctx, "route", "flush", "table", table)
		a.log.Info("stale routing rule removed", "mark", mark, "table", table)
	}
}

// applyNAT rewrites the source address of traffic leaving a tunnel.
//
// Kept apart from the steering program because it can fail on its own: a kernel
// without nat support loses this and keeps its routing, rather than losing
// both. The failure is reported rather than swallowed -- without it customers
// connect, handshake, and reach nothing, which is indistinguishable from a
// tunnel that is simply broken.
func (a *Applier) applyNAT(ctx context.Context, p Policy) error {
	script := BuildNAT(p.CustomerNets, p.TunnelDevices)

	a.mu.Lock()
	same := script == a.appliedNAT
	a.mu.Unlock()
	if same {
		if a.hasTable(ctx, "ip", NATTableName) {
			return nil
		}
		// Without it customers connect and reach nothing.
		a.log.Warn("the panel's tunnel address translation was removed by something else on this server; putting it back",
			"table", "ip "+NATTableName)
	}

	if _, err := a.runNFT(ctx, script, "-f", "-"); err != nil {
		return fmt.Errorf("routing: customers cannot reach anything outside the "+
			"tunnel until this is applied: %w", err)
	}

	a.mu.Lock()
	a.appliedNAT = script
	a.mu.Unlock()
	a.log.Info("tunnel egress translated",
		"subnets", len(p.CustomerNets), "tunnels", len(p.TunnelDevices))
	return nil
}

// hasTable reports whether one of the panel's tables is still in the kernel,
// saying yes when it cannot tell: a failed look is no reason to rewrite the
// rules every tick.
func (a *Applier) hasTable(ctx context.Context, family, name string) bool {
	out, err := a.runNFT(ctx, "", "list", "tables")
	if err != nil {
		return true
	}
	return nftstate.HasTable(out, family, name)
}

// rulesPresent reports whether every rule the plan adds -- one per hop, from
// its mark to its table -- is still there.
func (a *Applier) rulesPresent(ctx context.Context, plan Plan) bool {
	out, err := a.runIP(ctx, "rule", "show")
	if err != nil {
		return true
	}
	have := nftstate.RuleMarks(out)
	for _, s := range plan.Add {
		if len(s.Args) < 6 || s.Args[0] != "rule" || s.Args[2] != "fwmark" {
			continue
		}
		var m uint64
		if _, err := fmt.Sscanf(s.Args[3], "0x%x", &m); err != nil {
			continue
		}
		if have[uint32(m)] != s.Args[5] {
			return false
		}
	}
	return true
}

// Counters reads what each outbound has carried.
func (a *Applier) Counters(ctx context.Context) (map[uint32]uint64, error) {
	out, err := a.runNFT(ctx, "", "-j", "list", "counters", "table", "inet", TableName)
	if err != nil {
		if missingTable(err) {
			// Nothing applied yet. Not a fault.
			return map[uint32]uint64{}, nil
		}
		return nil, err
	}
	return parseCounters(out)
}

// Health reports whether policy routing is usable on this host.
func (a *Applier) Health(ctx context.Context) error {
	a.mu.Lock()
	lastErr := a.lastErr
	a.mu.Unlock()
	if lastErr != nil {
		return lastErr
	}

	// `ip rule` needs no privilege to list and fails loudly where the kernel
	// was built without policy routing, which is the one thing that would make
	// every outbound silently do nothing.
	if _, err := a.runIP(ctx, "rule", "list"); err != nil {
		return fmt.Errorf("%w: the kernel does not support policy routing here (%v)",
			ErrUnavailable, err)
	}
	return nil
}

// Teardown removes everything this package installed.
//
// Called when the panel is uninstalled or when routing is switched off. It is
// deliberately thorough about rules and deliberately narrow about which ones:
// only marks in our own range are touched, because a rule we did not create
// belongs to something else on this machine.
func (a *Applier) Teardown(ctx context.Context, hops []Hop) error {
	plan := BuildPlan(nil)
	for _, h := range hops {
		if !OwnsMark(h.Mark) || !OwnsTable(h.Table) {
			continue
		}
		plan.Remove = append(plan.Remove,
			Statement{Args: []string{"rule", "del", "fwmark",
				fmt.Sprintf("0x%08x", h.Mark), "table", fmt.Sprintf("%d", h.Table)}},
			Statement{Args: []string{"route", "flush", "table", fmt.Sprintf("%d", h.Table)}},
		)
	}
	for _, s := range plan.Remove {
		_, _ = a.runIP(ctx, s.Args...)
	}

	if _, err := a.runNFT(ctx, "", "delete", "table", "inet", TableName); err != nil && !missingTable(err) {
		return fmt.Errorf("routing: remove policy table: %w", err)
	}

	a.mu.Lock()
	a.applied, a.appliedP, a.ready = "", "", false
	a.mu.Unlock()
	return nil
}

func (a *Applier) runNFT(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	return run(ctx, nftBinary, stdin, args...)
}

func (a *Applier) runIP(ctx context.Context, args ...string) ([]byte, error) {
	return run(ctx, ipBinary, "", args...)
}

func run(ctx context.Context, bin, stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
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
		wrapped := fmt.Errorf("%s %s: %s", bin, strings.Join(args, " "), msg)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w (timed out after %s)", wrapped, cmdTimeout)
		}
		return nil, wrapped
	}
	return out.Bytes(), nil
}

func missingTable(err error) bool {
	s := err.Error()
	return strings.Contains(s, "No such file or directory") ||
		strings.Contains(s, "does not exist")
}

// planFingerprint reduces a plan to a string so an unchanged plan is not
// re-applied. Re-adding the same ip rule every tick would work, but it would
// also mean every tick spawns processes for nothing.
func hopsFingerprint(hops []Hop) string {
	marks := make([]string, 0, len(hops))
	for _, h := range hops {
		marks = append(marks, fmt.Sprintf("%08x", h.Mark))
	}
	sort.Strings(marks)
	return "hops:" + strings.Join(marks, ",")
}

func planFingerprint(p Plan) string {
	var b strings.Builder
	for _, s := range p.Add {
		b.WriteString(strings.Join(s.Args, " "))
		b.WriteByte('\n')
	}
	return b.String()
}
