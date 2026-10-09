package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpagent"
)

// l2tpAgentInterval is how often the agent looks at what the panel asked for
// and reports back: faster than the panel's collection tick, so a device the
// panel cuts off is gone within a tick of the decision.
const l2tpAgentInterval = time.Second

// l2tpAgentCommand is the root half of the L2TP/IPsec tunnel. The panel runs
// unprivileged and cannot run pppd; it describes the tunnel in its data
// directory, and wui-l2tp.service runs this, as root, for as long as that
// description exists.
func l2tpAgentCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("l2tp-agent takes the panel's data directory")
	}
	if os.Geteuid() != 0 {
		return errors.New("l2tp-agent runs as root: pppd and xl2tpd will not start otherwise")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	files, done, err := l2tpagent.OpenFiles(args[0])
	if err != nil {
		return fmt.Errorf("l2tp-agent: %w", err)
	}
	defer done()

	// Stopped by systemd -- for an update, or a restart -- the agent leaves
	// xl2tpd and every session running, for its next run to adopt.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	agent := l2tpagent.New(l2tpagent.NewSystem(log), files, log)
	log.Info("l2tp agent started", "version", version, "data", args[0])
	return agent.Run(ctx, l2tpAgentInterval)
}
