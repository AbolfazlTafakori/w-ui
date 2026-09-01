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

	l := slog.New(h)
	slog.SetDefault(l)
	return l, nil
}
