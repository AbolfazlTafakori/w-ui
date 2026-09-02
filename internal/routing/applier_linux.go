//go:build linux

package routing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
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

	mu       sync.Mutex
	applied  string // last nft program written
	appliedP string // fingerprint of the last routing plan
	lastErr  error
	ready    bool
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

	plan := BuildPlan(p.Hops)
	fp := planFingerprint(plan)

	a.mu.Lock()
	samePlan := fp == a.appliedP
	a.mu.Unlock()
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
func planFingerprint(p Plan) string {
	var b strings.Builder
	for _, s := range p.Add {
		b.WriteString(strings.Join(s.Args, " "))
		b.WriteByte('\n')
	}
	return b.String()
}
