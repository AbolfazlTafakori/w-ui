package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/abolfazl/w-ui/internal/fwsync"
)

// firewallSyncCommand lets the tunnels' ports through the server's own
// firewall, ufw or firewalld, as the panel last wrote them; run as root by
// wui-firewall.service whenever it does. With neither on, it does nothing.
func firewallSyncCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("firewall-sync takes the panel's data directory")
	}
	if os.Geteuid() != 0 {
		return errors.New("firewall-sync runs as root: the firewall is root's")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	raw, err := os.ReadFile(filepath.Join(args[0], fwsync.DesiredFile))
	var want []fwsync.Rule
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No tunnels described: whatever the helper opened is closed.
	case err != nil:
		return err
	default:
		if want, err = fwsync.Decode(raw); err != nil {
			return fmt.Errorf("firewall-sync: %w", err)
		}
	}
	return fwsync.Sync(want, fwsync.Active(), log)
}
