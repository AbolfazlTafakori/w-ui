// Package l2tpdriver is the panel's side of the PPP tunnels: L2TP/IPsec, and
// PPTP, which the same agent runs beside it the same way.
//
// The tunnel itself is run by the root agent (package l2tpagent): pppd only
// runs as root, and the panel does not. This driver describes the tunnel to
// the agent in the panel's data directory, reads back the agent's report of
// who is connected, and asks it to end sessions -- the same contract every
// other protocol's driver keeps, so the reconciler, the enforcer, the quotas
// and the device limit treat an L2TP device like any other.
//
// Billing is not here. Each device always comes up on its own address, and
// the kernel counts its traffic by that address, as it does for WireGuard and
// OpenVPN.
package l2tpdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// DataRoot is the panel's data directory, set by the panel at start.
var DataRoot = "/var/lib/wui"

// The device steps, replaced by tests that run where no device can be made.
var (
	ensureDeviceFn = ensureDevice
	removeDeviceFn = removeDevice
)

// DNSFor is the resolver handed to customers. The engine replaces it while
// the panel's own resolver is on; by default it is the interface's.
var DNSFor = func(iface *model.Interface) string { return iface.DNS }

// reportStale is how old the agent's report may be before the agent is taken
// to be down. It writes one every second.
const reportStale = 20 * time.Second

// startGrace is how long the agent is given to answer after the state is
// first written: systemd starts it from the path unit, and a first install of
// strongSwan and xl2tpd takes a while.
const startGrace = 15 * time.Minute

// Errors.
var (
	ErrNoAgent = errors.New("l2tpdriver: the L2TP and PPTP service (wui-l2tp) is not answering")
	ErrNoPSK   = errors.New("l2tpdriver: the tunnel has no pre-shared key; recreate it")
)

// Register makes the drivers available to the backend registry.
func Register() {
	backend.Register(model.ProtocolL2TP, func() backend.Backend { return New() })
	backend.RegisterRemover(model.ProtocolL2TP, Remove)
	backend.Register(model.ProtocolPPTP, func() backend.Backend { return NewPPTP() })
	backend.RegisterRemover(model.ProtocolPPTP, Remove)
}

// Driver is one PPP tunnel: L2TP or PPTP. A server carries one of each at
// most, since neither's port can be moved.
type Driver struct {
	log      *slog.Logger
	protocol model.Protocol

	mu    sync.Mutex
	iface *model.Interface
	paths l2tpconf.Paths
	// written is when this driver first wrote a state the agent had not
	// answered yet, for the start grace.
	written time.Time
}

// New builds an unopened L2TP driver.
func New() *Driver { return &Driver{log: slog.Default(), protocol: model.ProtocolL2TP} }

// NewPPTP builds an unopened PPTP driver.
func NewPPTP() *Driver { return &Driver{log: slog.Default(), protocol: model.ProtocolPPTP} }

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return d.protocol }

// Open describes the tunnel to the agent and reports whether the agent is
// carrying it.
//
// The accounts already described are kept: Open runs on every panel start,
// before the first Sync, and a state written then with nobody in it would
// have the agent end every customer's session as removed.
func (d *Driver) Open(ctx context.Context, iface *model.Interface) error {
	if iface.Protocol != d.protocol {
		return fmt.Errorf("%w: %s is %s", backend.ErrWrongProtocol, iface.Name, iface.Protocol)
	}
	if iface.Protocol == model.ProtocolL2TP && iface.L2TP.V.PSK == "" {
		return ErrNoPSK
	}
	paths := l2tpconf.PathsFor(DataRoot, string(iface.Protocol))
	if err := os.MkdirAll(paths.KickDir(), 0o750); err != nil {
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	if err := ensureDeviceFn(ctx, iface); err != nil {
		return err
	}

	d.mu.Lock()
	d.iface = iface
	d.paths = paths
	d.mu.Unlock()

	var kept []l2tpconf.Account
	if prev, err := readState(paths); err == nil && prev.Interface.ID == iface.ID {
		kept = prev.Interface.Accounts
	}
	if _, err := d.write(iface, kept); err != nil {
		return err
	}
	return d.agentError(true)
}

// state builds what the agent is told for this interface and these accounts.
func state(iface *model.Interface, accounts []l2tpconf.Account) (*l2tpconf.State, error) {
	gw, err := gateway(iface.Subnet)
	if err != nil {
		return nil, err
	}
	mtu := iface.MTU
	if mtu < l2tpconf.MinMTU || mtu > l2tpconf.MaxMTU {
		mtu = l2tpconf.DefaultMTU
	}
	st := &l2tpconf.State{
		Version: l2tpconf.SchemaVersion,
		Interface: l2tpconf.Iface{
			ID:       iface.ID,
			Name:     iface.Name,
			Subnet:   mustPrefix(iface.Subnet).Masked().String(),
			Gateway:  gw.String(),
			DNS:      resolvers(DNSFor(iface)),
			MTU:      mtu,
			Accounts: accounts,
		},
	}
	if iface.Protocol == model.ProtocolPPTP {
		st.Protocol = l2tpconf.ProtocolPPTP
	} else {
		st.Interface.PSK = iface.L2TP.V.PSK
		st.Interface.AllowPlain = iface.L2TP.V.AllowPlain
	}
	return st, nil
}

// write puts the state on disk if it differs from what is there, and returns
// the accounts it carried before.
func (d *Driver) write(iface *model.Interface, accounts []l2tpconf.Account) ([]l2tpconf.Account, error) {
	d.mu.Lock()
	paths := d.paths
	d.mu.Unlock()

	st, err := state(iface, accounts)
	if err != nil {
		return nil, err
	}
	body, err := l2tpconf.Encode(st)
	if err != nil {
		return nil, fmt.Errorf("l2tpdriver: %w", err)
	}
	var before []l2tpconf.Account
	existing, err := os.ReadFile(paths.State())
	if err == nil {
		if prev, err := l2tpconf.Decode(existing); err == nil {
			before = prev.Interface.Accounts
		}
		if string(existing) == string(body) {
			return before, nil
		}
	}
	if err := writeAtomic(paths.State(), body, 0o640); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.written.IsZero() {
		d.written = time.Now()
	}
	d.mu.Unlock()
	return before, nil
}

// Sync describes exactly the given accounts to the agent.
//
// A device whose credentials could not be written into pppd's file as they
// are -- a password with a quote in it, set before L2TP's rules applied -- is
// left out and said so, rather than refusing the whole tunnel over one row.
func (d *Driver) Sync(_ context.Context, desired []backend.DesiredAccount) (backend.SyncReport, error) {
	d.mu.Lock()
	iface := d.iface
	d.mu.Unlock()
	if iface == nil {
		return backend.SyncReport{}, backend.ErrNotOpen
	}
	pfx := mustPrefix(iface.Subnet).Masked()
	gw, _ := gateway(iface.Subnet)

	var want []l2tpconf.Account
	names := map[string]bool{}
	for _, a := range desired {
		if a.Username == "" || a.Secret == "" {
			continue
		}
		acc := l2tpconf.Account{ID: a.ID, Username: a.Username, Secret: a.Secret, IP: a.IP.String()}
		if err := l2tpconf.CheckUsername(acc.Username); err != nil {
			d.log.Warn("a device was left out", "protocol", d.protocol, "account", a.ID, "error", err)
			continue
		}
		if err := l2tpconf.CheckSecret(acc.Secret); err != nil {
			d.log.Warn("a device was left out: its password cannot be used here; set a new one",
				"protocol", d.protocol, "account", a.ID, "error", err)
			continue
		}
		if !a.IP.Is4() || !pfx.Contains(a.IP) || a.IP == gw || names[acc.Username] {
			d.log.Warn("a device was left out", "protocol", d.protocol, "account", a.ID, "address", a.IP)
			continue
		}
		names[acc.Username] = true
		want = append(want, acc)
	}
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })

	before, err := d.write(iface, want)
	if err != nil {
		return backend.SyncReport{}, err
	}
	return diffReport(before, want), nil
}

func diffReport(before, after []l2tpconf.Account) backend.SyncReport {
	old := make(map[uint]l2tpconf.Account, len(before))
	for _, a := range before {
		old[a.ID] = a
	}
	var r backend.SyncReport
	for _, a := range after {
		prev, ok := old[a.ID]
		switch {
		case !ok:
			r.Added++
		case prev != a:
			r.Updated++
		default:
			r.Unchanged++
		}
		delete(old, a.ID)
	}
	r.Removed = len(old)
	return r
}

// Stats is the agent's report of who is connected.
//
// L2TP has real sessions, so presence is known: a session in the report is
// connected now, and its last handshake is the report's own time. The
// counters are the session's own and start again with each one; billing is
// the kernel's, by address.
func (d *Driver) Stats(context.Context) ([]backend.Stat, error) {
	d.mu.Lock()
	paths := d.paths
	open := d.iface != nil
	d.mu.Unlock()
	if !open {
		return nil, backend.ErrNotOpen
	}
	r, err := freshReport(paths)
	if err != nil {
		return nil, err
	}
	at := time.Unix(r.At, 0)
	out := make([]backend.Stat, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		if s.Account == 0 {
			continue
		}
		out = append(out, backend.Stat{
			AccountID:     s.Account,
			RX:            s.RX,
			TX:            s.TX,
			LastHandshake: at,
			Endpoint:      s.Endpoint,
		})
	}
	return out, nil
}

// SessionDevices is the PPP link each connected customer is on, from the
// agent's report, for shaping to follow them there.
//
// The names go to tc, so only what pppd itself names a link is passed on.
func (d *Driver) SessionDevices(context.Context) ([]backend.SessionDevice, error) {
	d.mu.Lock()
	paths := d.paths
	open := d.iface != nil
	d.mu.Unlock()
	if !open {
		return nil, backend.ErrNotOpen
	}
	r, err := freshReport(paths)
	if err != nil {
		return nil, err
	}
	out := make([]backend.SessionDevice, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		if s.Account == 0 || !pppDevice(s.Device) {
			continue
		}
		out = append(out, backend.SessionDevice{AccountID: s.Account, Device: s.Device, Since: s.Since})
	}
	return out, nil
}

// pppDevice reports whether a name is one pppd gives a link: ppp and a unit
// number.
func pppDevice(name string) bool {
	unit, ok := strings.CutPrefix(name, "ppp")
	if !ok || unit == "" || len(unit) > 5 {
		return false
	}
	for _, c := range unit {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Kick asks the agent to end an account's session.
func (d *Driver) Kick(_ context.Context, accountID uint) error {
	d.mu.Lock()
	paths := d.paths
	open := d.iface != nil
	d.mu.Unlock()
	if !open {
		return backend.ErrNotOpen
	}
	if accountID == 0 {
		return backend.ErrUnknownAcct
	}
	return writeAtomic(paths.KickFile(accountID), nil, 0o640)
}

// Render produces the customer's sign-in details.
func (d *Driver) Render(_ context.Context, acc *model.Account, iface *model.Interface) (backend.ClientProfile, error) {
	return backend.ClientProfile{
		Filename: acc.DeviceName + "-" + string(iface.Protocol) + ".txt",
		MIMEType: "text/plain; charset=utf-8",
		Body:     []byte(Profile(acc, iface)),
	}, nil
}

// Health reports whether the agent is carrying the tunnel.
func (d *Driver) Health(context.Context) error {
	d.mu.Lock()
	open := d.iface != nil
	d.mu.Unlock()
	if !open {
		return backend.ErrNotOpen
	}
	return d.agentError(false)
}

// agentError is what the agent's report says is wrong, if anything.
//
// While the agent is starting or installing its packages the tunnel is not up
// but nothing is wrong either; opening says so with an error, so the tunnel is
// shown as not running yet, and health does not, so a tunnel that is merely
// starting is not torn down and opened again every tick.
func (d *Driver) agentError(opening bool) error {
	d.mu.Lock()
	paths := d.paths
	written := d.written
	d.mu.Unlock()

	r, err := readReport(paths)
	starting := !written.IsZero() && time.Since(written) < startGrace
	switch {
	case err != nil || time.Since(time.Unix(r.At, 0)) > reportStale:
		if starting && !opening {
			return nil
		}
		if starting {
			return fmt.Errorf("%w yet; it is starting", ErrNoAgent)
		}
		return fmt.Errorf("%w: check `systemctl status wui-l2tp.path wui-l2tp.service`", ErrNoAgent)
	case r.State == l2tpconf.AgentFailed:
		return fmt.Errorf("l2tpdriver: %s", r.Error)
	case r.State == l2tpconf.AgentInstalling:
		if opening {
			if d.protocol == model.ProtocolPPTP {
				return errors.New("l2tpdriver: installing pptpd and pppd")
			}
			return errors.New("l2tpdriver: installing strongSwan, xl2tpd and pppd")
		}
		return nil
	}
	return nil
}

// Close releases the driver. The tunnel keeps running: customers are not
// disconnected because the panel restarts.
func (d *Driver) Close() error {
	d.mu.Lock()
	d.iface = nil
	d.mu.Unlock()
	return nil
}

// Destroy takes the tunnel down for good.
func (d *Driver) Destroy(ctx context.Context) error {
	d.mu.Lock()
	iface := d.iface
	d.mu.Unlock()
	if iface == nil {
		return nil
	}
	return Remove(ctx, iface)
}

// Remove takes a tunnel down by its record: the state goes, and the agent,
// seeing no state, ends every session -- and on L2TP unloads IPsec -- and
// stops when it carries no other tunnel.
func Remove(ctx context.Context, iface *model.Interface) error {
	paths := l2tpconf.PathsFor(DataRoot, string(iface.Protocol))
	if err := os.Remove(paths.State()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	return removeDeviceFn(ctx, iface)
}

func readState(p l2tpconf.Paths) (*l2tpconf.State, error) {
	raw, err := os.ReadFile(p.State())
	if err != nil {
		return nil, err
	}
	return l2tpconf.Decode(raw)
}

// freshReport is the agent's report, if the agent is still writing it.
func freshReport(p l2tpconf.Paths) (l2tpconf.Report, error) {
	r, err := readReport(p)
	if err != nil {
		return l2tpconf.Report{}, err
	}
	if time.Since(time.Unix(r.At, 0)) > reportStale {
		return l2tpconf.Report{}, ErrNoAgent
	}
	return r, nil
}

func readReport(p l2tpconf.Paths) (l2tpconf.Report, error) {
	var r l2tpconf.Report
	raw, err := os.ReadFile(p.Report())
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("l2tpdriver: the agent's report: %w", err)
	}
	return r, nil
}

// Shared with the IKEv2 driver, which talks to the same agent the same way:
// a state file, a report, a kick per account, and a device holding the
// tunnel's gateway.

// EnsureDevice gives a tunnel its device (see ensureDevice).
func EnsureDevice(ctx context.Context, iface *model.Interface) error {
	return ensureDeviceFn(ctx, iface)
}

// RemoveDevice takes it away.
func RemoveDevice(ctx context.Context, iface *model.Interface) error {
	return removeDeviceFn(ctx, iface)
}

// Gateway is the first host of a subnet.
func Gateway(subnet string) (netip.Addr, error) { return gateway(subnet) }

// Resolvers is the IPv4 resolvers of a comma-separated list, two at most.
func Resolvers(field string) []string { return resolvers(field) }

// ReadReport is the agent's report as it is; FreshReport only while the
// agent is still writing it.
func ReadReport(p l2tpconf.Paths) (l2tpconf.Report, error)  { return readReport(p) }
func FreshReport(p l2tpconf.Paths) (l2tpconf.Report, error) { return freshReport(p) }

// WriteAtomic writes a file through a temporary one renamed into place.
func WriteAtomic(path string, body []byte, mode os.FileMode) error {
	return writeAtomic(path, body, mode)
}

// ReportStale and StartGrace are how old a report may be, and how long the
// agent is given to answer at first.
const (
	ReportStale = reportStale
	StartGrace  = startGrace
)

// gateway is the first host of the subnet: the server's end of every session.
func gateway(subnet string) (netip.Addr, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil || !p.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("l2tpdriver: subnet %q is not an IPv4 network", subnet)
	}
	return p.Masked().Addr().Next(), nil
}

func mustPrefix(subnet string) netip.Prefix {
	p, _ := netip.ParsePrefix(strings.TrimSpace(subnet))
	return p
}

// resolvers splits a comma-separated list into the IPv4 addresses pppd can
// hand out, two at most.
func resolvers(field string) []string {
	var out []string
	for _, part := range strings.Split(field, ",") {
		a, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil || !a.Is4() {
			continue
		}
		out = append(out, a.String())
		if len(out) == 2 {
			break
		}
	}
	return out
}

// writeAtomic writes through a temporary file renamed into place, so the
// agent never reads half a state.
func writeAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".wui-*")
	if err != nil {
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("l2tpdriver: %w", err)
	}
	return nil
}
