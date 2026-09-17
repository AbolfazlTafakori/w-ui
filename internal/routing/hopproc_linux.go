//go:build linux

package routing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Hops that a process owns.
//
// A WireGuard hop is a kernel device the panel configures and forgets. An
// OpenVPN hop is an openvpn process holding a tun; an Xray hop is two: xray
// holding the outbound the operator pasted in, listening on a loopback SOCKS
// port, and sing-box holding a tun that hands everything to that port. In
// every case what the policy sees is a device named wuih<id> to route into,
// the same as a WireGuard hop, so the rest of the routing layer does not care.
//
// Nothing here marks packets or touches routes: the marks are put on
// customer packets in prerouting and locally generated traffic never gets
// one, so a hop's own connections leave through the main table on their own.

// Binaries, looked up in PATH and the usual places. Overridable for tests.
var (
	openvpnBinary = "openvpn"
	xrayBinary    = "xray"
	singboxBinary = "sing-box"
)

// HopWorkDir is where process-backed hops keep their config files and logs.
// Set by main from the data directory before the manager runs.
var HopWorkDir = "/var/lib/wui/hops"

// A hop's process(es), and what they were started from.
type hopProc struct {
	kind  string
	cmds  []*exec.Cmd
	dir   string
	since time.Time
}

func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/usr/sbin", "/opt/wui/bin"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not installed on this server", name)
}

// waitDevice waits for a tun to appear and come up, which a process does on
// its own schedule after it starts.
func waitDevice(ctx context.Context, dev string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := net.InterfaceByName(dev); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("routing: %s did not appear within %s", dev, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *HopManager) procDir(dev string) string {
	return filepath.Join(HopWorkDir, dev)
}

// startProc launches one command for a hop with its output going to a log
// file in the hop's directory, and makes sure it dies with the panel.
func startProc(dir, logName, bin string, args ...string) (*exec.Cmd, error) {
	logf, err := os.OpenFile(filepath.Join(dir, logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	// Logs are capped by truncation on start rather than rotation: a hop
	// that flaps for a week would otherwise fill the disk with the same line.
	if st, err := logf.Stat(); err == nil && st.Size() > 4<<20 {
		_ = logf.Truncate(0)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		logf.Close()
	}()
	return cmd, nil
}

func exited(cmd *exec.Cmd) bool {
	return cmd.ProcessState != nil
}

func stopProc(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || exited(cmd) {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for !exited(cmd) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !exited(cmd) {
		_ = cmd.Process.Kill()
	}
}

// lastLogLine is what the process said last, for an error an operator can act on.
func lastLogLine(dir, name string) string {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	defer f.Close()
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	if len(last) > 300 {
		last = last[:300]
	}
	return last
}

// ── OpenVPN ──────────────────────────────────────────────────────────────────

// Profile directives that would let the far side -- or whoever wrote the
// profile -- rewrite this server's routing, run a program on it, or read a
// file from it. Routes pushed by the upstream are refused on the command
// line; the same lines inside the profile itself are dropped here, matched
// on the directive's own word so a tab or a doubled space does not slip one
// past. Anything that names a script, a plugin, a file to include, a user
// to become or a directory to move into is out: an OpenVPN outbound is a
// tunnel, not a place to run things.
var ovpnDropDirectives = map[string]bool{
	"redirect-gateway": true, "route": true, "route-ipv6": true, "route-gateway": true,
	"dhcp-option": true, "dev": true, "dev-type": true, "daemon": true, "log": true,
	"log-append": true, "up": true, "down": true, "up-restart": true, "route-up": true,
	"route-pre-down": true, "ipchange": true, "client-connect": true,
	"client-disconnect": true, "learn-address": true, "tls-verify": true,
	"auth-user-pass-verify": true, "plugin": true, "script-security": true,
	"management": true, "management-client": true, "management-hold": true,
	"management-log-cache": true, "management-query-passwords": true,
	"management-client-auth": true, "management-external-key": true,
	"management-external-cert": true, "writepid": true, "status": true,
	"status-version": true, "auth-user-pass": true, "route-nopull": true,
	"pull-filter": true, "ifconfig": true, "ifconfig-ipv6": true, "setenv": true,
	"setenv-safe": true, "config": true, "cd": true, "chroot": true, "user": true,
	"group": true, "tmp-dir": true, "iproute": true, "askpass": true, "echo": true,
	"crl-verify": true, "ca": true, "cert": true, "key": true, "pkcs12": true,
	"dh": true, "tls-auth": true, "tls-crypt": true, "tls-crypt-v2": true,
	"secret": true, "extra-certs": true, "x509-username-field": true,
}

// ovpnFileDirectives are the ones above that may still appear when the
// material is inline (<ca>...</ca>) rather than a path: the block form is
// kept, the path form is dropped, since a path is a read of this server.
var ovpnInlineBlocks = map[string]bool{
	"ca": true, "cert": true, "key": true, "pkcs12": true, "dh": true,
	"tls-auth": true, "tls-crypt": true, "tls-crypt-v2": true, "secret": true,
	"extra-certs": true, "crl-verify": true,
}

func cleanOpenVPNProfile(profile string) string {
	var b strings.Builder
	inBlock := ""
	for _, line := range strings.Split(strings.ReplaceAll(profile, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		// Inline blocks (<ca>...</ca>) are copied whole, for the material
		// a profile carries; a block of any other name is dropped whole.
		if inBlock != "" {
			if ovpnInlineBlocks[inBlock] {
				b.WriteString(line + "\n")
			}
			if trim == "</"+inBlock+">" {
				inBlock = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "<") && !strings.HasPrefix(trim, "</") && strings.HasSuffix(trim, ">") {
			inBlock = strings.Trim(trim, "<>")
			if ovpnInlineBlocks[inBlock] {
				b.WriteString(line + "\n")
			}
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) > 0 {
			word := strings.TrimLeft(fields[0], "-")
			if ovpnDropDirectives[word] {
				continue
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (m *HopManager) bringOpenVPN(ctx context.Context, s HopSpec) error {
	bin, err := lookPath(openvpnBinary)
	if err != nil {
		return err
	}
	dir := m.procDir(s.Device)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "client.ovpn"), []byte(cleanOpenVPNProfile(s.Config)), 0o600); err != nil {
		return err
	}
	args := []string{
		"--config", "client.ovpn",
		"--dev", s.Device, "--dev-type", "tun",
		// The upstream may push routes and a redirect-gateway; neither is
		// wanted. The policy decides what goes into this device.
		"--route-nopull",
		"--pull-filter", "ignore", "redirect-gateway",
		"--pull-filter", "ignore", "route",
		"--pull-filter", "ignore", "block-outside-dns",
		"--script-security", "0",
		"--nobind",
		"--persist-tun", "--persist-key",
		"--connect-retry", "5", "30",
		"--verb", "3",
	}
	if s.Username != "" {
		if err := os.WriteFile(filepath.Join(dir, "auth.txt"), []byte(s.Username+"\n"+s.Password+"\n"), 0o600); err != nil {
			return err
		}
		args = append(args, "--auth-user-pass", "auth.txt")
	}
	cmd, err := startProc(dir, "openvpn.log", bin, args...)
	if err != nil {
		return fmt.Errorf("routing: start openvpn for %s: %w", s.Device, err)
	}
	if err := waitDevice(ctx, s.Device, 20*time.Second); err != nil {
		stopProc(cmd)
		if last := lastLogLine(dir, "openvpn.log"); last != "" {
			return fmt.Errorf("routing: openvpn for %s: %s", s.Device, last)
		}
		return err
	}
	if s.MTU > 0 {
		_, _ = run(ctx, ipBinary, "", "link", "set", "mtu", fmt.Sprint(s.MTU), "dev", s.Device)
	}
	m.setProc(s.Device, &hopProc{kind: "openvpn", cmds: []*exec.Cmd{cmd}, dir: dir, since: time.Now()})
	m.log.Info("outbound hop is up", "device", s.Device, "kind", "openvpn", "endpoint", s.Endpoint)
	return nil
}

// ── Xray ─────────────────────────────────────────────────────────────────────

// xrayConfig wraps the operator's outbound in the smallest Xray config that
// serves it: one SOCKS inbound on loopback, one outbound.
func xrayConfig(s HopSpec) ([]byte, error) {
	var outbound map[string]any
	if err := json.Unmarshal([]byte(s.Config), &outbound); err != nil {
		return nil, fmt.Errorf("the outbound is not valid JSON: %w", err)
	}
	outbound["tag"] = "out"
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"tag":      "in",
			"listen":   "127.0.0.1",
			"port":     s.SocksPort,
			"protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
		}},
		"outbounds": []any{outbound},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// singboxConfig is the tun that feeds the SOCKS port: no routes of its own,
// no DNS hijack, just a device the policy can send packets into.
func singboxConfig(s HopSpec) ([]byte, error) {
	mtu := s.MTU
	if mtu <= 0 {
		mtu = 1400
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []any{map[string]any{
			"type":           "tun",
			"tag":            "tun",
			"interface_name": s.Device,
			"address":        []string{s.TunAddress},
			"mtu":            mtu,
			"auto_route":     false,
			"strict_route":   false,
			"stack":          "system",
		}},
		"outbounds": []any{map[string]any{
			"type":            "socks",
			"tag":             "proxy",
			"server":          "127.0.0.1",
			"server_port":     s.SocksPort,
			"version":         "5",
			"udp_over_tcp":    false,
			"network":         nil,
			"domain_resolver": nil,
		}},
		"route": map[string]any{"final": "proxy", "auto_detect_interface": false},
	}
	// Keys with nil values are not wanted in the file.
	out := cfg["outbounds"].([]any)[0].(map[string]any)
	delete(out, "network")
	delete(out, "domain_resolver")
	return json.MarshalIndent(cfg, "", "  ")
}

func (m *HopManager) bringXray(ctx context.Context, s HopSpec) error {
	xbin, err := lookPath(xrayBinary)
	if err != nil {
		return err
	}
	sbin, err := lookPath(singboxBinary)
	if err != nil {
		return err
	}
	dir := m.procDir(s.Device)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	xc, err := xrayConfig(s)
	if err != nil {
		return fmt.Errorf("routing: %s: %w", s.Device, err)
	}
	sc, err := singboxConfig(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "xray.json"), xc, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "sing-box.json"), sc, 0o600); err != nil {
		return err
	}

	// Checked before it is run: xray reports a bad outbound clearly on
	// -test, and vaguely, later, from a process that keeps restarting.
	if out, err := run(ctx, xbin, "", "run", "-test", "-config", filepath.Join(dir, "xray.json")); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return fmt.Errorf("routing: xray refused the outbound for %s: %s", s.Device, msg)
	}

	xcmd, err := startProc(dir, "xray.log", xbin, "run", "-config", "xray.json")
	if err != nil {
		return fmt.Errorf("routing: start xray for %s: %w", s.Device, err)
	}
	scmd, err := startProc(dir, "sing-box.log", sbin, "run", "-c", "sing-box.json", "-D", dir)
	if err != nil {
		stopProc(xcmd)
		return fmt.Errorf("routing: start sing-box for %s: %w", s.Device, err)
	}
	if err := waitDevice(ctx, s.Device, 10*time.Second); err != nil {
		stopProc(scmd)
		stopProc(xcmd)
		if last := lastLogLine(dir, "sing-box.log"); last != "" {
			return fmt.Errorf("routing: sing-box for %s: %s", s.Device, last)
		}
		return err
	}
	m.setProc(s.Device, &hopProc{kind: "xray", cmds: []*exec.Cmd{xcmd, scmd}, dir: dir, since: time.Now()})
	m.log.Info("outbound hop is up", "device", s.Device, "kind", "xray", "endpoint", s.Endpoint)
	return nil
}

// ── bookkeeping ──────────────────────────────────────────────────────────────

func (m *HopManager) setProc(dev string, p *hopProc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.procs == nil {
		m.procs = map[string]*hopProc{}
	}
	if old := m.procs[dev]; old != nil {
		for _, c := range old.cmds {
			stopProc(c)
		}
	}
	m.procs[dev] = p
}

// stopProcs ends a hop's processes and forgets them. The tun goes away with
// the process that held it.
func (m *HopManager) stopProcs(dev string) {
	m.mu.Lock()
	p := m.procs[dev]
	delete(m.procs, dev)
	m.mu.Unlock()
	if p == nil {
		return
	}
	for _, c := range p.cmds {
		stopProc(c)
	}
}

// procAlive reports whether every process behind a hop is still running. A
// hop whose process died is brought up again on the next sync rather than
// left as a device-less rule that drops everything.
func (m *HopManager) procAlive(dev string) bool {
	m.mu.Lock()
	p := m.procs[dev]
	m.mu.Unlock()
	if p == nil {
		return false
	}
	for _, c := range p.cmds {
		if exited(c) {
			return false
		}
	}
	return true
}

// HopStatus is what an operator is shown about a process-backed hop.
type HopStatus struct {
	Device string
	Kind   string
	Alive  bool
	Since  time.Time
	Last   string
}

// Statuses reports every process-backed hop.
func (m *HopManager) Statuses() []HopStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]HopStatus, 0, len(m.procs))
	for dev, p := range m.procs {
		st := HopStatus{Device: dev, Kind: p.kind, Alive: true, Since: p.since}
		for _, c := range p.cmds {
			if exited(c) {
				st.Alive = false
			}
		}
		logName := "openvpn.log"
		if p.kind == "xray" {
			logName = "xray.log"
		}
		st.Last = lastLogLine(p.dir, logName)
		out = append(out, st)
	}
	return out
}
