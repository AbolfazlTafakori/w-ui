//go:build !linux

package shaper

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
)

// TC is the non-Linux stand-in.
//
// tc is part of iproute2 and has no equivalent elsewhere. Rate limits are still
// stored and shown; the panel says plainly that they are not being applied,
// rather than implying a limit that nothing enforces.
type TC struct{ log *slog.Logger }

// New builds the inert shaper for this platform.
func New(log *slog.Logger) *TC {
	if log == nil {
		log = slog.Default()
	}
	return &TC{log: log}
}

func (s *TC) Apply(context.Context, []string, []Client) error { return unsupported() }
func (s *TC) Health(context.Context) error                    { return unsupported() }
func (s *TC) Close() error                                    { return nil }

func unsupported() error {
	return fmt.Errorf("%w: tc is Linux-only and this panel is running on %s",
		ErrUnavailable, runtime.GOOS)
}
