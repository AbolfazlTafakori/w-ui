//go:build linux

package l2tpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InstallUnits writes the agent's units where they are missing or differ,
// reloads systemd, switches the watcher on and brings an agent already
// running onto this binary. Its tunnel's sessions stay up through that: only
// the agent restarts.
//
// Run as root, by the installer's path or the update helper's.
func InstallUnits(binary, dataDir string) error {
	service, watch, err := Units(binary, dataDir)
	if err != nil {
		return err
	}
	changed := false
	for _, u := range []struct{ name, body string }{{ServiceUnit, service}, {PathUnit, watch}} {
		file := filepath.Join(UnitDir, u.name)
		if cur, err := os.ReadFile(file); err == nil && string(cur) == u.body {
			continue
		}
		if err := writeAtomic(file, u.body, 0o644); err != nil {
			return fmt.Errorf("l2tpagent: writing %s: %w", u.name, err)
		}
		changed = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	systemctl := func(args ...string) error {
		out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, tail(out))
		}
		return nil
	}
	if changed {
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
	}
	if err := systemctl("enable", "--now", PathUnit); err != nil {
		return err
	}
	return systemctl("try-restart", ServiceUnit)
}
