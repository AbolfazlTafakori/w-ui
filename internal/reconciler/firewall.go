package reconciler

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fwsync"
)

// keepFirewall writes the ports this server's tunnels need let through the
// firewall, when they changed. The root helper opens them in ufw or
// firewalld when one is on (package fwsync).
func (r *Reconciler) keepFirewall(ifaces []model.Interface) {
	if r.dataDir == "" {
		return
	}
	body := fwsync.Encode(fwsync.ForInterfaces(ifaces))
	r.mu.Lock()
	same := bytes.Equal(body, r.firewallWritten)
	r.mu.Unlock()
	if same {
		return
	}
	file := filepath.Join(r.dataDir, fwsync.DesiredFile)
	if cur, err := os.ReadFile(file); err != nil || !bytes.Equal(cur, body) {
		if err := writeFile(file, body); err != nil {
			r.log.Warn("the tunnels' ports could not be written for the firewall helper; "+
				"with ufw or firewalld on, open them by hand", "error", err)
			return
		}
	}
	r.mu.Lock()
	r.firewallWritten = body
	r.mu.Unlock()
}

// writeFile replaces a file whole, so the helper never reads half of one.
func writeFile(file string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
