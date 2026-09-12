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

// Level is the running threshold, changeable from the engine page without
// a restart.
var Level = new(slog.LevelVar)

// ParseLevel reads a level name.
func ParseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logger: unknown level %q", level)
}

// SetLevel changes the running threshold; an unknown name is ignored.
func SetLevel(level string) {
	if lv, err := ParseLevel(level); err == nil {
		Level.Set(lv)
	}
}

func New(level, format string) (*slog.Logger, error) {
	lv, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	Level.Set(lv)

	opts := &slog.HandlerOptions{Level: Level}

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
