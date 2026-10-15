//go:build linux

package fwsync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// UnitDir is where the units go.
var UnitDir = "/etc/systemd/system"

// InstallUnits writes the helper's units where they are missing or differ,
// switches the watcher on, and runs the helper once, so the ports already
// wanted are open now and not only after the next change. Run as root, by
// the update helper.
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
		tmp := file + ".tmp"
		if err := os.WriteFile(tmp, []byte(u.body), 0o644); err != nil {
			return fmt.Errorf("fwsync: writing %s: %w", u.name, err)
		}
		if err := os.Rename(tmp, file); err != nil {
			return fmt.Errorf("fwsync: writing %s: %w", u.name, err)
		}
		changed = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	systemctl := func(args ...string) error {
		out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
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
	if err := systemctl("enable", ServiceUnit); err != nil {
		return err
	}
	return systemctl("start", "--no-block", ServiceUnit)
}
