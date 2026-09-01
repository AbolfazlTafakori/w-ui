//go:build !linux

package enforce

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
)

// NFTables is the non-Linux stand-in.
//
// nftables is a Linux kernel subsystem, so on any other platform the panel can
// still be built and developed but cannot enforce anything. It says so through
// Health rather than pretending, and it keeps building the ruleset so the
// generator stays exercised by the test suite everywhere.
type NFTables struct {
	log *slog.Logger

	mu      sync.Mutex
	applied string
}

// NewNFTables builds the inert enforcer for this platform.
func NewNFTables(log *slog.Logger) *NFTables {
	return &NFTables{log: log}
}

func (n *NFTables) Apply(_ context.Context, rules []Rule) error {
	// Still generated: a malformed rule should fail here in development rather
	// than on the first Linux deployment.
	script, err := BuildRuleset(rules)
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.applied = script
	n.mu.Unlock()
	return nil
}

func (n *NFTables) Usage(context.Context) ([]Usage, error)         { return nil, nil }
func (n *NFTables) DrainCounters(context.Context) ([]Usage, error) { return nil, nil }
func (n *NFTables) ResetQuota(context.Context, []string) error     { return nil }
func (n *NFTables) Close() error                                   { return nil }

func (n *NFTables) Health(context.Context) error {
	return fmt.Errorf("%w: nftables is Linux-only and this panel is running on %s",
		ErrUnavailable, runtime.GOOS)
}

// Ruleset returns the last program built. Test and development aid.
func (n *NFTables) Ruleset() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.applied
}
