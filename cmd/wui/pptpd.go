package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/abolfazl/w-ui/internal/pptpd"
)

// pptpdCommand is the PPTP tunnel's server, which the L2TP agent starts with
// the tunnel's configuration file and keeps running in a session of its own,
// so the calls it carries outlive the agent's restarts.
func pptpdCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("pptpd takes its configuration file")
	}
	if os.Geteuid() != 0 {
		return errors.New("pptpd runs as root: GRE and pppd need it")
	}
	cfg, err := pptpd.ReadConfig(args[0])
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return pptpd.Run(ctx, cfg, log)
}
