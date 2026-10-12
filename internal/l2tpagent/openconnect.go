package l2tpagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/occonf"
	"github.com/abolfazl/w-ui/internal/radiusd"
)

// The OpenConnect tunnel.
//
// ocserv is run by the agent from files of its own (occonf.ConfDir), never
// the package's: it signs every device in by asking the agent's RADIUS
// server, which knows each device's password and address and, before it
// lets a device in, ends the device's other sessions -- so a second device
// on one sign-in takes the connection over, and the address is free for it.
// The agent's pass then reports who is connected, and ends a session the
// panel no longer wants: a device taken off, one kicked, one whose password
// changed.

// OCUser is one session ocserv holds.
type OCUser struct {
	ID       int
	Username string
	Device   string
	IP       netip.Addr
	Remote   string
	RX, TX   uint64
}

// OCSystem is what the OpenConnect agent does to the machine.
type OCSystem interface {
	EnsurePackages(ctx context.Context, protocol string) error
	// WriteOcserv writes the tunnel's files, and reports whether any of
	// them changed and whether only the certificate did.
	WriteOcserv(files []occonf.File) (changed, certOnly bool, err error)
	// RadiusSecret is the secret the files on disk carry, when there are
	// any: kept across the agent's restarts, so a restart is not a change.
	RadiusSecret() string
	// OcservRunning reports whether the agent's ocserv is up and answering.
	OcservRunning(ctx context.Context) bool
	// StartOcserv checks the configuration and starts ocserv; Restart stops
	// it first; Reload has it read its certificate again.
	StartOcserv(ctx context.Context) error
	RestartOcserv(ctx context.Context) error
	ReloadOcserv(ctx context.Context) error
	// RemoveOcserv stops ocserv and takes the files away.
	RemoveOcserv(ctx context.Context) error
	// OcservUsers lists the sessions; an error is ocserv not answering.
	OcservUsers(ctx context.Context) ([]OCUser, error)
	// Disconnect ends one.
	Disconnect(ctx context.Context, id int) error
	// Camouflage reports whether this ocserv can hide itself (1.2+).
	Camouflage(ctx context.Context) bool
	// ServeOCRadius runs the RADIUS server ocserv asks; StopOCRadius stops
	// it.
	ServeOCRadius(secret string, lookup radiusd.Lookup, onAccept radiusd.OnAccept) error
	StopOCRadius()
}

// OpenConnectAgent carries out the panel's OpenConnect tunnel.
type OpenConnectAgent struct {
	sys   OCSystem
	files Files
	log   *slog.Logger
	now   func() time.Time

	installed    bool
	applied      string
	state        *occonf.State
	idle         bool
	radiusSecret string
	// seen is, per session, when the agent first saw it and the password its
	// account had then: the newest of an account's is the one kept, and a
	// changed password ends the session.
	seen map[int]ocSeen
	// mu guards what the RADIUS server's hook reads while a pass runs.
	mu sync.Mutex
}

type ocSeen struct {
	first  time.Time
	secret string
}

// NewOpenConnect builds the OpenConnect tunnel's agent.
func NewOpenConnect(sys OCSystem, files Files, log *slog.Logger) *OpenConnectAgent {
	return &OpenConnectAgent{sys: sys, files: files, log: log.With("tunnel", "openconnect"), now: time.Now}
}

// Step does one pass, as Agent.Step does for the PPP tunnels.
func (a *OpenConnectAgent) Step(ctx context.Context) error {
	raw, ok, err := a.files.ReadState()
	if err != nil {
		return a.fail(fmt.Errorf("reading the panel's state: %w", err))
	}
	if !ok {
		if a.state != nil || !a.idle {
			a.teardown(ctx)
		}
		a.idle = true
		return ErrNoTunnel
	}
	a.idle = false
	want, err := occonf.Decode(raw)
	if err != nil {
		return a.fail(fmt.Errorf("the panel's state was refused: %w", err))
	}
	if !a.installed {
		a.report(l2tpconf.AgentInstalling, "", nil)
		if err := a.sys.EnsurePackages(ctx, l2tpconf.ProtocolOpenConnect); err != nil {
			return a.fail(fmt.Errorf("installing what OpenConnect needs: %w", err))
		}
		a.installed = true
	}
	if a.radiusSecret == "" {
		a.radiusSecret = a.sys.RadiusSecret()
	}
	if a.radiusSecret == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return a.fail(fmt.Errorf("making the RADIUS secret: %w", err))
		}
		a.radiusSecret = hex.EncodeToString(b)
	}
	// Who RADIUS lets in is the state just read: a device taken off cannot
	// sign in again from the next instant.
	if err := a.sys.ServeOCRadius(a.radiusSecret, ocLookupFrom(want), a.endOthers(ctx)); err != nil {
		return a.fail(fmt.Errorf("starting the RADIUS server sign-ins are checked by: %w", err))
	}
	files := occonf.Files(want, a.radiusSecret)
	changed, certOnly, err := a.sys.WriteOcserv(files)
	if err != nil {
		return a.fail(fmt.Errorf("writing ocserv's files: %w", err))
	}
	switch running := a.sys.OcservRunning(ctx); {
	case !running:
		if err := a.sys.StartOcserv(ctx); err != nil {
			return a.fail(fmt.Errorf("starting ocserv: %w", err))
		}
	case changed && certOnly:
		// A new certificate is read again without ending anyone's session.
		if err := a.sys.ReloadOcserv(ctx); err != nil {
			return a.fail(fmt.Errorf("having ocserv read its certificate again: %w", err))
		}
	case changed:
		// The port, the addresses or the camouflage: every session ends, and
		// every device signs in again on the new settings.
		if err := a.sys.RestartOcserv(ctx); err != nil {
			return a.fail(fmt.Errorf("restarting ocserv: %w", err))
		}
	}
	a.applied = ocFingerprint(files)
	a.mu.Lock()
	a.state = want
	a.mu.Unlock()

	users, err := a.sys.OcservUsers(ctx)
	if err != nil {
		return a.fail(fmt.Errorf("ocserv is not answering: %w", err))
	}
	live := a.endUnwanted(ctx, want, users)
	notice := ""
	if want.Interface.Camouflage && !a.sys.Camouflage(ctx) {
		notice = "this server's ocserv is older than 1.2 and cannot hide itself: camouflage is off until ocserv is updated"
	}
	a.report(l2tpconf.AgentRunning, notice, live)
	return nil
}

// endOthers is RADIUS's hook: before a device is let in, every other
// session on its sign-in is ended, and waited for, so the one signing in is
// the one that stays and its address is free for it.
func (a *OpenConnectAgent) endOthers(ctx context.Context) radiusd.OnAccept {
	return func(username string) {
		ids := a.sessionsOf(ctx, username)
		for _, id := range ids {
			if err := a.sys.Disconnect(ctx, id); err != nil {
				a.log.Warn("could not end a session", "user", username, "error", err)
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for len(ids) > 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			ids = a.sessionsOf(ctx, username)
		}
		if len(ids) > 0 {
			a.log.Warn("a session would not end before the same sign-in came in again", "user", username)
		} else if len(ids) == 0 {
			a.log.Info("signed in", "user", username)
		}
	}
}

func (a *OpenConnectAgent) sessionsOf(ctx context.Context, username string) []int {
	users, err := a.sys.OcservUsers(ctx)
	if err != nil {
		return nil
	}
	var ids []int
	for _, u := range users {
		if u.Username == username {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

// endUnwanted ends the sessions that should not exist -- a device taken off
// the tunnel, one the panel asked to disconnect, one whose password changed
// since it signed in, and all but the newest when one sign-in has two --
// and returns the rest as the report gives them.
func (a *OpenConnectAgent) endUnwanted(ctx context.Context, want *occonf.State, users []OCUser) []l2tpconf.Session {
	kicks, err := a.files.TakeKicks()
	if err != nil {
		a.log.Warn("could not read the disconnect requests", "error", err)
	}
	kicked := map[uint]bool{}
	for _, id := range kicks {
		kicked[id] = true
	}
	byName := map[string]occonf.Account{}
	for _, acc := range want.Interface.Accounts {
		byName[acc.Username] = acc
	}
	now := a.now()
	next := map[int]ocSeen{}
	for _, u := range users {
		s, ok := a.seen[u.ID]
		if !ok {
			s = ocSeen{first: now, secret: byName[u.Username].Secret}
		}
		next[u.ID] = s
	}
	// The newest session of each sign-in: seen last, and of two seen in one
	// pass, the higher id.
	newest := map[string]int{}
	for _, u := range users {
		b, ok := newest[u.Username]
		if !ok || next[u.ID].first.After(next[b].first) || (next[u.ID].first.Equal(next[b].first) && u.ID > b) {
			newest[u.Username] = u.ID
		}
	}
	var kept []l2tpconf.Session
	for _, u := range users {
		acc, ok := byName[u.Username]
		reason := ""
		switch {
		case !ok:
			reason = "removed from the tunnel"
		case kicked[acc.ID]:
			reason = "disconnected by the panel"
		case newest[u.Username] != u.ID:
			reason = "signed in again"
		case next[u.ID].secret != acc.Secret:
			reason = "password changed"
		}
		if reason != "" {
			if err := a.sys.Disconnect(ctx, u.ID); err != nil {
				a.log.Warn("could not end a session", "user", u.Username, "error", err)
			} else {
				a.log.Info("session ended", "user", u.Username, "why", reason)
				delete(next, u.ID)
				continue
			}
		}
		kept = append(kept, l2tpconf.Session{
			Account:  acc.ID,
			Device:   u.Device,
			Endpoint: u.Remote,
			Since:    next[u.ID].first.Unix(),
			RX:       u.RX,
			TX:       u.TX,
		})
	}
	a.seen = next
	return kept
}

// ocLookupFrom is who RADIUS lets in: every device on the tunnel, by its
// username, with its password, address and the tunnel's resolvers.
func ocLookupFrom(s *occonf.State) radiusd.Lookup {
	users := make(map[string]radiusd.User, len(s.Interface.Accounts))
	var dns []netip.Addr
	for _, d := range s.Interface.DNS {
		if ip, err := netip.ParseAddr(d); err == nil {
			dns = append(dns, ip)
		}
	}
	for _, acc := range s.Interface.Accounts {
		ip, err := netip.ParseAddr(acc.IP)
		if err != nil {
			continue
		}
		users[acc.Username] = radiusd.User{Password: acc.Secret, IP: ip, DNS: dns}
	}
	return func(name string) (radiusd.User, bool) {
		u, ok := users[name]
		return u, ok
	}
}

func (a *OpenConnectAgent) teardown(ctx context.Context) {
	a.sys.StopOCRadius()
	if err := a.sys.RemoveOcserv(ctx); err != nil {
		a.log.Warn("tearing down the tunnel", "step", "stopping ocserv", "error", err)
	}
	if err := a.files.RemoveReport(); err != nil {
		a.log.Warn("tearing down the tunnel", "step", "removing the report", "error", err)
	}
	a.mu.Lock()
	a.state = nil
	a.mu.Unlock()
	a.applied, a.seen, a.radiusSecret = "", nil, ""
	a.log.Info("OpenConnect tunnel taken down: the panel has none on this server")
}

func (a *OpenConnectAgent) fail(err error) error {
	a.log.Error("OpenConnect", "error", err)
	a.report(l2tpconf.AgentFailed, err.Error(), nil)
	return err
}

func (a *OpenConnectAgent) report(state, msg string, live []l2tpconf.Session) {
	r := l2tpconf.Report{At: a.now().Unix(), State: state, Applied: a.applied, Sessions: []l2tpconf.Session{}}
	if state == l2tpconf.AgentRunning {
		r.Notice = msg
	} else {
		r.Error = msg
	}
	r.Sessions = append(r.Sessions, live...)
	sort.Slice(r.Sessions, func(i, j int) bool { return r.Sessions[i].Account < r.Sessions[j].Account })
	if err := a.files.WriteReport(r); err != nil {
		a.log.Warn("could not write the report", "error", err)
	}
}

func ocFingerprint(files []occonf.File) string {
	parts := make([]string, 0, 2*len(files))
	for _, f := range files {
		parts = append(parts, f.Name, f.Body)
	}
	return l2tpconf.Fingerprint(parts...)
}
