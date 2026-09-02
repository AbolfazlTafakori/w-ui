//go:build !linux

package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
)

// ErrUnavailable is returned when the kernel cannot do policy routing here.
var ErrUnavailable = errors.New("routing: policy routing unavailable")

// Applier is the non-Linux stand-in.
//
// Packet marks and policy routing are Linux kernel features, so elsewhere the
// panel builds and runs but routes nothing. It keeps rendering the program so
// a policy that cannot be expressed fails during development rather than on
// the first deployment.
type Applier struct {
	log *slog.Logger

	mu      sync.Mutex
	applied string
}

// NewApplier builds the inert applier for this platform.
func NewApplier(log *slog.Logger) *Applier { return &Applier{log: log} }

func (a *Applier) Apply(_ context.Context, p Policy) error {
	script, err := BuildRuleset(p)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.applied = script
	a.mu.Unlock()
	return nil
}

func (a *Applier) Counters(context.Context) (map[uint32]uint64, error) {
	return map[uint32]uint64{}, nil
}

func (a *Applier) Teardown(context.Context, []Hop) error { return nil }

func (a *Applier) Health(context.Context) error {
	return fmt.Errorf("%w: packet marking and policy routing are Linux-only, "+
		"and this panel is running on %s", ErrUnavailable, runtime.GOOS)
}

// Ruleset returns the last program built. Test and development aid.
func (a *Applier) Ruleset() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applied
}
