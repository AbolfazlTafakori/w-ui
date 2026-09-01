// Package logger builds the panel's structured logger.
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// New builds a slog logger at the given level and format and installs it as the
// process default.
// Recent is the process-wide ring the panel reads its own log from.
//
// It is package-level because the logger is built before anything that would
// otherwise own it, and because there is exactly one panel process — a second
// ring would simply be a second half of the same log.
var Recent = NewRing()

func New(level, format string) (*slog.Logger, error) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "info", "":
		lv = slog.LevelInfo
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, fmt.Errorf("logger: unknown level %q", level)
	}

	opts := &slog.HandlerOptions{Level: lv}

	var h slog.Handler
	switch strings.ToLower(format) {
	case "json":
		h = slog.NewJSONHandler(os.Stdout, opts)
	case "text", "":
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("logger: unknown format %q, want text or json", format)
	}

	// Everything written also lands in the ring, so the panel can show the
	// recent past without an SSH session.
	l := slog.New(Tee(h, Recent))
	slog.SetDefault(l)
	return l, nil
}
