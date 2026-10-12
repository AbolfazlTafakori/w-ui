//go:build linux

package l2tpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abolfazl/w-ui/internal/occonf"
	"github.com/abolfazl/w-ui/internal/radiusd"
)

// The OpenConnect tunnel on Linux: ocserv, from files of the agent's own in
// occonf.ConfDir, started and stopped by the agent and left running across
// the agent's own restarts -- an update must not disconnect anyone.

func ocPath(name string) string { return filepath.Join(occonf.ConfDir, name) }

func (s *linuxSystem) WriteOcserv(files []occonf.File) (changed, certOnly bool, err error) {
	if err := os.MkdirAll(occonf.ConfDir, 0o700); err != nil {
		return false, false, err
	}
	onlyCert := true
	for _, f := range files {
		p := ocPath(f.Name)
		old, rerr := os.ReadFile(p)
		if rerr == nil && bytes.Equal(old, []byte(f.Body)) {
			continue
		}
		changed = true
		if f.Name != occonf.CertFile && f.Name != occonf.KeyFile {
			onlyCert = false
		}
		mode := os.FileMode(0o644)
		if f.Secret {
			mode = 0o600
		}
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, []byte(f.Body), mode); err != nil {
			return false, false, err
		}
		if err := os.Rename(tmp, p); err != nil {
			return false, false, err
		}
	}
	return changed, changed && onlyCert, nil
}

var authserverLine = regexp.MustCompile(`(?m)^authserver 127\.0\.0\.1:\d+:([0-9a-f]{16,})$`)

func (s *linuxSystem) RadiusSecret() string {
	raw, err := os.ReadFile(ocPath(occonf.RadcliFile))
	if err != nil {
		return ""
	}
	if m := authserverLine.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

// ocservPID is the agent's ocserv, when it is running: the pid in its own
// pid file, alive, and an ocserv. Its command line is no help -- ocserv
// renames its main process "ocserv-main" -- so it is known by its program,
// and by its pid file and occtl socket, which are the agent's alone.
func ocservPID() int {
	raw, err := os.ReadFile(occonf.PIDFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return 0
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil || filepath.Base(strings.TrimSuffix(exe, " (deleted)")) != "ocserv" {
		return 0
	}
	return pid
}

func (s *linuxSystem) occtl(ctx context.Context, args ...string) ([]byte, error) {
	return s.run(ctx, cmdTimeout, "", "occtl", append([]string{"-s", occonf.OcctlSocket}, args...)...)
}

func (s *linuxSystem) OcservRunning(ctx context.Context) bool {
	if ocservPID() == 0 {
		return false
	}
	_, err := s.occtl(ctx, "show", "status")
	return err == nil
}

func (s *linuxSystem) StartOcserv(ctx context.Context) error {
	conf := ocPath(occonf.ConfFile)
	// Checked before it is started: a configuration ocserv would refuse is
	// said so, with ocserv's own words.
	if out, err := s.run(ctx, cmdTimeout, "", "ocserv", "-t", "-c", conf); err != nil {
		return fmt.Errorf("ocserv refused its configuration: %s", tail(out))
	}
	if pid := ocservPID(); pid != 0 {
		// Up but not answering: replaced.
		s.stopPID(pid)
	}
	if out, err := s.run(ctx, cmdTimeout, "", "ocserv", "-c", conf); err != nil {
		return fmt.Errorf("%v: %s", err, tail(out))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.OcservRunning(ctx) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("ocserv did not come up; is the port free? %s", s.ocservLogTail(ctx))
}

func (s *linuxSystem) ocservLogTail(ctx context.Context) string {
	out, _ := s.run(ctx, cmdTimeout, "", "journalctl", "-t", "ocserv", "-n", "5", "--no-pager", "-o", "cat")
	return tail(out)
}

func (s *linuxSystem) stopPID(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for range 50 {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func (s *linuxSystem) RestartOcserv(ctx context.Context) error {
	if out, err := s.run(ctx, cmdTimeout, "", "ocserv", "-t", "-c", ocPath(occonf.ConfFile)); err != nil {
		// The running one is kept: a refused change must not take the tunnel
		// down.
		return fmt.Errorf("ocserv refused its new configuration, the running one is kept: %s", tail(out))
	}
	if pid := ocservPID(); pid != 0 {
		s.stopPID(pid)
	}
	return s.StartOcserv(ctx)
}

func (s *linuxSystem) ReloadOcserv(ctx context.Context) error {
	_, err := s.occtl(ctx, "reload")
	return err
}

func (s *linuxSystem) RemoveOcserv(ctx context.Context) error {
	if pid := ocservPID(); pid != 0 {
		s.stopPID(pid)
	}
	return os.RemoveAll(occonf.ConfDir)
}

// occtlUser is one session as `occtl --json show users` gives it.
type occtlUser struct {
	ID       int    `json:"ID"`
	Username string `json:"Username"`
	State    string `json:"State"`
	Device   string `json:"Device"`
	IPv4     string `json:"IPv4"`
	Remote   string `json:"Remote IP"`
	RX       string `json:"RX"`
	TX       string `json:"TX"`
}

func (s *linuxSystem) OcservUsers(ctx context.Context) ([]OCUser, error) {
	out, err := s.occtl(ctx, "--json", "show", "users")
	if err != nil {
		return nil, err
	}
	var us []occtlUser
	if err := json.Unmarshal(bytes.TrimSpace(out), &us); err != nil {
		return nil, fmt.Errorf("reading occtl: %w", err)
	}
	return ocUsers(us), nil
}

// ocUsers keeps the sessions that are connected, with their numbers read.
func ocUsers(us []occtlUser) []OCUser {
	var outs []OCUser
	for _, u := range us {
		if u.State != "connected" {
			continue
		}
		ip, _ := netip.ParseAddr(u.IPv4)
		rx, _ := strconv.ParseUint(u.RX, 10, 64)
		tx, _ := strconv.ParseUint(u.TX, 10, 64)
		outs = append(outs, OCUser{ID: u.ID, Username: u.Username, Device: u.Device, IP: ip, Remote: u.Remote, RX: rx, TX: tx})
	}
	return outs
}

func (s *linuxSystem) Disconnect(ctx context.Context, id int) error {
	_, err := s.occtl(ctx, "disconnect", "id", strconv.Itoa(id))
	return err
}

func (s *linuxSystem) Camouflage(ctx context.Context) bool {
	out, _ := s.run(ctx, cmdTimeout, "", "ocserv", "--version")
	return camouflageIn(string(out))
}

func (s *linuxSystem) ServeOCRadius(secret string, lookup radiusd.Lookup, onAccept radiusd.OnAccept) error {
	s.ocRadiusMu.Lock()
	defer s.ocRadiusMu.Unlock()
	if s.ocRadius != nil && s.ocRadiusSecret == secret {
		s.ocRadius.SetLookup(lookup)
		s.ocRadius.SetOnAccept(onAccept)
		return nil
	}
	if s.ocRadius != nil {
		s.ocRadiusStop()
		s.ocRadius = nil
	}
	addr := "127.0.0.1:" + strconv.Itoa(occonf.RadiusPort)
	srv, err := radiusd.Listen(addr, []byte(secret), lookup, s.log.With("part", "openconnect-radius"))
	if err != nil {
		return err
	}
	srv.SetOnAccept(onAccept)
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	s.ocRadius, s.ocRadiusSecret, s.ocRadiusStop = srv, secret, cancel
	return nil
}

func (s *linuxSystem) StopOCRadius() {
	s.ocRadiusMu.Lock()
	defer s.ocRadiusMu.Unlock()
	if s.ocRadius != nil {
		s.ocRadiusStop()
		s.ocRadius = nil
	}
}
