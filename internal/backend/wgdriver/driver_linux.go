//go:build linux

package wgdriver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/wgconf"
)

// cmdTimeout bounds every subprocess. A hung `ip` or `awg` must not stall the
// reconciler, which would stop collection for every other interface too.
const cmdTimeout = 15 * time.Second

// Driver manages one WireGuard or AmneziaWG interface.
type Driver struct {
	log *slog.Logger

	mu      sync.Mutex
	iface   *model.Interface
	ctrl    *wgctrl.Client // nil for AmneziaWG, which netlink does not cover
	amnezia bool
	// byKey maps a peer's public key to the account it belongs to, so stats
	// read back from the kernel can be attributed without a database lookup.
	byKey map[string]uint
	// warned is what has been logged once already, so a warning that holds
	// on every sync is said once and not every few seconds.
	warned map[string]bool

	// AmneziaWG only. Its device is read and written through the awg tool,
	// a process each time, and every write hands the whole configuration
	// to the device -- amneziawg-go reapplies all of it. Done on every tick
	// for every tunnel, that was most of what an AmneziaWG server spent.
	//
	// dump is the last `awg show dump`, read for the counters; the sync in
	// the same tick reads its peers from it instead of asking again.
	// applied is the configuration last written, so an unchanged one is not
	// written again: it is written when a peer differs from what the device
	// holds -- which also catches a device that lost its peers -- or when
	// the configuration itself changed.
	dump    string
	dumpAt  time.Time
	applied [sha256.Size]byte
}

// dumpFresh is how long a dump read for the counters stands for the
// device's peers. The counters and the sync are one tick apart at most, and
// nothing but the sync writes the device in between.
const dumpFresh = 3 * time.Second

func (d *Driver) warnOnce(key, msg string, args ...any) {
	d.mu.Lock()
	if d.warned == nil {
		d.warned = map[string]bool{}
	}
	seen := d.warned[key]
	d.warned[key] = true
	d.mu.Unlock()
	if !seen {
		d.log.Warn(msg, args...)
	}
}

// New builds an unopened driver.
func New() *Driver {
	return &Driver{log: slog.Default(), byKey: map[string]uint{}}
}

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return model.ProtocolWireGuard }

// Open prepares the interface: creates the link if missing, gives it its
// address, key and port, and brings it up.
//
// It is written to be safe on an interface that already exists and is carrying
// traffic. A panel restart must not drop a single customer, so nothing here
// deletes or recreates a working link.
func (d *Driver) Open(ctx context.Context, iface *model.Interface) error {
	if iface.Protocol != model.ProtocolWireGuard {
		return fmt.Errorf("%w: %s is %s", backend.ErrWrongProtocol, iface.Name, iface.Protocol)
	}

	d.mu.Lock()
	d.iface = iface
	d.amnezia = iface.Mode == model.ModeAmnezia
	d.mu.Unlock()

	tool := toolFor(iface)
	if _, err := exec.LookPath(tool); err != nil {
		return fmt.Errorf("%w: %s (install %s)", ErrNoTool, tool,
			map[bool]string{true: "amneziawg", false: "wireguard-tools"}[d.amnezia])
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("%w: ip (install iproute2)", ErrNoTool)
	}

	if err := d.ensureLink(ctx, iface); err != nil {
		return err
	}
	if err := d.configureDevice(ctx, iface); err != nil {
		return err
	}
	if err := d.run(ctx, "ip", "link", "set", "up", "dev", iface.Name); err != nil {
		return fmt.Errorf("%w: bringing %s up: %v", ErrLinkFailed, iface.Name, err)
	}

	if !d.amnezia {
		c, err := wgctrl.New()
		if err != nil {
			return fmt.Errorf("wgdriver: open netlink: %w", err)
		}
		d.mu.Lock()
		d.ctrl = c
		d.mu.Unlock()
	}

	d.log.Info("wireguard interface ready",
		"interface", iface.Name, "mode", iface.Mode, "port", iface.ListenPort)
	return nil
}

// ensureLink creates the interface if it is not already there.
func (d *Driver) ensureLink(ctx context.Context, iface *model.Interface) error {
	exists := d.run(ctx, "ip", "link", "show", "dev", iface.Name) == nil

	if exists {
		// A device with this name already existed. Names like wg0 are the
		// convention rather than the exception, so it may well belong to
		// something else on this machine — and the next few lines would
		// overwrite its key, its port and its entire peer list, taking that
		// deployment down without a word.
		if err := d.assertOurs(ctx, iface); err != nil {
			return err
		}
	} else {
		err := d.run(ctx, "ip", "link", "add", "dev", iface.Name,
			"type", linkType(iface))
		switch {
		case err == nil:
			d.log.Info("created interface", "interface", iface.Name, "type", linkType(iface))
		case iface.Mode == model.ModeAmnezia:
			// No amneziawg module on this kernel. There is a userspace
			// implementation for exactly this, and refusing here would leave
			// the panel unable to offer obfuscation on any machine whose
			// kernel is newer than the module's packages.
			if err := d.startUserspace(ctx, iface, err); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: creating %s (%s): %v",
				ErrLinkFailed, iface.Name, linkType(iface), err)
		}
	}

	gw, bits, err := wgconf.Gateway(iface.Subnet)
	if err != nil {
		return err
	}
	// `addr replace` rather than `add`: re-running must not fail on an address
	// that is already there, and must still correct one that has changed.
	if err := d.run(ctx, "ip", "addr", "replace",
		fmt.Sprintf("%s/%d", gw, bits), "dev", iface.Name); err != nil {
		return fmt.Errorf("%w: addressing %s: %v", ErrLinkFailed, iface.Name, err)
	}
	if iface.MTU > 0 {
		if err := d.run(ctx, "ip", "link", "set", "mtu",
			fmt.Sprint(iface.MTU), "dev", iface.Name); err != nil {
			d.log.Warn("could not set MTU", "interface", iface.Name, "error", err)
		}
	}
	return nil
}

// assertOurs refuses to take over a tunnel this panel did not create.
//
// Ownership is decided by the public key, which is the one thing about a
// WireGuard device that is both unique and already known to us. A device
// carrying a key we have never issued belongs to somebody else.
//
// A device with no key at all is treated as ours: that is what a freshly
// created link looks like, and what one left behind by a crash looks like too.
func (d *Driver) assertOurs(ctx context.Context, iface *model.Interface) error {
	out, err := d.output(ctx, toolFor(iface), "show", iface.Name, "public-key")
	if err != nil {
		// Not a WireGuard device at all, or unreadable. Either way it is not
		// something to start writing peers into.
		return fmt.Errorf("%w: %s already exists and is not a tunnel this panel "+
			"can manage; give the interface a different name", ErrLinkFailed, iface.Name)
	}

	existing := strings.TrimSpace(out)
	switch {
	case existing == "" || existing == "(none)":
		return nil // freshly created, or left over from a crash
	case existing == strings.TrimSpace(iface.PublicKey):
		return nil // ours, from a previous run
	}

	return fmt.Errorf("%w: %s already exists and belongs to another WireGuard "+
		"configuration (its public key is not one this panel issued). Refusing to "+
		"take it over; give this interface a different name",
		ErrLinkFailed, iface.Name)
}

// configureDevice sets the private key, the listen port and, for AmneziaWG, the
// obfuscation parameters.
func (d *Driver) configureDevice(ctx context.Context, iface *model.Interface) error {
	if d.amnezia {
		// The obfuscation values are interface-wide and unknown to the
		// WireGuard netlink API, so the whole device is configured from a file
		// through awg instead -- with the peers it already holds. A file
		// without them would remove every customer from a device that was
		// carrying them, on every restart of the panel, until the next sync
		// put them back with fresh handshakes.
		d.forgetApplied()
		return d.syncConfFile(ctx, iface, d.heldPeers(ctx, iface))
	}

	key, err := wgtypes.ParseKey(iface.PrivateKey)
	if err != nil {
		return fmt.Errorf("wgdriver: interface private key: %w", err)
	}
	port := iface.ListenPort

	c, err := wgctrl.New()
	if err != nil {
		return fmt.Errorf("wgdriver: open netlink: %w", err)
	}
	defer c.Close()

	if err := c.ConfigureDevice(iface.Name, wgtypes.Config{
		PrivateKey: &key,
		ListenPort: &port,
	}); err != nil {
		return fmt.Errorf("wgdriver: configure %s: %w", iface.Name, err)
	}
	return nil
}

// syncConfFile writes a full configuration and applies it with awg syncconf.
//
// syncconf, not setconf: setconf tears down and recreates every peer, which
// drops all live sessions. syncconf changes only what differs, so customers who
// were not touched keep their connection.
func (d *Driver) syncConfFile(ctx context.Context, iface *model.Interface, peers []wgconf.Peer) error {
	text := wgconf.RenderServer(iface, peers)
	sum := sha256.Sum256([]byte(text))
	err := d.applyConf(ctx, iface, text)
	if err == nil {
		d.markApplied(sum)
		return nil
	}
	p := iface.AWG.V
	hasSpecials := p.I1 != "" || p.I2 != "" || p.I3 != "" || p.I4 != "" || p.I5 != ""
	if hasSpecials {
		// An AmneziaWG on this server older than I1-I5 refuses the whole
		// file over them. They are only ever the sender's -- customers'
		// devices send theirs either way -- so the tunnel is carried
		// without them rather than not at all.
		if err2 := d.applyConf(ctx, iface, wgconf.RenderServerPlain(iface, peers)); err2 == nil {
			d.warnOnce(iface.Name+"/specials", "this server's AmneziaWG does not know I1-I5; the tunnel runs without the server sending them -- update amneziawg to have it send them too",
				"interface", iface.Name)
			// Marked as the full file: that is what was meant, and the
			// device takes no more of it however often it is sent.
			d.markApplied(sum)
			return nil
		}
	}
	if p.UsesAWG2() {
		return fmt.Errorf("%w (the profile uses AmneziaWG 1.5+ settings -- identifier ranges or I1-I5 -- which this server's amneziawg may be too old for; update it)", err)
	}
	return err
}

// applyConf writes a full configuration and applies it with awg syncconf.
func (d *Driver) applyConf(ctx context.Context, iface *model.Interface, text string) error {

	// 0600 because the file carries the server's private key for as long as it
	// exists, which is a few milliseconds but still on disk.
	f, err := os.CreateTemp("", "wui-*.conf")
	if err != nil {
		return fmt.Errorf("wgdriver: temp config: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("wgdriver: secure temp config: %w", err)
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return fmt.Errorf("wgdriver: write temp config: %w", err)
	}
	f.Close()

	if err := d.run(ctx, "awg", "syncconf", iface.Name, f.Name()); err != nil {
		return fmt.Errorf("wgdriver: awg syncconf %s: %w", iface.Name, err)
	}
	return nil
}

// Sync makes the interface hold exactly the given accounts.
func (d *Driver) Sync(ctx context.Context, desired []backend.DesiredAccount) (backend.SyncReport, error) {
	d.mu.Lock()
	iface := d.iface
	amnezia := d.amnezia
	ctrl := d.ctrl
	d.mu.Unlock()

	if iface == nil {
		return backend.SyncReport{}, backend.ErrNotOpen
	}

	want := toPeerSet(desired)

	have, err := d.currentPeers(ctx, iface, ctrl)
	if err != nil {
		return backend.SyncReport{}, err
	}
	dd := computeDiff(want, have)
	unchanged := len(want) - len(dd.add) - len(dd.update)

	if amnezia {
		peers := make([]wgconf.Peer, 0, len(want))
		for _, a := range want {
			peers = append(peers, wgconf.Peer{
				PublicKey:    a.PublicKey,
				PresharedKey: a.PresharedKey,
				AllowedIP:    allowedIP(a),
			})
		}
		// In one order, so the same peers are the same file.
		sort.Slice(peers, func(i, j int) bool { return peers[i].PublicKey < peers[j].PublicKey })
		// Written only when something differs: a peer the device does not
		// hold as wanted, or a configuration not yet written.
		if dd.empty() && d.isApplied(sha256.Sum256([]byte(wgconf.RenderServer(iface, peers)))) {
			d.rememberKeys(want)
			return dd.report(unchanged), nil
		}
		if err := d.syncConfFile(ctx, iface, peers); err != nil {
			return backend.SyncReport{}, err
		}
	} else {
		if err := d.applyNetlink(iface.Name, ctrl, dd); err != nil {
			return backend.SyncReport{}, err
		}
	}

	d.rememberKeys(want)
	return dd.report(unchanged), nil
}

// applyNetlink applies the diff in a single netlink transaction.
//
// Additions, address changes and removals go together, so there is no window in
// which a removed customer is still connected or a new one is not yet allowed.
// Peers that did not change are not mentioned at all and keep their sessions.
func (d *Driver) applyNetlink(name string, c *wgctrl.Client, dd diff) error {
	if c == nil {
		return backend.ErrNotOpen
	}

	peers := peerConfigs(dd)
	if peers == nil {
		return nil
	}
	if want := len(dd.add) + len(dd.update) + len(dd.remove); len(peers) != want {
		d.log.Warn("some peers were skipped because their keys could not be parsed",
			"interface", name, "skipped", want-len(peers))
	}
	if len(peers) == 0 {
		// Nothing changed. Writing an empty config would be harmless, but not
		// touching the device at all is the point of the diff.
		return nil
	}

	// ReplacePeers stays false: unchanged peers keep their live sessions.
	return c.ConfigureDevice(name, wgtypes.Config{Peers: peers})
}

// currentPeers reads the public keys the interface holds and the address each
// is allowed to use.
func (d *Driver) currentPeers(ctx context.Context, iface *model.Interface, c *wgctrl.Client) (map[string]string, error) {
	out := map[string]string{}

	if d.amnezia {
		raw, err := d.readDump(ctx, iface, true)
		if err != nil {
			// A freshly created interface has no peers to report; that is not a
			// failure, it is the starting state.
			return out, nil
		}
		lines := strings.Split(strings.TrimSpace(raw), "\n")
		for i, line := range lines {
			if i == 0 {
				continue // the first line describes the interface itself
			}
			cols := strings.Split(line, "\t")
			if len(cols) < 4 {
				continue
			}
			ip := strings.TrimSuffix(cols[3], "/32")
			out[cols[0]] = ip
		}
		return out, nil
	}

	if c == nil {
		return out, nil
	}
	dev, err := c.Device(iface.Name)
	if err != nil {
		return out, nil
	}
	for _, p := range dev.Peers {
		ip := ""
		if len(p.AllowedIPs) > 0 {
			ip = p.AllowedIPs[0].IP.String()
		}
		out[p.PublicKey.String()] = ip
	}
	return out, nil
}

// Stats reports what the kernel has seen for each peer.
func (d *Driver) Stats(ctx context.Context) ([]backend.Stat, error) {
	d.mu.Lock()
	iface := d.iface
	ctrl := d.ctrl
	byKey := make(map[string]uint, len(d.byKey))
	for k, v := range d.byKey {
		byKey[k] = v
	}
	d.mu.Unlock()

	if iface == nil {
		return nil, backend.ErrNotOpen
	}

	if d.amnezia {
		return d.statsFromDump(ctx, iface, byKey)
	}
	if ctrl == nil {
		return nil, backend.ErrNotOpen
	}

	dev, err := ctrl.Device(iface.Name)
	if err != nil {
		return nil, fmt.Errorf("wgdriver: read %s: %w", iface.Name, err)
	}

	out := make([]backend.Stat, 0, len(dev.Peers))
	for _, p := range dev.Peers {
		id, ok := byKey[p.PublicKey.String()]
		if !ok {
			continue // a peer we do not own; leave it alone
		}
		s := backend.Stat{
			AccountID:     id,
			RX:            uint64(p.ReceiveBytes),
			TX:            uint64(p.TransmitBytes),
			LastHandshake: p.LastHandshakeTime,
		}
		if p.Endpoint != nil {
			s.Endpoint = p.Endpoint.String()
		}
		out = append(out, s)
	}
	return out, nil
}

// statsFromDump parses `awg show <iface> dump`, the only interface AmneziaWG
// offers for reading peer counters.
func (d *Driver) statsFromDump(ctx context.Context, iface *model.Interface, byKey map[string]uint) ([]backend.Stat, error) {
	raw, err := d.readDump(ctx, iface, false)
	if err != nil {
		return nil, fmt.Errorf("wgdriver: awg show %s: %w", iface.Name, err)
	}

	var out []backend.Stat
	for i, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if i == 0 {
			continue
		}
		// public-key psk endpoint allowed-ips handshake rx tx keepalive
		cols := strings.Split(line, "\t")
		if len(cols) < 8 {
			continue
		}
		id, ok := byKey[cols[0]]
		if !ok {
			continue
		}
		s := backend.Stat{AccountID: id}
		if cols[2] != "(none)" {
			s.Endpoint = cols[2]
		}
		if secs := parseInt(cols[4]); secs > 0 {
			s.LastHandshake = time.Unix(secs, 0)
		}
		s.RX = uint64(parseInt(cols[5]))
		s.TX = uint64(parseInt(cols[6]))
		out = append(out, s)
	}
	return out, nil
}

// Kick is not meaningful for WireGuard.
//
// The protocol has no sessions to terminate: a peer either exists in the
// interface or it does not. Removing it through Sync is the equivalent action,
// and saying so is better than pretending to disconnect someone.
func (d *Driver) Kick(context.Context, uint) error {
	return backend.ErrNotSupported
}

// Render produces the profile for one device.
func (d *Driver) Render(_ context.Context, acc *model.Account, iface *model.Interface) (backend.ClientProfile, error) {
	return backend.ClientProfile{
		Filename: fmt.Sprintf("%s.conf", acc.DeviceName),
		MIMEType: "text/plain; charset=utf-8",
		Body:     []byte(wgconf.RenderClient(acc, iface)),
	}, nil
}

// Health reports whether the interface is present and configured.
func (d *Driver) Health(ctx context.Context) error {
	d.mu.Lock()
	iface := d.iface
	d.mu.Unlock()

	if iface == nil {
		return backend.ErrNotOpen
	}
	if err := d.run(ctx, "ip", "link", "show", "dev", iface.Name); err != nil {
		return fmt.Errorf("wgdriver: interface %s is gone: %w", iface.Name, err)
	}
	return nil
}

// Close releases the netlink handle and leaves the interface up.
//
// Tearing the link down would disconnect every customer the moment the panel
// restarts, turning a routine upgrade into an outage.
func (d *Driver) Close() error {
	d.mu.Lock()
	c := d.ctrl
	d.ctrl = nil
	d.mu.Unlock()

	if c != nil {
		return c.Close()
	}
	return nil
}

// readDump is `awg show <iface> dump`. The counters read it fresh and keep
// it; the sync takes a kept one, once, if it is fresh enough, and asks
// itself otherwise.
func (d *Driver) readDump(ctx context.Context, iface *model.Interface, forSync bool) (string, error) {
	if forSync {
		d.mu.Lock()
		raw, at := d.dump, d.dumpAt
		d.dump, d.dumpAt = "", time.Time{}
		d.mu.Unlock()
		if raw != "" && time.Since(at) < dumpFresh {
			return raw, nil
		}
	}
	raw, err := d.output(ctx, "awg", "show", iface.Name, "dump")
	if err != nil {
		return "", err
	}
	if !forSync {
		d.mu.Lock()
		d.dump, d.dumpAt = raw, time.Now()
		d.mu.Unlock()
	}
	return raw, nil
}

// heldPeers is what the device holds now, as peers for a configuration
// file: key, preshared key and address, as the dump shows them.
func (d *Driver) heldPeers(ctx context.Context, iface *model.Interface) []wgconf.Peer {
	raw, err := d.output(ctx, "awg", "show", iface.Name, "dump")
	if err != nil {
		return nil
	}
	return peersFromDump(raw)
}

// peersFromDump reads the peers of an `awg show dump`: the first line is
// the device; each after it is public-key, preshared-key, endpoint,
// allowed-ips and the counters.
func peersFromDump(raw string) []wgconf.Peer {
	var out []wgconf.Peer
	for i, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if i == 0 {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 4 || cols[0] == "" {
			continue
		}
		p := wgconf.Peer{PublicKey: cols[0], AllowedIP: cols[3]}
		if cols[1] != "(none)" {
			p.PresharedKey = cols[1]
		}
		if p.AllowedIP == "(none)" || p.AllowedIP == "" {
			// A file cannot say "no address"; the sync adds it back as wanted.
			continue
		}
		out = append(out, p)
	}
	return out
}

func (d *Driver) markApplied(sum [sha256.Size]byte) {
	d.mu.Lock()
	d.applied = sum
	// What was read before the write no longer stands for the device.
	d.dump, d.dumpAt = "", time.Time{}
	d.mu.Unlock()
}

func (d *Driver) forgetApplied() {
	d.mu.Lock()
	d.applied = [sha256.Size]byte{}
	d.mu.Unlock()
}

func (d *Driver) isApplied(sum [sha256.Size]byte) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.applied == sum
}

func (d *Driver) rememberKeys(want peerSet) {
	m := make(map[string]uint, len(want))
	for key, a := range want {
		m[key] = a.ID
	}
	d.mu.Lock()
	d.byKey = m
	d.mu.Unlock()
}

func (d *Driver) run(ctx context.Context, name string, args ...string) error {
	_, err := d.output(ctx, name, args...)
	return err
}

func (d *Driver) output(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		// A command we ran out of patience with reports "signal: killed",
		// which reads as though something went wrong with the tool. It says
		// nothing about the deadline that actually ended it, and sends an
		// operator looking for a crash that did not happen.
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s %s: no answer within %s",
				name, strings.Join(args, " "), cmdTimeout)
		}
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// singleHost is the /32 filter that confines a peer to its own address.
func parseInt(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Destroy removes the device. A userspace amneziawg-go process exits with
// its TUN device; a kernel device simply goes.
func (d *Driver) Destroy(ctx context.Context) error {
	d.mu.Lock()
	iface := d.iface
	d.mu.Unlock()
	if iface == nil {
		return nil
	}
	return RemoveLink(ctx, iface)
}

// RemoveLink deletes the device by name, whoever created it, and the
// amneziawg-go process serving it if there is one.
func RemoveLink(ctx context.Context, iface *model.Interface) error {
	if exec.CommandContext(ctx, "ip", "link", "show", "dev", iface.Name).Run() != nil {
		return nil // already gone
	}
	if out, err := exec.CommandContext(ctx, "ip", "link", "del", "dev", iface.Name).CombinedOutput(); err != nil {
		return fmt.Errorf("wgdriver: remove %s: %v: %s", iface.Name, err, strings.TrimSpace(string(out)))
	}
	// A userspace device dies with its link; the process notices and exits.
	// Any that did not is told to.
	_ = exec.CommandContext(ctx, "pkill", "-f", "^"+userspaceTool+" "+iface.Name+"$").Run()
	return nil
}

func init() {
	backend.RegisterRemover(model.ProtocolWireGuard, RemoveLink)
}
