// Package ikev2driver is the panel's side of the IKEv2/IPsec tunnel.
//
// The tunnel is run by the root agent (package l2tpagent), in the strongSwan
// L2TP's IPsec runs in: the panel cannot run strongSwan. This driver
// describes the tunnel to the agent in the panel's data directory, reads back
// the agent's report of who is connected, and asks it to end sessions -- the
// contract every driver keeps, so the reconciler, the quotas and the device
// limit treat an IKEv2 device like any other.
//
// Billing is not here: each device comes up on its own address, and the
// kernel counts its traffic by it, as for every other protocol.
package ikev2driver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/tunneldns"
)

// Errors.
var (
	ErrNoAgent = errors.New("ikev2driver: the PPP and IKEv2 service (wui-l2tp) is not answering")
	ErrNoModes = errors.New("ikev2driver: the tunnel lets no device in; turn on a way of signing in")
	ErrNoCerts = errors.New("ikev2driver: the tunnel has no certificates; recreate it")
)

// Register makes the driver available to the backend registry.
func Register() {
	backend.Register(model.ProtocolIKEv2, func() backend.Backend { return New() })
	backend.RegisterRemover(model.ProtocolIKEv2, Remove)
}

// Driver is the IKEv2 tunnel. A server carries one: its ports are the
// system's.
type Driver struct {
	log *slog.Logger

	mu      sync.Mutex
	iface   *model.Interface
	paths   l2tpconf.Paths
	written time.Time
	ifID    uint32 // the tunnel device's, which every session is tied to
}

// noXfrmTold is the tunnels the operator was told their kernel cannot shape:
// once each, not on every open.
var noXfrmTold sync.Map

// New builds an unopened driver.
func New() *Driver { return &Driver{log: slog.Default()} }

// SetLogger attaches the panel's logger.
func (d *Driver) SetLogger(l *slog.Logger) { d.log = l }

func (d *Driver) Protocol() model.Protocol { return model.ProtocolIKEv2 }

// Modes is the ways in a tunnel's parameters allow, as the state names them.
func Modes(p model.IKEv2Params) []string {
	var out []string
	if p.MSCHAPv2 {
		out = append(out, ikev2conf.ModeMSCHAPv2)
	}
	if p.PSK {
		out = append(out, ikev2conf.ModePSK)
	}
	if p.RSA {
		out = append(out, ikev2conf.ModeRSA)
	}
	if p.EAPTLS {
		out = append(out, ikev2conf.ModeEAPTLS)
	}
	return out
}

// ServerID is the identity the server announces: the first name its
// certificate is for, or what devices dial.
func ServerID(iface *model.Interface) string {
	if names := iface.IKEv2.V.ServerNames; len(names) > 0 {
		return names[0]
	}
	if servers := l2tpdriver.Servers(iface); len(servers) > 0 {
		return servers[0]
	}
	return iface.EndpointHost
}

// Open describes the tunnel to the agent and reports whether the agent is
// carrying it. The accounts already described are kept, as for L2TP: a state
// written with nobody in it before the first Sync would end every session.
func (d *Driver) Open(ctx context.Context, iface *model.Interface) error {
	if iface.Protocol != model.ProtocolIKEv2 {
		return fmt.Errorf("%w: %s is %s", backend.ErrWrongProtocol, iface.Name, iface.Protocol)
	}
	p := iface.IKEv2.V
	if !p.AnyMode() {
		return ErrNoModes
	}
	// The authority's certificate and the server's own are what a tunnel runs
	// on; the authority's key only issues, and a node is never given it.
	if (p.MSCHAPv2 || p.ByCertificate()) && (p.CACert == "" || p.ServerCert == "" || p.ServerKey == "") {
		return ErrNoCerts
	}
	paths := ikev2conf.NewPaths(l2tpdriver.DataRoot)
	if err := os.MkdirAll(paths.KickDir(), 0o750); err != nil {
		return fmt.Errorf("ikev2driver: %w", err)
	}
	ifID, err := ensureDevice(ctx, iface)
	if err != nil {
		return err
	}
	if ifID == 0 {
		if _, told := noXfrmTold.LoadOrStore(iface.Name, true); !told {
			d.log.Warn("this kernel has no xfrm interfaces: IKEv2 runs, but its speed limits cannot be applied",
				"interface", iface.Name)
		}
	}
	d.mu.Lock()
	d.iface = iface
	d.paths = paths
	d.ifID = ifID
	d.mu.Unlock()

	var kept []ikev2conf.Account
	if prev, err := readState(paths); err == nil && prev.Interface.ID == iface.ID {
		kept = prev.Interface.Accounts
	}
	if _, err := d.write(iface, kept); err != nil {
		return err
	}
	return d.agentError(true)
}

// state builds what the agent is told.
func state(iface *model.Interface, ifID uint32, accounts []ikev2conf.Account) (*ikev2conf.State, error) {
	gw, err := l2tpdriver.Gateway(iface.Subnet)
	if err != nil {
		return nil, err
	}
	p := iface.IKEv2.V
	pfx, _ := netipPrefix(iface.Subnet)
	return &ikev2conf.State{
		Version: ikev2conf.SchemaVersion,
		Interface: ikev2conf.Iface{
			ID:         iface.ID,
			Name:       iface.Name,
			Subnet:     pfx,
			Gateway:    gw.String(),
			DNS:        l2tpdriver.Resolvers(tunneldns.For(iface)),
			Modes:      Modes(p),
			ServerID:   ServerID(iface),
			CACert:     p.CACert,
			ServerCert: p.ServerCert,
			ServerKey:  p.ServerKey,
			Accounts:   accounts,
			IfID:       ifID,
		},
	}, nil
}

// write puts the state on disk if it differs, and returns the accounts it
// carried before.
func (d *Driver) write(iface *model.Interface, accounts []ikev2conf.Account) ([]ikev2conf.Account, error) {
	d.mu.Lock()
	paths, ifID := d.paths, d.ifID
	d.mu.Unlock()
	st, err := state(iface, ifID, accounts)
	if err != nil {
		return nil, err
	}
	body, err := ikev2conf.Encode(st)
	if err != nil {
		return nil, fmt.Errorf("ikev2driver: %w", err)
	}
	var before []ikev2conf.Account
	if existing, err := os.ReadFile(paths.State()); err == nil {
		if prev, err := ikev2conf.Decode(existing); err == nil {
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
// these rules applied, or no certificate on a tunnel that signs in by one --
// is left out and said so, rather than refusing the whole tunnel over it.
func (d *Driver) Sync(_ context.Context, desired []backend.DesiredAccount) (backend.SyncReport, error) {
	d.mu.Lock()
	iface := d.iface
	d.mu.Unlock()
	if iface == nil {
		return backend.SyncReport{}, backend.ErrNotOpen
	}
	pfx, _ := netipParse(iface.Subnet)
	gw, _ := l2tpdriver.Gateway(iface.Subnet)
	byCert := iface.IKEv2.V.ByCertificate()
	server := ServerID(iface)

	var want []ikev2conf.Account
	names := map[string]bool{}
	for _, a := range desired {
		if a.Username == "" || a.Secret == "" {
			continue
		}
		acc := ikev2conf.Account{ID: a.ID, Username: a.Username, Secret: a.Secret, IP: a.IP.String()}
		if byCert {
			acc.Cert = a.Cert
		}
		switch {
		case l2tpconf.CheckUsername(acc.Username) != nil, l2tpconf.CheckSecret(acc.Secret) != nil:
			d.log.Warn("a device was left out: its username or password cannot be used here; set a new one",
				"protocol", "ikev2", "account", a.ID)
			continue
		case byCert && acc.Cert == "":
			d.log.Warn("a device was left out: it has no certificate yet", "protocol", "ikev2", "account", a.ID)
			continue
		case !a.IP.Is4() || !pfx.Contains(a.IP) || a.IP == gw || names[acc.Username] || acc.Username == server:
			d.log.Warn("a device was left out", "protocol", "ikev2", "account", a.ID, "address", a.IP)
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

func diffReport(before, after []ikev2conf.Account) backend.SyncReport {
	old := make(map[uint]ikev2conf.Account, len(before))
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
		Filename: acc.DeviceName + "-ikev2.txt",
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
		return fmt.Errorf("ikev2driver: %s", r.Error)
	case r.State == l2tpconf.AgentInstalling:
		if opening {
			return errors.New("ikev2driver: installing strongSwan and its EAP plugins")
		}
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
// seeing none, takes it out of strongSwan.
func Remove(ctx context.Context, iface *model.Interface) error {
	paths := ikev2conf.NewPaths(l2tpdriver.DataRoot)
	if err := os.Remove(paths.State()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("ikev2driver: %w", err)
	}
	return removeDevice(ctx, iface)
}

func readState(p l2tpconf.Paths) (*ikev2conf.State, error) {
	raw, err := os.ReadFile(p.State())
	if err != nil {
		return nil, err
	}
	return ikev2conf.Decode(raw)
}
