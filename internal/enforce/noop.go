package enforce

import (
	"context"
	"sort"
	"sync"
)

// Noop records rules without programming anything.
//
// It stands in for the nftables enforcer on hosts that cannot run it — a
// developer machine, or the test suite — so the layers above can be built and
// exercised now. It accounts for nothing, so a panel running on it does not
// enforce quotas; Health says so rather than pretending otherwise.
type Noop struct {
	mu    sync.Mutex
	rules map[string]Rule
}

// NewNoop builds an inert enforcer.
func NewNoop() *Noop { return &Noop{rules: map[string]Rule{}} }

func (n *Noop) Apply(_ context.Context, rules []Rule) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	next := make(map[string]Rule, len(rules))
	for _, r := range rules {
		next[r.Key] = r
	}
	n.rules = next
	return nil
}

func (n *Noop) Usage(_ context.Context) ([]Usage, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	out := make([]Usage, 0, len(n.rules))
	for key, r := range n.rules {
		out = append(out, Usage{Key: key, Bytes: r.UsedBytes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (n *Noop) DrainCounters(_ context.Context) ([]Usage, error) { return nil, nil }

func (n *Noop) ResetQuota(_ context.Context, keys []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	for _, k := range keys {
		if r, ok := n.rules[k]; ok {
			r.UsedBytes = 0
			n.rules[k] = r
		}
	}
	return nil
}

// Health always reports unavailable. Enforcement is the panel's core promise,
// so an operator must never be able to mistake this for a working setup.
func (n *Noop) Health(_ context.Context) error { return ErrUnavailable }

func (n *Noop) Close() error { return nil }

// Rules returns the applied rule set, sorted by key. Test-only.
func (n *Noop) Rules() []Rule {
	n.mu.Lock()
	defer n.mu.Unlock()

	out := make([]Rule, 0, len(n.rules))
	for _, r := range n.rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
