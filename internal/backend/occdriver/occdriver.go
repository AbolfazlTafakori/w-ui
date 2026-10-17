// Package occdriver is the panel's side of the OpenConnect tunnel.
//
// The tunnel is run by the root agent (package l2tpagent), which runs ocserv:
// the panel cannot. This driver describes the tunnel to the agent in the
// panel's data directory, reads back the agent's report of who is
// connected, and asks it to end sessions -- the contract every driver keeps,
// so the reconciler, the quotas, the speed limits and the device limit treat
// an OpenConnect device like any other.
//
// Billing is not here: each device comes up on its own address, and the
// kernel counts its traffic by it, as for every other protocol. Each session
// has a device of its own (vpns0, vpns1...), which the speed limits follow.
package occdriver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/occonf"
	"github.com/abolfazl/w-ui/internal/tunneldns"
)

// Errors.
var (
	ErrNoAgent = errors.New("occdriver: the tunnel service (wui-l2tp) is not answering")
	ErrNoCert  = errors.New("occdriver: the tunnel has no certificate; recreate it")
)

// Register makes the driver available to the backend registry.
func Register() {
	backend.Register(model.ProtocolOpenConnect, func() backend.Backend { return New() })
	backend.RegisterRemover(model.ProtocolOpenConnect, Remove)
}

// Driver is the OpenConnect tunnel. A server carries one.
type Driver struct {
	log *slog.Logger

	mu      sync.Mutex
	iface   *model.Interface
	paths   l2tpconf.Paths
	written time.Time
}

// New builds an unopened driver.
func New() *Driver { return &Driver{log: slog.Default()} }

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return model.ProtocolOpenConnect }

// Open describes the tunnel to the agent and reports whether the agent is
// carrying it. The accounts already described are kept: a state written
// with nobody in it before the first Sync would end every session.
func (d *Driver) Open(ctx context.Context, iface *model.Interface) error {
	if iface.Protocol != model.ProtocolOpenConnect {
		return fmt.Errorf("%w: %s is %s", backend.ErrWrongProtocol, iface.Name, iface.Protocol)
	}
	p := iface.OpenConnect.V
	if p.Cert == "" || p.Key == "" {
		return ErrNoCert
	}
	paths := occonf.NewPaths(backend.DataDir)
	if err := os.MkdirAll(paths.KickDir(), 0o750); err != nil {
		return fmt.Errorf("occdriver: %w", err)
	}
	if err := l2tpdriver.EnsureDevice(ctx, iface); err != nil {
		return err
	}
	d.mu.Lock()
	d.iface = iface
	d.paths = paths
	d.mu.Unlock()

	var kept []occonf.Account
	if prev, err := readState(paths); err == nil && prev.Interface.ID == iface.ID {
		kept = prev.Interface.Accounts
	}
	if _, err := d.write(iface, kept); err != nil {
		return err
	}
	return d.agentError(true)
}

// state builds what the agent is told.
func state(iface *model.Interface, accounts []occonf.Account) (*occonf.State, error) {
	gw, err := l2tpdriver.Gateway(iface.Subnet)
	if err != nil {
		return nil, err
	}
	pfx, err := netip.ParsePrefix(strings.TrimSpace(iface.Subnet))
	if err != nil {
		return nil, err
	}
	p := iface.OpenConnect.V
	mtu := iface.MTU
	if mtu < occonf.MinMTU || mtu > occonf.MaxMTU {
		mtu = occonf.DefaultMTU
	}
	return &occonf.State{
		Version: occonf.SchemaVersion,
		Interface: occonf.Iface{
			ID:               iface.ID,
			Name:             iface.Name,
			Port:             iface.ListenPort,
			Subnet:           pfx.Masked().String(),
			Gateway:          gw.String(),
			DNS:              l2tpdriver.Resolvers(tunneldns.For(iface)),
			MTU:              mtu,
			DTLS:             p.DTLS,
			Camouflage:       p.Camouflage,
			CamouflageSecret: p.CamouflageSecret,
			Cert:             p.Cert,
			Key:              p.Key,
			Accounts:         accounts,
		},
	}, nil
}

// write puts the state on disk if it differs, and returns the accounts it
// carried before.
func (d *Driver) write(iface *model.Interface, accounts []occonf.Account) ([]occonf.Account, error) {
	d.mu.Lock()
	paths := d.paths
	d.mu.Unlock()
	st, err := state(iface, accounts)
	if err != nil {
		return nil, err
	}
	body, err := occonf.Encode(st)
	if err != nil {
		return nil, fmt.Errorf("occdriver: %w", err)
	}
	var before []occonf.Account
	if existing, err := os.ReadFile(paths.State()); err == nil {
		if prev, err := occonf.Decode(existing); err == nil {
			before = prev.Interface.Accounts
		}
		if string(existing) == string(body) {
			return before, nil
		}
	}
	if err := l2tpdriver.WriteAtomic(paths.State(), body, 0o640); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.written.IsZero() {
		d.written = time.Now()
	}
	d.mu.Unlock()
	return before, nil
}

// Sync describes exactly the given accounts to the agent. A device that
// cannot be written as it is -- a password with a quote in it, set before
// these rules applied -- is left out and said so, rather than refusing the
// whole tunnel over it.
func (d *Driver) Sync(_ context.Context, desired []backend.DesiredAccount) (backend.SyncReport, error) {
	d.mu.Lock()
	iface := d.iface
	d.mu.Unlock()
	if iface == nil {
		return backend.SyncReport{}, backend.ErrNotOpen
	}
	pfx, _ := netip.ParsePrefix(strings.TrimSpace(iface.Subnet))
	pfx = pfx.Masked()
	gw, _ := l2tpdriver.Gateway(iface.Subnet)

	var want []occonf.Account
	names := map[string]bool{}
	for _, a := range desired {
		if a.Username == "" || a.Secret == "" {
			continue
		}
		acc := occonf.Account{ID: a.ID, Username: a.Username, Secret: a.Secret, IP: a.IP.String()}
		switch {
		case l2tpconf.CheckUsername(acc.Username) != nil, l2tpconf.CheckSecret(acc.Secret) != nil:
			d.log.Warn("a device was left out: its username or password cannot be used here; set a new one",
				"protocol", "openconnect", "account", a.ID)
			continue
		case !a.IP.Is4() || !pfx.Contains(a.IP) || a.IP == gw || names[acc.Username]:
			d.log.Warn("a device was left out", "protocol", "openconnect", "account", a.ID, "address", a.IP)
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

func diffReport(before, after []occonf.Account) backend.SyncReport {
	old := make(map[uint]occonf.Account, len(before))
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

// Stats is the agent's report of who is connected: a session in it is
// connected now.
func (d *Driver) Stats(context.Context) ([]backend.Stat, error) {
	d.mu.Lock()
	paths := d.paths
	open := d.iface != nil
	d.mu.Unlock()
	if !open {
		return nil, backend.ErrNotOpen
	}
	r, err := l2tpdriver.FreshReport(paths)
	if err != nil {
		return nil, err
	}
	at := time.Unix(r.At, 0)
	out := make([]backend.Stat, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		if s.Account == 0 {
			continue
		}
		out = append(out, backend.Stat{AccountID: s.Account, RX: s.RX, TX: s.TX, LastHandshake: at, Endpoint: s.Endpoint})
	}
	return out, nil
}

// SessionDevices are the sessions' own devices, which the speed limits
// follow: ocserv gives each session a vpns device of its own.
func (d *Driver) SessionDevices(context.Context) ([]backend.SessionDevice, error) {
	d.mu.Lock()
	paths := d.paths
	open := d.iface != nil
	d.mu.Unlock()
	if !open {
		return nil, backend.ErrNotOpen
	}
	r, err := l2tpdriver.FreshReport(paths)
	if err != nil {
		return nil, err
	}
	out := make([]backend.SessionDevice, 0, len(r.Sessions))
	for _, s := range r.Sessions {
		if s.Account == 0 || !vpnsDevice(s.Device) {
			continue
		}
		out = append(out, backend.SessionDevice{AccountID: s.Account, Device: s.Device, Since: s.Since})
	}
	return out, nil
}

// vpnsDevice reports whether a name is one ocserv gives a session: vpns and
// a number. Nothing else from the report is ever shaped.
func vpnsDevice(name string) bool {
	unit, ok := strings.CutPrefix(name, occonf.DeviceName)
	if !ok || unit == "" {
		return false
	}
	n, err := strconv.Atoi(unit)
	return err == nil && n >= 0 && n < 1<<20
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
	return l2tpdriver.WriteAtomic(paths.KickFile(accountID), nil, 0o640)
}

// Render produces the customer's sign-in, as text for a person.
func (d *Driver) Render(_ context.Context, acc *model.Account, iface *model.Interface) (backend.ClientProfile, error) {
	return backend.ClientProfile{
		Filename: acc.DeviceName + "-openconnect.txt",
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

// agentError is what the agent's report says is wrong, as for L2TP.
func (d *Driver) agentError(opening bool) error {
	d.mu.Lock()
	paths := d.paths
	written := d.written
	d.mu.Unlock()
	r, err := l2tpdriver.ReadReport(paths)
	starting := !written.IsZero() && time.Since(written) < l2tpdriver.StartGrace
	switch {
	case err != nil || time.Since(time.Unix(r.At, 0)) > l2tpdriver.ReportStale:
		if starting && !opening {
			return nil
		}
		if starting {
			return fmt.Errorf("%w yet; it is starting", ErrNoAgent)
		}
		return fmt.Errorf("%w: check `systemctl status wui-l2tp.path wui-l2tp.service`", ErrNoAgent)
	case r.State == l2tpconf.AgentFailed:
		return fmt.Errorf("occdriver: %s", r.Error)
	case r.State == l2tpconf.AgentInstalling:
		if opening {
			return errors.New("occdriver: installing ocserv")
		}
	case r.Notice != "":
		// Running, with something asked for that this server cannot do.
		d.log.Warn("OpenConnect", "notice", r.Notice)
	}
	return nil
}

// Close releases the driver. The tunnel keeps running.
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

// Remove takes the tunnel down by its record: the state goes, and the agent,
// seeing none, stops ocserv.
func Remove(ctx context.Context, iface *model.Interface) error {
	paths := occonf.NewPaths(backend.DataDir)
	if err := os.Remove(paths.State()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("occdriver: %w", err)
	}
	return l2tpdriver.RemoveDevice(ctx, iface)
}

func readState(p l2tpconf.Paths) (*occonf.State, error) {
	raw, err := os.ReadFile(p.State())
	if err != nil {
		return nil, err
	}
	return occonf.Decode(raw)
}
