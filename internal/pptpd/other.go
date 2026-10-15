//go:build !linux

package pptpd

import (
	"context"
	"errors"
	"log/slog"
)

// Config is the server's configuration; the server itself is Linux's.
type Config struct {
	Listen, Options, Pppd, Hostname string
	MaxConns                        int
}

// ReadConfig is Linux's.
func ReadConfig(string) (Config, error) { return Config{}, errors.New("PPTP is served on Linux only") }

// Run is Linux's.
func Run(context.Context, Config, *slog.Logger) error {
	return errors.New("PPTP is served on Linux only")
}
