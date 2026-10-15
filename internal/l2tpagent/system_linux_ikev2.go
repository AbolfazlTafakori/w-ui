//go:build linux

package l2tpagent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/radiusd"
)

// The IKEv2 tunnel on Linux: strongSwan's swanctl files, shared with L2TP's.

// pluginDirs are where distributions put strongSwan's plugins.
var pluginDirs = []string{"/usr/lib/ipsec/plugins", "/usr/lib64/strongswan/plugins", "/usr/lib/strongswan/plugins"}

// ikev2Plugins are the plugins IKEv2's ways of signing in need beyond what
// L2TP's IPsec does: Debian ships them in packages of their own.
var ikev2Plugins = []string{"eap-mschapv2", "eap-tls", "eap-identity", "eap-radius"}

func havePlugin(name string) bool {
	for _, d := range pluginDirs {
		if _, err := os.Stat(filepath.Join(d, "libstrongswan-"+name+".so")); err == nil {
			return true
		}
	}
	return false
}

// swanctlRoot is strongSwan's swanctl directory: conf.d's parent, where
// x509ca, x509 and private are.
func swanctlRoot() (string, error) {
	d, err := swanctlDir()
	if err != nil {
		return "", err
	}
	return filepath.Dir(d), nil
}

// ikev2Dirs are the swanctl directories the tunnel writes into.
var ikev2Dirs = []string{"x509ca", "x509", "private", "conf.d"}

// settingsDir is strongSwan's settings directory beside swanctl's:
// /etc/strongswan.d, or /etc/strongswan/strongswan.d on the RHEL family.
func settingsDir(root string) string {
	if filepath.Base(filepath.Dir(root)) == "strongswan" {
		return filepath.Join(filepath.Dir(root), "strongswan.d")
	}
	return filepath.Join(filepath.Dir(root), "strongswan.d")
}

// fileDir is where a tunnel file goes.
func fileDir(root, dir string) string {
	if dir == ikev2conf.SettingsDir {
		return settingsDir(root)
	}
	return filepath.Join(root, dir)
}

func (s *linuxSystem) WriteSwanctl(files []ikev2conf.File) error {
	root, err := swanctlRoot()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, f := range files {
		dir := fileDir(root, f.Dir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if f.Secret {
			mode = 0o600
		}
		path := filepath.Join(dir, f.Name)
		if err := writeAtomic(path, f.Body, mode); err != nil {
			return err
		}
		keep[path] = true
	}
	// A device gone takes its certificate with it.
	return removeIKEv2Files(root, keep)
}

// removeIKEv2Files removes the tunnel's own files -- by their prefix --
// except those kept.
func removeIKEv2Files(root string, keep map[string]bool) error {
	for _, d := range append(append([]string(nil), ikev2Dirs...), ikev2conf.SettingsDir) {
		entries, err := os.ReadDir(fileDir(root, d))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ikev2conf.Prefix) {
				continue
			}
			path := filepath.Join(fileDir(root, d), e.Name())
			if keep[path] {
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (s *linuxSystem) LoadSwanctl(ctx context.Context) error {
	if err := s.ensureCharon(ctx); err != nil {
		return err
	}
	// The RADIUS settings are strongSwan's own, read again on request.
	if err := s.loadAll(ctx); err != nil {
		return err
	}
	_, err := s.run(ctx, cmdTimeout, "", "swanctl", "--reload-settings")
	return err
}

func (s *linuxSystem) ServeRadius(secret string, lookup radiusd.Lookup) error {
	s.radiusMu.Lock()
	defer s.radiusMu.Unlock()
	if s.radius != nil && s.radiusSecret == secret {
		s.radius.SetLookup(lookup)
		return nil
	}
	if s.radius != nil {
		s.radiusStop()
		s.radius = nil
	}
	addr := net.JoinHostPort(ikev2conf.RadiusHost, strconv.Itoa(ikev2conf.RadiusPort))
	srv, err := radiusd.Listen(addr, []byte(secret), lookup, s.log.With("part", "radius"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	s.radius, s.radiusSecret, s.radiusStop = srv, secret, cancel
	return nil
}

func (s *linuxSystem) StopRadius() {
	s.radiusMu.Lock()
	defer s.radiusMu.Unlock()
	if s.radius != nil {
		s.radiusStop()
		s.radius = nil
	}
}

// ensureCharon starts strongSwan when it is not running, noting that the
// agent did, so the last tunnel to leave stops it again.
//
// A strongSwan that is running but started before IKEv2's plugins were
// installed -- by an L2TP tunnel made first, on a fresh server -- has not
// loaded them, and refuses every sign-in by password ("loading EAP_RADIUS
// method failed"). It is restarted once to load them; L2TP's sessions, if
// any, reconnect.
func (s *linuxSystem) ensureCharon(ctx context.Context) error {
	unit := strongswanUnit()
	if s.active(ctx, unit) {
		out, err := s.run(ctx, cmdTimeout, "", "swanctl", "--stats")
		if err != nil {
			return nil // asked again on the next pass
		}
		if missing := notLoaded(string(out), ikev2Plugins, havePlugin); len(missing) > 0 {
			s.log.Warn("strongSwan started before IKEv2's plugins were installed; restarting it to load them",
				"plugins", strings.Join(missing, ", "))
			if _, err := s.run(ctx, cmdTimeout, "", "systemctl", "restart", unit); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := s.run(ctx, cmdTimeout, "", "systemctl", "start", unit); err != nil {
		return err
	}
	if err := s.WriteRun(startedMarker, unit+"\n"); err != nil {
		s.log.Warn("could not note that strongSwan was started here", "error", err)
	}
	return nil
}

// loadAll has strongSwan load every file, retrying while it starts.
func (s *linuxSystem) loadAll(ctx context.Context) error {
	var last error
	for i := 0; i < 20; i++ {
		if _, last = s.run(ctx, cmdTimeout, "", "swanctl", "--load-all", "--noprompt"); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return last
}

func (s *linuxSystem) RemoveSwanctl(ctx context.Context) error {
	root, err := swanctlRoot()
	if err != nil {
		return nil // no strongSwan, nothing of ours in it
	}
	if err := removeIKEv2Files(root, nil); err != nil {
		return err
	}
	return s.releaseCharon(ctx)
}

// releaseCharon is a tunnel leaving strongSwan: reloaded, so what was taken
// out is unloaded, while anything of the panel's is still in it -- the other
// tunnel's file -- and stopped when nothing is and the agent started it.
func (s *linuxSystem) releaseCharon(ctx context.Context) error {
	unit := strongswanUnit()
	if s.panelFilesLeft() || !fileExists(l2tpconf.RunFile(startedMarker)) {
		if s.active(ctx, unit) {
			return s.loadAll(ctx)
		}
		return nil
	}
	if _, err := s.run(ctx, cmdTimeout, "", "systemctl", "stop", unit); err != nil {
		return err
	}
	return s.RemoveRun(startedMarker)
}

// panelFilesLeft reports whether either tunnel still has its file in
// strongSwan's configuration.
func (s *linuxSystem) panelFilesLeft() bool {
	d, err := swanctlDir()
	if err != nil {
		return false
	}
	return fileExists(filepath.Join(d, swanctlFile)) || fileExists(filepath.Join(d, ikev2conf.ConfFile))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *linuxSystem) SAs(ctx context.Context) ([]IKESA, error) {
	out, err := s.run(ctx, cmdTimeout, "", "swanctl", "--list-sas", "--raw")
	if err != nil {
		return nil, err
	}
	return parseSAs(out), nil
}

func (s *linuxSystem) Terminate(ctx context.Context, id uint) error {
	_, err := s.run(ctx, cmdTimeout, "", "swanctl", "--terminate", "--ike-id", strconv.FormatUint(uint64(id), 10),
		"--force", "--timeout", "3")
	if err != nil {
		return fmt.Errorf("ending session %d: %w", id, err)
	}
	return nil
}

// notLoaded are the plugins installed on disk that the running strongSwan
// has not loaded, read from `swanctl --stats` ("loaded plugins: ...").
func notLoaded(stats string, want []string, installed func(string) bool) []string {
	loaded := map[string]bool{}
	for _, line := range strings.Split(stats, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "loaded plugins:"); ok {
			for _, p := range strings.Fields(rest) {
				loaded[p] = true
			}
		}
	}
	var out []string
	for _, p := range want {
		if installed(p) && !loaded[p] {
			out = append(out, p)
		}
	}
	return out
}
