package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpagent"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/update"
)

// What a setup request to the update helper does, in this build: the L2TP
// agent's units. Set for the helper's run and for a panel that is root
// already and does it itself.
func init() {
	update.Setup = func(dataDir, binary string) error { return l2tpagent.InstallUnits(binary, dataDir) }
}

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

	// Stopped by systemd -- for an update, or a restart -- the agent leaves
	// xl2tpd, pptpd and every session running, for its next run to adopt.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	sys := l2tpagent.NewSystem(log)
	var agents []l2tpagent.Stepper
	for _, p := range []string{l2tpconf.ProtocolL2TP, l2tpconf.ProtocolPPTP} {
		files, done, err := l2tpagent.OpenFiles(args[0], p)
		if err != nil {
			return fmt.Errorf("l2tp-agent: %w", err)
		}
		defer done()
		agents = append(agents, l2tpagent.New(p, sys, files, log))
	}
	// IKEv2 beside them, in the strongSwan L2TP's IPsec runs in.
	ikeFiles, ikeDone, err := l2tpagent.OpenFiles(args[0], l2tpconf.ProtocolIKEv2)
	if err != nil {
		return fmt.Errorf("l2tp-agent: %w", err)
	}
	defer ikeDone()
	agents = append(agents, l2tpagent.NewIKEv2(l2tpagent.NewIPsecSystem(log), ikeFiles, log))
	log.Info("ppp agent started", "version", version, "data", args[0])
	return l2tpagent.Run(ctx, l2tpAgentInterval, agents...)
}

// ensureL2TPUnits puts the L2TP agent's systemd units in place when they are
// missing or are not what this build writes.
//
// The installer and the update script write them. A server updated from the
// panel's own button ran neither -- the release that installed this build
// knew nothing of L2TP -- so the panel asks the update helper, the root
// channel it already has, to have this binary write them. Once, at start, and
// only where systemd runs.
func ensureL2TPUnits(ctx context.Context, log *slog.Logger, dataDir string) {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return
	}
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return
	}
	if dir, err := filepath.Abs(dataDir); err == nil {
		dataDir = dir
	}
	if l2tpagent.UnitsInPlace(self, dataDir) {
		return
	}
	if err := update.RequestSetup(ctx, dataDir); err != nil {
		log.Warn("the L2TP agent's service is not in place; an L2TP tunnel will not start "+
			"until the update script has been run once on this server", "error", err)
		return
	}
	log.Info("the L2TP agent's service was put in place")
}
