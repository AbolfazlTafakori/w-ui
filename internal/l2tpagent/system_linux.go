//go:build linux

package l2tpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

const (
	chapSecrets = "/etc/ppp/chap-secrets"
	// swanctlFile is the agent's one file in strongSwan's configuration,
	// beside whatever else is there.
	swanctlFile = "wui-l2tp.conf"

	cmdTimeout     = 30 * time.Second
	installTimeout = 15 * time.Minute
)

// swanctlDirs are where distributions keep strongSwan's swanctl
// configuration: Debian and Ubuntu, then Fedora and the RHEL family.
var swanctlDirs = []string{"/etc/swanctl/conf.d", "/etc/strongswan/swanctl/conf.d"}

// linuxSystem runs the real programs.
type linuxSystem struct {
	log *slog.Logger
}

// NewSystem is the system on this machine.
func NewSystem(log *slog.Logger) System { return &linuxSystem{log: log} }

// NewIPsecSystem is the system the IKEv2 tunnel runs on: the same machine.
func NewIPsecSystem(log *slog.Logger) IPsecSystem { return &linuxSystem{log: log} }

func (s *linuxSystem) run(ctx context.Context, timeout time.Duration, stdin string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, tail(out))
	}
	return out, nil
}

func tail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if len(s) > 400 {
		s = "..." + s[len(s)-400:]
	}
	return s
}

// ── packages ─────────────────────────────────────────────────────────────────

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func (s *linuxSystem) missing(protocol string) []string {
	var out []string
	bins := []string{"xl2tpd", "pppd", "swanctl"}
	switch protocol {
	case l2tpconf.ProtocolPPTP:
		bins = []string{"pptpd", "pppd"}
	case l2tpconf.ProtocolIKEv2:
		bins = []string{"swanctl"}
		for _, p := range ikev2Plugins {
			if !havePlugin(p) {
				out = append(out, "strongSwan's "+p+" plugin")
			}
		}
	}
	for _, bin := range bins {
		if !have(bin) {
			out = append(out, bin)
		}
	}
	if protocol != l2tpconf.ProtocolPPTP {
		if _, err := swanctlDir(); err != nil {
			out = append(out, "strongSwan's swanctl directory")
		}
	}
	return out
}

// packagedUnits are the services the IPsec, L2TP and PPTP packages can bring
// with them, under the names the distributions give them.
var packagedUnits = []string{
	"xl2tpd.service", "strongswan.service", "strongswan-starter.service",
	"strongswan-swanctl.service", "ipsec.service", "pptpd.service",
}

// packages are what a protocol's tunnel is installed from, by family.
func packages(protocol, family string) []string {
	switch {
	case protocol == l2tpconf.ProtocolPPTP:
		return []string{"pptpd", "ppp"}
	case protocol == l2tpconf.ProtocolIKEv2 && family == "debian":
		// The EAP methods and AES-GCM are in packages of their own there.
		return []string{"strongswan-swanctl", "charon-systemd", "libcharon-extauth-plugins",
			"libcharon-extra-plugins", "libstrongswan-standard-plugins"}
	case protocol == l2tpconf.ProtocolIKEv2:
		return []string{"strongswan"}
	case family == "debian":
		return []string{"strongswan-swanctl", "charon-systemd", "xl2tpd", "ppp"}
	}
	return []string{"strongswan", "xl2tpd", "ppp"}
}

// EnsurePackages installs what a protocol's tunnel needs when any of it is
// missing: strongSwan, xl2tpd and pppd for L2TP; pptpd and pppd for PPTP.
//
// The installer and the update script install L2TP's too; this is for a panel
// updated from its own button, or a machine where that install failed, and
// for PPTP, which is installed the first time a PPTP tunnel is made. What it
// installs is not left running for nothing: a packaged xl2tpd or pptpd would
// take the port from the agent's own, and strongSwan is started by the agent
// while a tunnel exists and stopped when it goes.
//
// Nothing already on the machine is touched. A package that would have to be
// removed -- libreswan, say, for another VPN -- stops the install instead, and
// a service that was enabled or running before is left as it was: it is the
// operator's, not ours to switch off.
func (s *linuxSystem) EnsurePackages(ctx context.Context, protocol string) error {
	gone := s.missing(protocol)
	if len(gone) == 0 {
		return nil
	}
	s.log.Info("installing what the tunnel needs", "protocol", protocol, "missing", strings.Join(gone, ", "))
	theirs := map[string]bool{}
	for _, unit := range packagedUnits {
		theirs[unit] = s.inUse(ctx, unit)
	}
	family, id := osFamily()
	switch family {
	case "debian":
		env := append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		upd := exec.CommandContext(ctx, "apt-get", "update", "-qq")
		upd.Env = env
		_ = upd.Run()
		cctx, cancel := context.WithTimeout(ctx, installTimeout)
		defer cancel()
		args := append([]string{"install", "-y", "-qq", "--no-remove"}, packages(protocol, family)...)
		cmd := exec.CommandContext(cctx, "apt-get", args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("apt-get install: %v: %s", err, tail(out))
		}
	case "rhel":
		// strongSwan and xl2tpd come from EPEL on the RHEL family; Fedora
		// carries them itself.
		if id != "fedora" {
			_, _ = s.run(ctx, installTimeout, "", "dnf", "install", "-y", "-q", "epel-release")
		}
		args := append([]string{"install", "-y", "-q"}, packages(protocol, family)...)
		if _, err := s.run(ctx, installTimeout, "", "dnf", args...); err != nil {
			return err
		}
	default:
		return fmt.Errorf("install %s yourself: this distribution (%s) is not one the agent knows how to install for", strings.Join(gone, ", "), id)
	}
	// Installed here, so off until the agent wants them: the packaged xl2tpd
	// is never the one used, and strongSwan is the agent's to start.
	for _, unit := range packagedUnits {
		if !theirs[unit] {
			_, _ = s.run(ctx, cmdTimeout, "", "systemctl", "disable", "--now", unit)
		}
	}
	if left := s.missing(protocol); len(left) > 0 {
		return fmt.Errorf("still missing after installing: %s", strings.Join(left, ", "))
	}
	return nil
}

// osFamily reads /etc/os-release.
func osFamily() (family, id string) {
	raw, _ := os.ReadFile("/etc/os-release")
	var like string
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			id = v
		case "ID_LIKE":
			like = v
		}
	}
	all := " " + id + " " + like + " "
	switch {
	case strings.Contains(all, " debian ") || strings.Contains(all, " ubuntu "):
		return "debian", id
	case strings.Contains(all, " rhel ") || strings.Contains(all, " fedora ") || strings.Contains(all, " centos "):
		return "rhel", id
	}
	return "", id
}

// ── files ────────────────────────────────────────────────────────────────────

// writeAtomic writes through a temporary file in the same directory and
// renames it into place, so nothing ever reads half a file.
func writeAtomic(path, body string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wui-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *linuxSystem) WriteSecrets(protocol string, st *l2tpconf.State) error {
	existing, err := os.ReadFile(chapSecrets)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if st == nil && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	body := l2tpconf.ChapSecrets(string(existing), protocol, st)
	if body == string(existing) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(chapSecrets), 0o755); err != nil {
		return err
	}
	return writeAtomic(chapSecrets, body, 0o600)
}

func (s *linuxSystem) WriteRun(name, body string) error {
	if err := os.MkdirAll(l2tpconf.RunDir, 0o700); err != nil {
		return err
	}
	return writeAtomic(l2tpconf.RunFile(name), body, 0o600)
}

// RemoveRun removes some of the agent's files, and the directory once it is
// empty: the other tunnel's files, while it runs, stay where they are.
func (s *linuxSystem) RemoveRun(names ...string) error {
	for _, n := range names {
		if err := os.Remove(l2tpconf.RunFile(n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	_ = os.Remove(l2tpconf.RunDir) // only when empty
	return nil
}

// ── IPsec ────────────────────────────────────────────────────────────────────

func swanctlDir() (string, error) {
	for _, d := range swanctlDirs {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return d, nil
		}
	}
	return "", errors.New("no swanctl configuration directory; is strongSwan's swanctl installed?")
}

// strongswanUnit is the unit that runs charon with swanctl. Debian's older
// releases call it strongswan-swanctl; everything current calls it strongswan.
func strongswanUnit() string {
	for _, dir := range []string{"/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"} {
		if _, err := os.Stat(filepath.Join(dir, "strongswan.service")); err == nil {
			return "strongswan.service"
		}
		if _, err := os.Stat(filepath.Join(dir, "strongswan-swanctl.service")); err == nil {
			return "strongswan-swanctl.service"
		}
	}
	return "strongswan.service"
}

// inUse reports whether a service is enabled or running.
func (s *linuxSystem) inUse(ctx context.Context, unit string) bool {
	if _, err := s.run(ctx, cmdTimeout, "", "systemctl", "is-enabled", "--quiet", unit); err == nil {
		return true
	}
	return s.active(ctx, unit)
}

func (s *linuxSystem) active(ctx context.Context, unit string) bool {
	_, err := s.run(ctx, cmdTimeout, "", "systemctl", "is-active", "--quiet", unit)
	return err == nil
}

func (s *linuxSystem) LoadIPsec(ctx context.Context, conf string) error {
	dir, err := swanctlDir()
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, swanctlFile), conf, 0o600); err != nil {
		return err
	}
	unit := strongswanUnit()
	if !s.active(ctx, unit) {
		if _, err := s.run(ctx, cmdTimeout, "", "systemctl", "start", unit); err != nil {
			return err
		}
		if err := s.WriteRun(startedMarker, unit+"\n"); err != nil {
			s.log.Warn("could not note that strongSwan was started here", "error", err)
		}
	}
	// charon answers on its control socket a moment after it starts.
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

func (s *linuxSystem) UnloadIPsec(ctx context.Context) error {
	dir, err := swanctlDir()
	if err == nil {
		if err := os.Remove(filepath.Join(dir, swanctlFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	// Stopped only when nothing of the panel's is left in it -- the IKEv2
	// tunnel runs in the same strongSwan -- and the agent started it;
	// otherwise L2TP's connection is taken out and the rest left running.
	return s.releaseCharon(ctx)
}

// ── firewall ─────────────────────────────────────────────────────────────────

func (s *linuxSystem) ApplyFirewall(ctx context.Context, script string) error {
	_, err := s.run(ctx, cmdTimeout, script, "nft", "-f", "-")
	return err
}

// ── daemons ──────────────────────────────────────────────────────────────────

// daemon is how one tunnel's daemon is run.
type daemon struct {
	bin  string
	conf string // its configuration, which also tells the agent's own apart
	pid  string
	log  string
	args func() []string
}

func daemonOf(protocol string) daemon {
	if protocol == l2tpconf.ProtocolPPTP {
		return daemon{
			bin: "pptpd", conf: l2tpconf.RunFile("pptpd.conf"),
			pid: l2tpconf.RunFile("pptpd.pid"), log: l2tpconf.RunFile("pptpd.log"),
			// -f: in the foreground, so the pid the agent records is the one
			// that runs.
			args: func() []string {
				return []string{"-f", "-c", l2tpconf.RunFile("pptpd.conf"), "-p", l2tpconf.RunFile("pptpd.pid")}
			},
		}
	}
	return daemon{
		bin: "xl2tpd", conf: l2tpconf.RunFile("xl2tpd.conf"),
		pid: l2tpconf.RunFile("xl2tpd.pid"), log: l2tpconf.RunFile("xl2tpd.log"),
		args: func() []string {
			return []string{"-D", "-c", l2tpconf.RunFile("xl2tpd.conf"), "-p", l2tpconf.RunFile("xl2tpd.pid"),
				"-C", l2tpconf.RunFile("l2tp-control")}
		},
	}
}

// running is the pid of the agent's own daemon, when it is alive: the pid
// file names it, and the process is that daemon reading the agent's
// configuration -- a recycled pid belonging to something else is not taken
// for it.
func (d daemon) running() (int, bool) {
	raw, err := os.ReadFile(d.pid)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]))
	if err != nil || pid <= 0 {
		return 0, false
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return pid, false
	}
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	if len(args) == 0 || filepath.Base(args[0]) != d.bin || !strings.Contains(string(cmdline), d.conf) {
		return pid, false
	}
	return pid, true
}

func (s *linuxSystem) Running(protocol string) bool {
	_, ok := daemonOf(protocol).running()
	return ok
}

func (s *linuxSystem) Start(ctx context.Context, protocol string) error {
	d := daemonOf(protocol)
	if pid, ok := d.running(); ok {
		s.log.Info("adopted the running "+d.bin, "pid", pid)
		return nil
	}
	bin, err := exec.LookPath(d.bin)
	if err != nil {
		return fmt.Errorf("%s is not installed", d.bin)
	}
	_ = os.Remove(d.pid)
	logf, err := os.OpenFile(d.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, d.args()...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	// Its own session, so it outlives the agent: the agent is restarted on
	// every update, and the sessions the daemon carries must not end with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := writeAtomic(d.pid, strconv.Itoa(cmd.Process.Pid)+"\n", 0o600); err != nil {
		s.log.Warn("could not write the pid file", "daemon", d.bin, "error", err)
	}
	// Waited on, so a daemon that exits is reaped rather than left a zombie
	// for as long as the agent runs.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// A port another program holds, or a configuration the daemon refuses,
	// ends it within a moment; reported here rather than as a customer who
	// cannot connect.
	select {
	case err := <-exited:
		body, _ := os.ReadFile(d.log)
		return fmt.Errorf("%s exited at start (%v): %s", d.bin, err, tail(body))
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.log.Info("started "+d.bin, "pid", cmd.Process.Pid)
	return nil
}

func (s *linuxSystem) Stop(ctx context.Context, protocol string) error {
	d := daemonOf(protocol)
	pid, ok := d.running()
	if !ok {
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	_ = os.Remove(d.pid)
	return nil
}

// ── sessions ─────────────────────────────────────────────────────────────────

// ipAddr is what `ip -j addr` says about one device.
type ipAddr struct {
	IfName   string `json:"ifname"`
	AddrInfo []struct {
		Family  string `json:"family"`
		Local   string `json:"local"`
		Address string `json:"address"`
	} `json:"addr_info"`
}

func (s *linuxSystem) Sessions(gateway netip.Addr) ([]Live, error) {
	out, err := s.run(context.Background(), cmdTimeout, "", "ip", "-j", "-4", "addr", "show")
	if err != nil {
		return nil, err
	}
	return sessionsFrom(out, gateway, readSession)
}

// sessionsFrom picks the PPP sessions of this tunnel out of `ip -j addr`:
// those whose local end is the tunnel's gateway. Everything else about each
// is read by look, so this can be tested without a machine.
func sessionsFrom(ipJSON []byte, gateway netip.Addr, look func(dev string) (pid int, since int64, endpoint string, rx, tx uint64)) ([]Live, error) {
	var devs []ipAddr
	if err := json.Unmarshal(ipJSON, &devs); err != nil {
		return nil, fmt.Errorf("reading ip's answer: %w", err)
	}
	var out []Live
	for _, d := range devs {
		if !strings.HasPrefix(d.IfName, "ppp") {
			continue
		}
		for _, a := range d.AddrInfo {
			local, err1 := netip.ParseAddr(a.Local)
			peer, err2 := netip.ParseAddr(a.Address)
			if err1 != nil || err2 != nil || local != gateway || peer == local {
				continue
			}
			pid, since, endpoint, rx, tx := look(d.IfName)
			if pid <= 0 {
				continue
			}
			out = append(out, Live{
				Session: l2tpconf.Session{Device: d.IfName, Endpoint: endpoint, Since: since, RX: rx, TX: tx},
				IP:      peer,
				PID:     pid,
			})
		}
	}
	return out, nil
}

// readSession reads what the system knows about one PPP device: the pppd
// carrying it from its pid file, when that started, the device's public
// address from the ipparam xl2tpd hands pppd, and the device's counters.
func readSession(dev string) (pid int, since int64, endpoint string, rx, tx uint64) {
	for _, dir := range []string{"/run", "/var/run"} {
		p := filepath.Join(dir, dev+".pid")
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]))
		if info, err := os.Stat(p); err == nil {
			since = info.ModTime().Unix()
		}
		break
	}
	if pid > 0 {
		if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
			endpoint = ipparam(cmdline)
		}
	}
	rx = readCounter(dev, "rx_bytes")
	tx = readCounter(dev, "tx_bytes")
	return
}

// ipparam is the value after "ipparam" in a NUL-separated command line.
func ipparam(cmdline []byte) string {
	args := bytes.Split(bytes.TrimRight(cmdline, "\x00"), []byte{0})
	for i := 0; i+1 < len(args); i++ {
		if string(args[i]) == "ipparam" {
			if a, err := netip.ParseAddr(string(args[i+1])); err == nil {
				return a.String()
			}
		}
	}
	return ""
}

func readCounter(dev, name string) uint64 {
	raw, err := os.ReadFile(filepath.Join("/sys/class/net", dev, "statistics", name))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return n
}

// EndSession stops a pppd, and only a pppd: the pid came from a file, and a
// recycled one must not end something else.
func (s *linuxSystem) EndSession(pid int) error {
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(string(comm)); name != "pppd" {
		return fmt.Errorf("pid %d is %q, not pppd", pid, name)
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
