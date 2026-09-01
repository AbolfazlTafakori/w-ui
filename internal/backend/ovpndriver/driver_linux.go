//go:build linux

package ovpndriver

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
)

const (
	// cmdTimeout bounds a management-interface exchange. The reconciler must not
	// stall on a server that has stopped answering.
	cmdTimeout = 10 * time.Second

	// startupGrace is how long the process is given to either bind its port or
	// fail. OpenVPN writes its pid file once it is past that point.
	startupGrace = 5 * time.Second
)

// DataRoot is where interface directories are created. It is a variable so the
// panel can point it at its own data directory.
var DataRoot = "/var/lib/wui"

// Driver manages one OpenVPN server process.
type Driver struct {
	log *slog.Logger

	mu     sync.Mutex
	iface  *model.Interface
	layout ovpnconf.Layout
	// byName maps a username to the account it belongs to, so sessions read
	// from the management interface can be attributed without a database query.
	byName map[string]uint
}

// New builds an unopened driver.
func New() *Driver {
	return &Driver{log: slog.Default(), byName: map[string]uint{}}
}

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return model.ProtocolOpenVPN }

// Open lays out the interface's files and makes sure the server is running.
//
// It is written to be safe on an interface that is already up and carrying
// traffic. The process is only restarted when its configuration actually
// changed, because a restart disconnects every customer on the interface.
func (d *Driver) Open(ctx context.Context, iface *model.Interface) error {
	if iface.Protocol != model.ProtocolOpenVPN {
		return fmt.Errorf("%w: %s is %s", backend.ErrWrongProtocol, iface.Name, iface.Protocol)
	}
	if _, err := exec.LookPath("openvpn"); err != nil {
		return ErrNoBinary
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return ErrNoTun
	}
	if iface.OpenVPN.V.CACert == "" || iface.OpenVPN.V.ServerKey == "" {
		return fmt.Errorf("ovpndriver: interface %s has no certificates; recreate it", iface.Name)
	}

	layout := ovpnconf.NewLayout(DataRoot, iface.Name)

	d.mu.Lock()
	d.iface = iface
	d.layout = layout
	d.mu.Unlock()

	if err := d.writeInterfaceFiles(iface, layout); err != nil {
		return err
	}
	if err := d.ensureRunning(ctx, iface, layout); err != nil {
		return err
	}

	d.log.Info("openvpn interface ready",
		"interface", iface.Name, "port", iface.ListenPort,
		"transport", iface.OpenVPN.V.Transport)
	return nil
}

// writeInterfaceFiles lays down everything that depends on the interface rather
// than on its accounts.
func (d *Driver) writeInterfaceFiles(iface *model.Interface, l ovpnconf.Layout) error {
	if err := os.MkdirAll(l.ClientDir(), 0o700); err != nil {
		return fmt.Errorf("ovpndriver: create %s: %w", l.ClientDir(), err)
	}

	p := iface.OpenVPN.V
	// The server key and the tls-crypt key are both secrets that let the holder
	// impersonate this server, so they are written no wider than the account
	// running the panel.
	files := []struct {
		path string
		body string
		mode os.FileMode
	}{
		{l.CACert(), p.CACert + "\n", 0o644},
		{l.ServerCert(), p.ServerCert + "\n", 0o644},
		{l.ServerKey(), p.ServerKey + "\n", 0o600},
		{l.TLSCryptKey(), strings.TrimSpace(p.TLSCryptKey) + "\n", 0o600},
		{l.AuthScript(), ovpnconf.RenderAuthScript(l), 0o700},
	}
	for _, f := range files {
		if err := writeFileAtomic(f.path, f.body, f.mode); err != nil {
			return err
		}
	}

	conf, err := ovpnconf.RenderServer(iface, l)
	if err != nil {
		return err
	}
	return writeFileAtomic(l.ServerConf(), conf, 0o600)
}

// ensureRunning starts the server, adopts an already-running one, or restarts it
// if its configuration changed.
func (d *Driver) ensureRunning(ctx context.Context, iface *model.Interface, l ovpnconf.Layout) error {
	want, err := configFingerprint(l.ServerConf())
	if err != nil {
		return err
	}
	stampPath := filepath.Join(l.Dir, "config.fingerprint")

	pid, alive := runningPID(l.PIDFile())
	if alive {
		have, _ := os.ReadFile(stampPath)
		if strings.TrimSpace(string(have)) == want {
			// Already running the configuration we want. Leaving it alone is
			// the difference between a panel restart being invisible and it
			// disconnecting every customer on the interface.
			d.log.Info("adopted the running openvpn server",
				"interface", iface.Name, "pid", pid)
			return nil
		}
		d.log.Info("openvpn configuration changed; restarting",
			"interface", iface.Name, "pid", pid)
		stop(pid)
	}

	if err := d.start(ctx, iface, l); err != nil {
		return err
	}
	return writeFileAtomic(stampPath, want+"\n", 0o600)
}

// start launches the server detached from the panel.
//
// Setsid puts it in its own session, so it survives the panel exiting. That is
// deliberate: upgrading or restarting the panel should not disconnect paying
// customers, and the pid file lets the next run adopt the process it left.
func (d *Driver) start(ctx context.Context, iface *model.Interface, l ovpnconf.Layout) error {
	_ = os.Remove(l.Management())

	cmd := exec.Command("openvpn",
		"--config", l.ServerConf(),
		"--daemon",
		"--writepid", l.PIDFile(),
	)
	cmd.Dir = l.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s: %v: %s",
			ErrStartFailed, iface.Name, err, strings.TrimSpace(string(out)))
	}

	// --daemon returns as soon as it has forked, which is before the port is
	// bound. Waiting for the management socket means a failure to bind is
	// reported here rather than as a customer who cannot connect.
	deadline := time.Now().Add(startupGrace)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(l.Management()); err == nil {
			pid, _ := runningPID(l.PIDFile())
			d.log.Info("started openvpn", "interface", iface.Name, "pid", pid)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	tail := lastLines(l.LogFile(), 5)
	return fmt.Errorf("%w: %s did not come up within %s: %s",
		ErrStartFailed, iface.Name, startupGrace, tail)
}

// Sync makes the server hold exactly the given accounts.
func (d *Driver) Sync(ctx context.Context, desired []backend.DesiredAccount) (backend.SyncReport, error) {
	d.mu.Lock()
	iface := d.iface
	l := d.layout
	d.mu.Unlock()

	if iface == nil {
		return backend.SyncReport{}, backend.ErrNotOpen
	}

	want := toAccountSet(desired)

	have, err := d.currentAssignments(l)
	if err != nil {
		return backend.SyncReport{}, err
	}
	dd := computeDiff(want, have)

	added := 0
	for _, a := range dd.write {
		if _, existed := have[a.Username]; !existed {
			added++
		}
	}
	unchanged := len(want) - len(dd.write)

	// The credential file is written whole. It is small, it is read on every
	// login rather than held open, and rewriting it entirely means a removed
	// customer cannot survive as a stale line.
	credentials := ovpnconf.RenderCredentials(toRenderAccounts(want))
	if err := writeFileIfChanged(l.Credentials(), credentials, 0o600); err != nil {
		return backend.SyncReport{}, err
	}

	for _, a := range dd.write {
		body, err := ovpnconf.RenderClientConfig(a, iface.Subnet)
		if err != nil {
			d.log.Warn("skipping an account with an unusable address",
				"username", a.Username, "error", err)
			continue
		}
		if err := writeFileAtomic(filepath.Join(l.ClientDir(), a.Username), body, 0o600); err != nil {
			return backend.SyncReport{}, err
		}
	}

	for _, username := range dd.remove {
		if err := os.Remove(filepath.Join(l.ClientDir(), username)); err != nil && !os.IsNotExist(err) {
			return backend.SyncReport{}, fmt.Errorf("ovpndriver: remove %s: %w", username, err)
		}
	}

	// Removing the credential only stops the next login. A customer who is
	// already connected stays connected until their session is cut, which for a
	// customer who has just run out of data is the whole point.
	for _, username := range dd.remove {
		if err := d.kill(ctx, l, username); err != nil {
			d.log.Warn("could not disconnect a removed account",
				"username", username, "error", err)
		}
	}

	d.rememberNames(want)
	return dd.report(unchanged, added), nil
}

// currentAssignments reads the address directory back.
func (d *Driver) currentAssignments(l ovpnconf.Layout) (map[string]string, error) {
	entries, err := os.ReadDir(l.ClientDir())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("ovpndriver: read %s: %w", l.ClientDir(), err)
	}

	files := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(l.ClientDir(), e.Name()))
		if err != nil {
			continue
		}
		files[e.Name()] = string(body)
	}
	return parseClientConfigs(files), nil
}

func (d *Driver) rememberNames(want accountSet) {
	names := make(map[string]uint, len(want))
	for username, a := range want {
		names[username] = a.ID
	}
	d.mu.Lock()
	d.byName = names
	d.mu.Unlock()
}

// Stats reports what the management interface says about connected clients.
//
// These counters are per session and reset on reconnect. They are used for
// presence and for the sharing detector, not for billing: billing is done from
// the nftables counters attached to each account's address, which survive
// reconnects and cannot be reset by the customer.
func (d *Driver) Stats(ctx context.Context) ([]backend.Stat, error) {
	d.mu.Lock()
	l := d.layout
	names := make(map[string]uint, len(d.byName))
	for k, v := range d.byName {
		names[k] = v
	}
	open := d.iface != nil
	d.mu.Unlock()

	if !open {
		return nil, backend.ErrNotOpen
	}

	raw, err := d.management(ctx, l, "status 2\n")
	if err != nil {
		return nil, err
	}

	sessions := parseStatus(raw)
	out := make([]backend.Stat, 0, len(sessions))
	for _, s := range sessions {
		id, known := names[s.Username]
		if !known {
			// A session for a credential the panel does not recognise. It is
			// reported rather than silently dropped, because the next Sync
			// removing it is the correct outcome and worth being able to see.
			d.log.Warn("connected session for an unknown account", "username", s.Username)
			continue
		}
		out = append(out, backend.Stat{
			AccountID: id,
			RX:        s.RX,
			TX:        s.TX,
			// OpenVPN has real sessions, so presence is known rather than
			// inferred. Reporting connection time as the last handshake lets
			// the panel treat both protocols the same way.
			LastHandshake: time.Unix(s.Since, 0),
			Endpoint:      s.RealIP,
		})
	}
	return out, nil
}

// Kick disconnects an account's session.
func (d *Driver) Kick(ctx context.Context, accountID uint) error {
	d.mu.Lock()
	l := d.layout
	open := d.iface != nil
	var username string
	for name, id := range d.byName {
		if id == accountID {
			username = name
			break
		}
	}
	d.mu.Unlock()

	if !open {
		return backend.ErrNotOpen
	}
	if username == "" {
		return backend.ErrUnknownAcct
	}
	return d.kill(ctx, l, username)
}

func (d *Driver) kill(ctx context.Context, l ovpnconf.Layout, username string) error {
	cmd, err := killCommand(username)
	if err != nil {
		return err
	}
	_, err = d.management(ctx, l, cmd)
	return err
}

// Render produces the customer's .ovpn file.
func (d *Driver) Render(_ context.Context, acc *model.Account, iface *model.Interface) (backend.ClientProfile, error) {
	return backend.ClientProfile{
		Filename: fmt.Sprintf("%s.ovpn", acc.DeviceName),
		MIMEType: "application/x-openvpn-profile",
		Body:     []byte(ovpnconf.RenderClient(acc, iface)),
	}, nil
}

// Health reports whether the server process is answering.
func (d *Driver) Health(ctx context.Context) error {
	d.mu.Lock()
	l := d.layout
	open := d.iface != nil
	d.mu.Unlock()

	if !open {
		return backend.ErrNotOpen
	}
	if pid, alive := runningPID(l.PIDFile()); !alive {
		return fmt.Errorf("ovpndriver: the server process is not running (last pid %d)", pid)
	}
	_, err := d.management(ctx, l, "pid\n")
	return err
}

// Close releases driver resources. The server keeps running: customers should
// not be disconnected because the panel is restarting.
func (d *Driver) Close() error {
	d.mu.Lock()
	d.iface = nil
	d.mu.Unlock()
	return nil
}

// management runs one command against the management socket and returns the
// reply.
func (d *Driver) management(ctx context.Context, l ovpnconf.Layout, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", l.Management())
	if err != nil {
		return "", fmt.Errorf("ovpndriver: management socket: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if _, err := conn.Write([]byte(command)); err != nil {
		return "", fmt.Errorf("ovpndriver: write %q: %w", strings.TrimSpace(command), err)
	}

	// The management protocol has no length prefix and no single terminator:
	// each command ends with its own marker. Reading until one of them appears
	// avoids blocking until the deadline on every call.
	var b strings.Builder
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		b.WriteString(line)
		b.WriteByte('\n')

		if strings.HasPrefix(line, "END") ||
			strings.HasPrefix(line, "SUCCESS:") ||
			strings.HasPrefix(line, "ERROR:") {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return b.String(), fmt.Errorf("ovpndriver: read reply: %w", err)
	}
	return b.String(), nil
}

// runningPID reads the pid file and reports whether that process is alive.
func runningPID(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	// Signal 0 performs the permission and existence checks without delivering
	// anything, which is the standard way to ask whether a pid is live.
	if err := syscall.Kill(pid, 0); err != nil {
		return pid, false
	}
	return pid, true
}

// stop ends a running server and waits for it to go.
func stop(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// configFingerprint hashes the server configuration, so a restart happens when
// the configuration actually changed and not merely because it was rewritten.
func configFingerprint(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ovpndriver: fingerprint %s: %w", path, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// writeFileAtomic writes through a temporary file and renames it into place, so
// a reader never sees a half-written file. OpenVPN reads several of these while
// running.
func writeFileAtomic(path, body string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ovpndriver: create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".wui-*")
	if err != nil {
		return fmt.Errorf("ovpndriver: temp file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("ovpndriver: chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return fmt.Errorf("ovpndriver: write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ovpndriver: close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("ovpndriver: install %s: %w", path, err)
	}
	return nil
}

// writeFileIfChanged avoids rewriting a file whose contents already match.
func writeFileIfChanged(path, body string, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == body {
		return nil
	}
	return writeFileAtomic(path, body, mode)
}

// lastLines returns the tail of a log file, for error messages that would
// otherwise say only that something failed.
func lastLines(path string, n int) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "(no log)"
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
