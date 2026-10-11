// Package l2tpagent is the root half of the PPP tunnels: L2TP/IPsec, and
// PPTP beside it.
//
// It runs as `wui l2tp-agent <data dir>`, as root, started by a systemd path
// unit the moment the panel writes a tunnel's state file and stopping on its
// own when no such file is left. Nothing runs, and no port is held, on a
// server with neither tunnel. Each tunnel is an Agent of its own, with its
// own state, report, daemon and block of credentials: one never touches the
// other's customers.
//
// The panel runs as an unprivileged user in a sandbox and cannot run pppd,
// so it describes the tunnel in a file and this carries it out: xl2tpd, the
// credentials pppd reads, the IPsec connection in strongSwan, and a firewall
// rule that keeps L2TP's port closed to anything that did not come out of
// IPsec. It also reports back who is connected, and ends the sessions the
// panel asks it to.
//
// The data directory belongs to the panel's user, so everything read from it
// is untrusted: the state is checked again here, in full, before any of it
// reaches a file, and every file operation in that directory goes through an
// os.Root, which refuses a link leading out of it.
package l2tpagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// System is what the agent does to the machine. The Linux implementation
// runs the real programs; tests give it a recording stand-in.
type System interface {
	// EnsurePackages installs whatever a protocol's tunnel needs and is
	// missing: strongSwan, xl2tpd and pppd for L2TP, pptpd and pppd for
	// PPTP. It is called before the first apply of each run.
	EnsurePackages(ctx context.Context, protocol string) error

	// WriteSecrets replaces a tunnel's block in /etc/ppp/chap-secrets with
	// one set from s, or removes it when s is nil, keeping every other line.
	WriteSecrets(protocol string, s *l2tpconf.State) error
	// WriteRun writes one of the agent's own files in l2tpconf.RunDir.
	WriteRun(name, body string) error
	// RemoveRun takes some of the agent's files away.
	RemoveRun(names ...string) error

	// LoadIPsec writes the IPsec connection and has strongSwan load it,
	// starting strongSwan if it is not running. UnloadIPsec removes it, and
	// stops strongSwan if this agent was what started it.
	LoadIPsec(ctx context.Context, conf string) error
	UnloadIPsec(ctx context.Context) error

	// ApplyFirewall loads an nftables program.
	ApplyFirewall(ctx context.Context, script string) error

	// Start starts a tunnel's daemon -- xl2tpd, or pptpd -- on the agent's
	// configuration, or adopts the one an earlier run left; Stop ends it and
	// every session it carries. Running reports whether it is up.
	Start(ctx context.Context, protocol string) error
	Stop(ctx context.Context, protocol string) error
	Running(protocol string) bool

	// Sessions lists every PPP session on the machine whose local end is
	// gateway -- every session of this tunnel, whoever it belongs to --
	// with the peer's address and the pid of the pppd carrying it.
	Sessions(gateway netip.Addr) ([]Live, error)
	// EndSession stops a pppd.
	EndSession(pid int) error
}

// Live is a session as the system finds it. Account is filled in by the
// agent from IP; zero is a session no current account holds.
type Live struct {
	l2tpconf.Session
	IP  netip.Addr
	PID int
}

// Files is the agent's view of the panel's directory.
type Files interface {
	// ReadState returns the state file, or ok false when there is none.
	ReadState() (raw []byte, ok bool, err error)
	// WriteReport replaces the report.
	WriteReport(r l2tpconf.Report) error
	// RemoveReport takes it away.
	RemoveReport() error
	// TakeKicks returns the accounts the panel asked to disconnect, and
	// removes the requests.
	TakeKicks() ([]uint, error)
}

// startedMarker records that strongSwan was started by this agent, so taking
// the L2TP tunnel down stops it again -- and leaves it alone when it was
// running for something else.
const startedMarker = "strongswan-started-by-wui"

// Agent carries out the panel's state for one tunnel.
type Agent struct {
	// protocol is the tunnel's: l2tpconf.ProtocolL2TP or ProtocolPPTP.
	protocol string
	sys      System
	files    Files
	log      *slog.Logger
	now      func() time.Time

	// installed is whether this run has already made sure of the packages.
	installed bool
	// applied is the state the tunnel is carrying; nil before the first
	// apply.
	applied *l2tpconf.State
	// appliedFor is the fingerprint of that state's tunnel-wide settings
	// (everything but the accounts): a change there restarts xl2tpd, a
	// change in the accounts alone does not.
	appliedFor string
	// failure is the last reason the tunnel could not be brought to the
	// state asked for, reported until an apply succeeds.
	failure string
	// seen is the password each live session signed in with, as a
	// fingerprint, by session.
	seen map[string]string
	// idle is whether the last pass found no tunnel and took it down, so the
	// next such pass has nothing to do.
	idle bool
}

// New builds the agent of a protocol's tunnel.
func New(protocol string, sys System, files Files, log *slog.Logger) *Agent {
	return &Agent{protocol: protocol, sys: sys, files: files, log: log.With("tunnel", protocol), now: time.Now}
}

// ErrNoTunnel is returned by Step when the panel has no tunnel of this agent's
// protocol here: the agent has taken it down.
var ErrNoTunnel = errors.New("l2tpagent: no tunnel is configured")

func (a *Agent) pptp() bool { return a.protocol == l2tpconf.ProtocolPPTP }

// name is the tunnel as the log and the report say it.
func (a *Agent) name() string {
	if a.pptp() {
		return "PPTP"
	}
	return "L2TP"
}

// Step does one pass: reads the state, brings the machine to it, ends the
// sessions it should, and writes the report.
func (a *Agent) Step(ctx context.Context) error {
	raw, ok, err := a.files.ReadState()
	if err != nil {
		return a.fail(fmt.Errorf("reading the panel's state: %w", err))
	}
	if !ok {
		if a.applied != nil || !a.idle {
			a.teardown(ctx)
		}
		a.idle = true
		return ErrNoTunnel
	}
	a.idle = false
	want, err := l2tpconf.Decode(raw)
	if err != nil {
		// What is running stays as it is: a state that cannot be carried
		// out is no reason to take down one that works.
		return a.fail(fmt.Errorf("the panel's state was refused: %w", err))
	}
	if want.IsPPTP() != a.pptp() {
		return a.fail(fmt.Errorf("the panel's state is for %s, not %s", want.Protocol, a.protocol))
	}

	if !a.installed {
		a.report(l2tpconf.AgentInstalling, "", nil)
		if err := a.sys.EnsurePackages(ctx, a.protocol); err != nil {
			return a.fail(fmt.Errorf("installing what %s needs: %w", a.name(), err))
		}
		a.installed = true
	}

	if err := a.apply(ctx, want); err != nil {
		return a.fail(err)
	}
	a.failure = ""

	gateway, _ := netip.ParseAddr(want.Interface.Gateway)
	live, err := a.sys.Sessions(gateway)
	if err != nil {
		a.log.Warn("could not list the sessions", "error", err)
	}
	book := addressBook(want)
	for i := range live {
		live[i].Account = book[live[i].IP]
	}
	live = a.endUnwanted(ctx, want, live)
	a.report(l2tpconf.AgentRunning, "", live)
	return nil
}

// apply brings the machine to want.
func (a *Agent) apply(ctx context.Context, want *l2tpconf.State) error {
	tunnel := tunnelFingerprint(want)
	tunnelChanged := tunnel != a.appliedFor

	if tunnelChanged {
		for _, f := range a.configFiles(want) {
			if err := a.sys.WriteRun(f.name, f.body); err != nil {
				return fmt.Errorf("writing %s: %w", f.name, err)
			}
		}
	}
	// Written every pass that differs: pppd reads it at each sign-in, so a
	// new device can sign in at once and a removed one cannot, with nothing
	// restarted.
	if a.applied == nil || !sameAccounts(a.applied, want) {
		if err := a.sys.WriteSecrets(a.protocol, want); err != nil {
			return fmt.Errorf("writing the credentials: %w", err)
		}
	}
	if tunnelChanged && !a.pptp() {
		if err := a.sys.LoadIPsec(ctx, l2tpconf.Swanctl(want)); err != nil {
			return fmt.Errorf("loading the IPsec connection: %w", err)
		}
		if err := a.sys.ApplyFirewall(ctx, l2tpconf.Guard(want)); err != nil {
			return fmt.Errorf("closing L2TP's port outside IPsec: %w", err)
		}
	}

	switch {
	case tunnelChanged && a.applied != nil:
		// The tunnel's own settings changed. The daemon and every pppd it
		// runs read them at start, so the change takes a restart -- which
		// ends every session on the tunnel, as a change of address or
		// resolver has to.
		a.log.Info(a.name() + " settings changed; restarting its daemon")
		if err := a.sys.Stop(ctx, a.protocol); err != nil {
			return fmt.Errorf("stopping the %s daemon: %w", a.name(), err)
		}
		if err := a.sys.Start(ctx, a.protocol); err != nil {
			return fmt.Errorf("starting the %s daemon: %w", a.name(), err)
		}
	case !a.sys.Running(a.protocol):
		// The first pass of this run adopts a daemon an earlier run left,
		// which is how restarting the agent keeps everyone connected; one
		// that has died is started again.
		if err := a.sys.Start(ctx, a.protocol); err != nil {
			return fmt.Errorf("starting the %s daemon: %w", a.name(), err)
		}
	}

	a.applied = want
	a.appliedFor = tunnel
	return nil
}

// endUnwanted ends the sessions that should not exist: a device the panel has
// taken off the tunnel, one whose password changed since it signed in, and
// one the panel asked to disconnect. pppd checks credentials at sign-in and
// never again, so without this a customer cut off for running out of data, or
// one whose password was rotated because it leaked, would stay connected.
func (a *Agent) endUnwanted(ctx context.Context, want *l2tpconf.State, live []Live) []Live {
	kicks, err := a.files.TakeKicks()
	if err != nil {
		a.log.Warn("could not read the disconnect requests", "error", err)
	}
	kicked := map[uint]bool{}
	for _, id := range kicks {
		kicked[id] = true
	}
	current := map[uint]l2tpconf.Account{}
	for _, acc := range want.Interface.Accounts {
		current[acc.ID] = acc
	}

	newest := newestSessions(live)

	kept := live[:0]
	for _, s := range live {
		acc, ok := current[s.Account]
		reason := ""
		switch {
		case ok && newest[s.Account] != sessionKey(s):
			// One sign-in is one session, as OpenVPN holds it: a device that
			// signs in again -- from another network, or another device --
			// replaces the session before it. Both cannot stand anyway: the
			// account has one address, and the second link would take it.
			reason = "signed in again"
		case !ok:
			// No current account holds this address: the device was taken
			// off the tunnel, or its account deleted.
			reason = "removed from the tunnel"
		case kicked[s.Account]:
			reason = "disconnected by the panel"
		case a.signedInWith(s) != "" && a.signedInWith(s) != l2tpconf.Fingerprint(acc.Secret):
			reason = "password changed"
		}
		if reason == "" {
			kept = append(kept, s)
			continue
		}
		if err := a.sys.EndSession(s.PID); err != nil {
			a.log.Warn("could not end a session", "account", s.Account, "device", s.Device, "error", err)
			kept = append(kept, s)
			continue
		}
		a.log.Info("session ended", "account", s.Account, "device", s.Device, "why", reason)
	}
	a.rememberSignIns(want, kept)
	return kept
}

// A session's password is the one its account had when the session was first
// seen. Sessions are seen within a pass of signing in, and a password change
// lands on the next pass, so the two are told apart.
func (a *Agent) signedInWith(s Live) string {
	if a.seen == nil {
		return ""
	}
	return a.seen[sessionKey(s)]
}

func (a *Agent) rememberSignIns(want *l2tpconf.State, live []Live) {
	secrets := map[uint]string{}
	for _, acc := range want.Interface.Accounts {
		secrets[acc.ID] = l2tpconf.Fingerprint(acc.Secret)
	}
	next := make(map[string]string, len(live))
	for _, s := range live {
		key := sessionKey(s)
		if was, ok := a.seen[key]; ok {
			next[key] = was
			continue
		}
		next[key] = secrets[s.Account]
	}
	a.seen = next
}

func sessionKey(s Live) string { return fmt.Sprintf("%d/%d", s.PID, s.Since) }

// newestSessions is the latest session of each account: the one that
// started last, and of two that started in the same second the later pppd.
func newestSessions(live []Live) map[uint]string {
	best := map[uint]Live{}
	for _, s := range live {
		if s.Account == 0 {
			continue
		}
		b, ok := best[s.Account]
		if !ok || s.Since > b.Since || (s.Since == b.Since && s.PID > b.PID) {
			best[s.Account] = s
		}
	}
	out := make(map[uint]string, len(best))
	for id, s := range best {
		out[id] = sessionKey(s)
	}
	return out
}

// teardown takes the tunnel down: the panel has none of this protocol here
// any more. Only this tunnel's daemon, credentials and files go; the other
// tunnel, if there is one, is left as it is.
func (a *Agent) teardown(ctx context.Context) {
	type step struct {
		what string
		do   func() error
	}
	steps := []step{
		{"stopping the daemon", func() error { return a.sys.Stop(ctx, a.protocol) }},
		{"removing the credentials", func() error { return a.sys.WriteSecrets(a.protocol, nil) }},
	}
	if !a.pptp() {
		steps = append(steps,
			step{"removing the IPsec connection", func() error { return a.sys.UnloadIPsec(ctx) }},
			step{"opening L2TP's port again", func() error { return a.sys.ApplyFirewall(ctx, l2tpconf.GuardRemoval()) }})
	}
	steps = append(steps,
		step{"removing the agent's files", func() error { return a.sys.RemoveRun(a.ownFiles()...) }},
		step{"removing the report", a.files.RemoveReport})
	for _, s := range steps {
		if err := s.do(); err != nil {
			a.log.Warn("tearing down the tunnel", "step", s.what, "error", err)
		}
	}
	a.applied = nil
	a.appliedFor = ""
	a.log.Info(a.name() + " tunnel taken down: the panel has none on this server")
}

type runFile struct{ name, body string }

// configFiles are the files the tunnel's daemon and pppd read.
func (a *Agent) configFiles(s *l2tpconf.State) []runFile {
	if a.pptp() {
		return []runFile{
			{"pptp-options", l2tpconf.PPTPOptions(s)},
			{"pptpd.conf", l2tpconf.PPTPD(s)},
		}
	}
	return []runFile{
		{"options", l2tpconf.PPPOptions(s)},
		{"xl2tpd.conf", l2tpconf.XL2TPD(s)},
		{"l2tp-secrets", l2tpconf.TunnelSecrets(s)},
	}
}

// ownFiles are every file of this tunnel's in the run directory: its
// configuration and what its daemon leaves there.
func (a *Agent) ownFiles() []string {
	if a.pptp() {
		return []string{"pptp-options", "pptpd.conf", "pptpd.pid", "pptpd.log"}
	}
	return []string{"options", "xl2tpd.conf", "l2tp-secrets", "xl2tpd.pid", "xl2tpd.log", "l2tp-control", startedMarker}
}

func (a *Agent) fail(err error) error {
	a.failure = err.Error()
	a.log.Error(a.name(), "error", err)
	a.report(l2tpconf.AgentFailed, a.failure, nil)
	return err
}

func (a *Agent) report(state, msg string, live []Live) {
	r := l2tpconf.Report{At: a.now().Unix(), State: state, Error: msg, Sessions: []l2tpconf.Session{}}
	if a.applied != nil {
		r.Applied = a.appliedFor
	}
	for _, s := range live {
		r.Sessions = append(r.Sessions, s.Session)
	}
	sort.Slice(r.Sessions, func(i, j int) bool { return r.Sessions[i].Account < r.Sessions[j].Account })
	if err := a.files.WriteReport(r); err != nil {
		a.log.Warn("could not write the report", "error", err)
	}
}

// Run steps every agent every interval until the context ends or the panel
// has no tunnel of any of theirs: one with no tunnel keeps looking, for as
// long as another has one, and starts its own the moment the panel asks.
// Stopped by its context -- systemd stopping the unit, for an update -- it
// leaves every daemon and session running, for the next run to adopt.
func Run(ctx context.Context, interval time.Duration, agents ...*Agent) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		none := true
		for _, a := range agents {
			if err := a.Step(ctx); !errors.Is(err, ErrNoTunnel) {
				none = false
			}
		}
		if none {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// tunnelFingerprint covers everything but the accounts.
func tunnelFingerprint(s *l2tpconf.State) string {
	f := s.Interface
	if s.IsPPTP() {
		return l2tpconf.Fingerprint(l2tpconf.PPTPOptions(s), l2tpconf.PPTPD(s), f.Subnet)
	}
	return l2tpconf.Fingerprint(l2tpconf.PPPOptions(s), l2tpconf.XL2TPD(s), l2tpconf.Swanctl(s), f.Subnet)
}

func sameAccounts(a, b *l2tpconf.State) bool {
	x, y := a.Interface.Accounts, b.Interface.Accounts
	if len(x) != len(y) {
		return false
	}
	byID := make(map[uint]l2tpconf.Account, len(x))
	for _, acc := range x {
		byID[acc.ID] = acc
	}
	for _, acc := range y {
		if byID[acc.ID] != acc {
			return false
		}
	}
	return true
}

// addressBook maps each account's address to the account.
func addressBook(s *l2tpconf.State) map[netip.Addr]uint {
	out := make(map[netip.Addr]uint, len(s.Interface.Accounts))
	for _, acc := range s.Interface.Accounts {
		if ip, err := netip.ParseAddr(acc.IP); err == nil {
			out[ip] = acc.ID
		}
	}
	return out
}
