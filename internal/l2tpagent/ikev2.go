package l2tpagent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// The IKEv2 tunnel.
//
// IKEv2 is strongSwan's alone -- no pppd, no daemon of its own -- and the
// strongSwan is the one L2TP's IPsec runs in: the agent writes the tunnel's
// connections, pools, secrets and certificates beside L2TP's file, and loads
// them all. Neither tunnel stops strongSwan while the other has a file there.

// IPsecSystem is what the IKEv2 agent does to the machine.
type IPsecSystem interface {
	// EnsurePackages installs what the tunnel needs and is missing.
	EnsurePackages(ctx context.Context, protocol string) error
	// WriteSwanctl writes the tunnel's files under strongSwan's swanctl
	// directory, and removes the ones of its own it no longer has.
	WriteSwanctl(files []ikev2conf.File) error
	// LoadSwanctl has strongSwan load everything, starting it if it is not
	// running.
	LoadSwanctl(ctx context.Context) error
	// RemoveSwanctl takes every one of the tunnel's files away and reloads
	// strongSwan -- or stops it, when it was the agent's and nothing of the
	// panel's is left in it.
	RemoveSwanctl(ctx context.Context) error
	// SAs lists every session strongSwan holds; an error is strongSwan not
	// answering.
	SAs(ctx context.Context) ([]IKESA, error)
	// Terminate ends one.
	Terminate(ctx context.Context, id uint) error
}

// IKEv2Agent carries out the panel's IKEv2 tunnel.
type IKEv2Agent struct {
	sys   IPsecSystem
	files Files
	log   *slog.Logger
	now   func() time.Time

	installed bool
	// applied is the fingerprint of the files strongSwan was last given.
	applied string
	state   *ikev2conf.State
	failure string
	// seen is, per session, the secret and certificate its account had when
	// it was first seen: one changed since ends the session.
	seen map[uint]string
	idle bool
}

// NewIKEv2 builds the IKEv2 tunnel's agent.
func NewIKEv2(sys IPsecSystem, files Files, log *slog.Logger) *IKEv2Agent {
	return &IKEv2Agent{sys: sys, files: files, log: log.With("tunnel", "ikev2"), now: time.Now}
}

// Step does one pass, as Agent.Step does for the PPP tunnels.
func (a *IKEv2Agent) Step(ctx context.Context) error {
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
	want, err := ikev2conf.Decode(raw)
	if err != nil {
		return a.fail(fmt.Errorf("the panel's state was refused: %w", err))
	}
	if !a.installed {
		a.report(l2tpconf.AgentInstalling, "", nil)
		if err := a.sys.EnsurePackages(ctx, l2tpconf.ProtocolIKEv2); err != nil {
			return a.fail(fmt.Errorf("installing what IKEv2 needs: %w", err))
		}
		a.installed = true
	}

	files := ikev2conf.Files(want)
	print := filesFingerprint(files)
	sas, listErr := a.sys.SAs(ctx)
	// Written and loaded when they changed, and again when strongSwan is
	// not answering -- it died, or this is the first pass of a run.
	if print != a.applied || listErr != nil {
		if err := a.sys.WriteSwanctl(files); err != nil {
			return a.fail(fmt.Errorf("writing strongSwan's files: %w", err))
		}
		if err := a.sys.LoadSwanctl(ctx); err != nil {
			return a.fail(fmt.Errorf("loading the tunnel into strongSwan: %w", err))
		}
		a.applied = print
		if sas, listErr = a.sys.SAs(ctx); listErr != nil {
			a.log.Warn("could not list the sessions", "error", listErr)
		}
	}
	a.state = want
	a.failure = ""

	live := a.endUnwanted(ctx, want, sas)
	a.report(l2tpconf.AgentRunning, "", live)
	return nil
}

// endUnwanted ends the sessions that should not exist -- a device taken off
// the tunnel, one whose password or certificate changed since it signed in,
// one the panel asked to disconnect, and all but the newest when a device
// has two -- and returns the rest as the report gives them.
func (a *IKEv2Agent) endUnwanted(ctx context.Context, want *ikev2conf.State, sas []IKESA) []l2tpconf.Session {
	kicks, err := a.files.TakeKicks()
	if err != nil {
		a.log.Warn("could not read the disconnect requests", "error", err)
	}
	kicked := map[uint]bool{}
	for _, id := range kicks {
		kicked[id] = true
	}
	current := map[uint]ikev2conf.Account{}
	for _, acc := range want.Interface.Accounts {
		current[acc.ID] = acc
	}
	// The newest session of each account: the one established last.
	newest := map[uint]IKESA{}
	for _, sa := range sas {
		acc, ok := ikev2conf.ParseConnName(sa.Conn)
		if !ok || !sa.Established {
			continue
		}
		if b, ok := newest[acc]; !ok || sa.Age < b.Age || (sa.Age == b.Age && sa.ID > b.ID) {
			newest[acc] = sa
		}
	}

	next := map[uint]string{}
	var kept []l2tpconf.Session
	for _, sa := range sas {
		accID, ours := ikev2conf.ParseConnName(sa.Conn)
		if !ours || !sa.Established {
			continue
		}
		acc, ok := current[accID]
		print := l2tpconf.Fingerprint(acc.Secret, acc.Cert)
		reason := ""
		switch {
		case !ok:
			reason = "removed from the tunnel"
		case kicked[accID]:
			reason = "disconnected by the panel"
		case newest[accID].ID != sa.ID:
			reason = "signed in again"
		case a.seen[sa.ID] != "" && a.seen[sa.ID] != print:
			reason = "password or certificate changed"
		}
		if reason != "" {
			if err := a.sys.Terminate(ctx, sa.ID); err != nil {
				a.log.Warn("could not end a session", "account", accID, "error", err)
			} else {
				a.log.Info("session ended", "account", accID, "why", reason)
				continue
			}
		}
		if was, ok := a.seen[sa.ID]; ok {
			next[sa.ID] = was
		} else {
			next[sa.ID] = print
		}
		kept = append(kept, l2tpconf.Session{
			Account:  accID,
			Device:   "ike" + strconv.FormatUint(uint64(sa.ID), 10),
			Endpoint: sa.RemoteHost,
			Since:    a.now().Unix() - sa.Age,
			RX:       sa.BytesIn,
			TX:       sa.BytesOut,
		})
	}
	a.seen = next
	return kept
}

func (a *IKEv2Agent) teardown(ctx context.Context) {
	if err := a.sys.RemoveSwanctl(ctx); err != nil {
		a.log.Warn("tearing down the tunnel", "step", "removing it from strongSwan", "error", err)
	}
	if err := a.files.RemoveReport(); err != nil {
		a.log.Warn("tearing down the tunnel", "step", "removing the report", "error", err)
	}
	a.state, a.applied, a.seen = nil, "", nil
	a.log.Info("IKEv2 tunnel taken down: the panel has none on this server")
}

func (a *IKEv2Agent) fail(err error) error {
	a.failure = err.Error()
	a.log.Error("IKEv2", "error", err)
	a.report(l2tpconf.AgentFailed, a.failure, nil)
	return err
}

func (a *IKEv2Agent) report(state, msg string, live []l2tpconf.Session) {
	r := l2tpconf.Report{At: a.now().Unix(), State: state, Error: msg, Applied: a.applied, Sessions: []l2tpconf.Session{}}
	r.Sessions = append(r.Sessions, live...)
	sort.Slice(r.Sessions, func(i, j int) bool { return r.Sessions[i].Account < r.Sessions[j].Account })
	if err := a.files.WriteReport(r); err != nil {
		a.log.Warn("could not write the report", "error", err)
	}
}

func filesFingerprint(files []ikev2conf.File) string {
	parts := make([]string, 0, 3*len(files))
	for _, f := range files {
		parts = append(parts, f.Dir, f.Name, f.Body)
	}
	return l2tpconf.Fingerprint(parts...)
}
