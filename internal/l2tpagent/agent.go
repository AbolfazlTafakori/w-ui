// Package l2tpagent is the root half of the L2TP/IPsec tunnel.
//
// It runs as `wui l2tp-agent <data dir>`, as root, started by a systemd path
// unit the moment the panel writes an L2TP state file and stopping on its own
// when that file is gone. Nothing runs, and no port is held, on a server with
// no L2TP tunnel.
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
	// EnsurePackages installs whatever of strongSwan, xl2tpd and pppd is
	// missing. It is called before the first apply of each run.
	EnsurePackages(ctx context.Context) error

	// WriteSecrets replaces /etc/ppp/chap-secrets with the panel's block
	// set from s, or removed when s is nil, keeping every other line.
	WriteSecrets(s *l2tpconf.State) error
	// WriteRun writes one of the agent's own files in l2tpconf.RunDir.
	WriteRun(name, body string) error
	// RemoveRun takes the agent's directory away.
	RemoveRun() error

	// LoadIPsec writes the IPsec connection and has strongSwan load it,
	// starting strongSwan if it is not running. UnloadIPsec removes it, and
	// stops strongSwan if this agent was what started it.
	LoadIPsec(ctx context.Context, conf string) error
	UnloadIPsec(ctx context.Context) error

	// ApplyFirewall loads an nftables program.
	ApplyFirewall(ctx context.Context, script string) error

	// StartL2TP starts xl2tpd on the agent's configuration, or adopts the
	// one an earlier run left; StopL2TP ends it and every session it
	// carries. L2TPRunning reports whether it is up.
	StartL2TP(ctx context.Context) error
	StopL2TP(ctx context.Context) error
	L2TPRunning() bool

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

// Agent carries out the panel's L2TP state.
type Agent struct {
	sys   System
	files Files
	log   *slog.Logger
	now   func() time.Time

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
}

// New builds an agent.
func New(sys System, files Files, log *slog.Logger) *Agent {
	return &Agent{sys: sys, files: files, log: log, now: time.Now}
}

// ErrNoTunnel is returned by Step when the panel has no L2TP tunnel here: the
// agent has taken everything down and its run is over.
var ErrNoTunnel = errors.New("l2tpagent: no L2TP tunnel is configured")

// Step does one pass: reads the state, brings the machine to it, ends the
// sessions it should, and writes the report.
func (a *Agent) Step(ctx context.Context) error {
	raw, ok, err := a.files.ReadState()
	if err != nil {
		return a.fail(fmt.Errorf("reading the panel's state: %w", err))
	}
	if !ok {
		a.teardown(ctx)
		return ErrNoTunnel
	}
	want, err := l2tpconf.Decode(raw)
	if err != nil {
		// What is running stays as it is: a state that cannot be carried
		// out is no reason to take down one that works.
		return a.fail(fmt.Errorf("the panel's state was refused: %w", err))
	}

	if !a.installed {
		a.report(l2tpconf.AgentInstalling, "", nil)
		if err := a.sys.EnsurePackages(ctx); err != nil {
			return a.fail(fmt.Errorf("installing strongSwan, xl2tpd and pppd: %w", err))
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
		if err := a.sys.WriteRun("options", l2tpconf.PPPOptions(want)); err != nil {
			return fmt.Errorf("writing pppd's options: %w", err)
		}
		if err := a.sys.WriteRun("xl2tpd.conf", l2tpconf.XL2TPD(want)); err != nil {
			return fmt.Errorf("writing xl2tpd's configuration: %w", err)
		}
	}
	// Written every pass that differs: pppd reads it at each sign-in, so a
	// new device can sign in at once and a removed one cannot, with nothing
	// restarted.
	if a.applied == nil || !sameAccounts(a.applied, want) {
		if err := a.sys.WriteSecrets(want); err != nil {
			return fmt.Errorf("writing the credentials: %w", err)
		}
	}
	if tunnelChanged {
		if err := a.sys.LoadIPsec(ctx, l2tpconf.Swanctl(want)); err != nil {
			return fmt.Errorf("loading the IPsec connection: %w", err)
		}
		if err := a.sys.ApplyFirewall(ctx, l2tpconf.Guard()); err != nil {
			return fmt.Errorf("closing L2TP's port outside IPsec: %w", err)
		}
	}

	switch {
	case tunnelChanged && a.applied != nil:
		// The tunnel's own settings changed. xl2tpd and every pppd it runs
		// read them at start, so the change takes a restart -- which ends
		// every session on the tunnel, as a change of address or resolver
		// has to.
		a.log.Info("L2TP settings changed; restarting xl2tpd")
		if err := a.sys.StopL2TP(ctx); err != nil {
			return fmt.Errorf("stopping xl2tpd: %w", err)
		}
		if err := a.sys.StartL2TP(ctx); err != nil {
			return fmt.Errorf("starting xl2tpd: %w", err)
		}
	case !a.sys.L2TPRunning():
		// The first pass of this run adopts an xl2tpd an earlier run left,
		// which is how restarting the agent keeps everyone connected; one
		// that has died is started again.
		if err := a.sys.StartL2TP(ctx); err != nil {
			return fmt.Errorf("starting xl2tpd: %w", err)
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

	kept := live[:0]
	for _, s := range live {
		acc, ok := current[s.Account]
		reason := ""
		switch {
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

// teardown takes everything down: the panel has no L2TP tunnel here any more.
func (a *Agent) teardown(ctx context.Context) {
	steps := []struct {
		what string
		do   func() error
	}{
		{"stopping xl2tpd", func() error { return a.sys.StopL2TP(ctx) }},
		{"removing the IPsec connection", func() error { return a.sys.UnloadIPsec(ctx) }},
		{"removing the credentials", func() error { return a.sys.WriteSecrets(nil) }},
		{"opening L2TP's port again", func() error { return a.sys.ApplyFirewall(ctx, l2tpconf.GuardRemoval()) }},
		{"removing the agent's files", a.sys.RemoveRun},
		{"removing the report", a.files.RemoveReport},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			a.log.Warn("tearing down the L2TP tunnel", "step", s.what, "error", err)
		}
	}
	a.applied = nil
	a.appliedFor = ""
	a.log.Info("L2TP tunnel taken down: the panel has none on this server")
}

func (a *Agent) fail(err error) error {
	a.failure = err.Error()
	a.log.Error("L2TP", "error", err)
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

// Run steps every interval until the context ends or the panel has no tunnel.
// Stopped by its context -- systemd stopping the unit, for an update -- it
// leaves xl2tpd and every session running, for the next run to adopt.
func (a *Agent) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := a.Step(ctx); errors.Is(err, ErrNoTunnel) {
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
