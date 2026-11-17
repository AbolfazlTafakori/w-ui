package l2tpagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/radiusd"
	"github.com/abolfazl/w-ui/internal/vici"
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
	// directory -- each only when it is not already there as it is -- and
	// removes the ones of its own it no longer has. It returns what it
	// wrote or removed, as "dir/name".
	WriteSwanctl(files []ikev2conf.File) ([]string, error)
	// LoadSwanctl has strongSwan load what Loads names, starting it if it is
	// not running.
	LoadSwanctl(ctx context.Context, l Loads) error
	// ApplyItems unloads and loads these connections, pools and secrets
	// alone, over strongSwan's control socket; the files already say the
	// same. An error leaves the rest to LoadSwanctl.
	ApplyItems(ctx context.Context, unload, load []ikev2conf.Item) error
	// RemoveSwanctl takes every one of the tunnel's files away and reloads
	// strongSwan -- or stops it, when it was the agent's and nothing of the
	// panel's is left in it.
	RemoveSwanctl(ctx context.Context) error
	// SAs lists every session strongSwan holds; an error is strongSwan not
	// answering.
	SAs(ctx context.Context) ([]IKESA, error)
	// Instance names the running strongSwan: it changes when strongSwan
	// is started again, by the agent or by anyone else. Empty when it
	// cannot be told.
	Instance() string
	// Terminate ends one.
	Terminate(ctx context.Context, id uint) error
	// ServeRadius runs the RADIUS server password sign-ins are checked by,
	// with these devices -- starting it, or telling the running one who is
	// on the tunnel now. StopRadius stops it.
	ServeRadius(secret string, lookup radiusd.Lookup) error
	StopRadius()
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
	// wanted is the fingerprint the pass before wanted, and waiting since
	// when a change has been held back for the panel to finish making it.
	wanted  string
	waiting time.Time
	// items is, by Item.Key, the fingerprint of every connection, pool and
	// secret strongSwan was last given.
	items map[string]string
	// instance is the strongSwan they were given to.
	instance string
	state    *ikev2conf.State
	// stateKey fingerprints the panel's state file state was read from, and
	// statePrint the files it renders to.
	stateKey, statePrint string
	failure              string
	// seen is, per session, the secret and certificate its account had when
	// it was first seen: one changed since ends the session.
	seen map[uint]string
	idle bool
	// radiusSecret is what strongSwan and the RADIUS server share: made by
	// this agent and written nowhere but strongSwan's settings.
	radiusSecret string
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
	// The state unchanged since the last pass -- most passes -- is not
	// read or rendered again: with thousands of devices that alone is
	// most of a second.
	key := l2tpconf.Fingerprint(string(raw))
	same := a.state != nil && key == a.stateKey
	want := a.state
	if !same {
		if want, err = ikev2conf.Decode(raw); err != nil {
			return a.fail(fmt.Errorf("the panel's state was refused: %w", err))
		}
	}
	if !a.installed {
		a.report(l2tpconf.AgentInstalling, "", nil)
		if err := a.sys.EnsurePackages(ctx, l2tpconf.ProtocolIKEv2); err != nil {
			return a.fail(fmt.Errorf("installing what IKEv2 needs: %w", err))
		}
		a.installed = true
	}

	if a.radiusSecret == "" {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return a.fail(fmt.Errorf("making the RADIUS secret: %w", err))
		}
		a.radiusSecret = hex.EncodeToString(b)
	}
	var files []ikev2conf.File
	print := a.statePrint
	if !same {
		if want.Interface.Has(ikev2conf.ModeMSCHAPv2) {
			if err := a.sys.ServeRadius(a.radiusSecret, lookupFrom(want)); err != nil {
				return a.fail(fmt.Errorf("starting the RADIUS server password sign-ins are checked by: %w", err))
			}
		} else {
			a.sys.StopRadius()
		}
		files = ikev2conf.Files(want, a.radiusSecret)
		print = filesFingerprint(files)
	}
	sas, listErr := a.sys.SAs(ctx)
	// strongSwan not answering -- it died, or this is the first pass of a
	// run -- is given everything; a change, only what it touched, once the
	// panel has stopped making it.
	// A strongSwan started again since -- killed and restarted by systemd,
	// say -- answers, but may hold nothing of what it was given; what it
	// does hold is not known.
	restarted := a.applied != "" && a.sys.Instance() != a.instance
	if restarted {
		a.log.Warn("strongSwan was started again; loading the tunnel into it")
	}
	down := listErr != nil || a.applied == "" || restarted
	var live []l2tpconf.Session
	if !down {
		// Sessions that should not exist are ended first: a device
		// taken away holds its address no longer when its pool goes.
		live = a.endUnwanted(ctx, want, sas)
	}
	if down || (print != a.applied && a.settled(print)) {
		if files == nil {
			files = ikev2conf.Files(want, a.radiusSecret)
		}
		changed, err := a.sys.WriteSwanctl(files)
		if err != nil {
			return a.fail(fmt.Errorf("writing strongSwan's files: %w", err))
		}
		l := loadsFor(changed)
		items := ikev2conf.Items(want)
		prints := itemPrints(items)
		if down {
			l = Loads{All: true, Settings: l.Settings}
		} else if unload, load := a.itemDiff(items, prints); len(unload)+len(load) > 0 {
			// What one device changed is loaded alone: swanctl would
			// hand strongSwan every connection again.
			if err := a.sys.ApplyItems(ctx, unload, load); err != nil {
				a.log.Warn("could not load the change alone; loading it through swanctl", "error", err)
			} else {
				l.Secrets, l.Pools, l.Conns = false, false, false
			}
		} else {
			l.Secrets, l.Pools, l.Conns = false, false, false
		}
		// Nothing for swanctl when all of it went over the socket.
		if l != (Loads{}) {
			if err := a.sys.LoadSwanctl(ctx, l); err != nil {
				// The files are written: the next pass would see
				// nothing changed, and loads them all instead.
				a.applied = ""
				return a.fail(fmt.Errorf("loading the tunnel into strongSwan: %w", err))
			}
		}
		a.applied, a.waiting, a.items = print, time.Time{}, prints
		a.instance = a.sys.Instance()
	}
	a.wanted = print
	if down {
		if sas, listErr = a.sys.SAs(ctx); listErr != nil {
			a.log.Warn("could not list the sessions", "error", listErr)
		}
		live = a.endUnwanted(ctx, want, sas)
	}
	a.state, a.stateKey, a.statePrint = want, key, print
	a.failure = ""
	a.report(l2tpconf.AgentRunning, "", live)
	return nil
}

// settleFor is how long a change still being made -- the panel making
// thousands of certificates -- is held back at most before it is loaded.
const settleFor = 10 * time.Second

// settled reports whether a change the panel wants is ready to load: it is
// what the pass before wanted too, so the panel has stopped changing it, or
// it has been held back long enough. Changes coming one on another are
// loaded together, every settleFor, not one by one.
func (a *IKEv2Agent) settled(print string) bool {
	if a.waiting.IsZero() {
		a.waiting = a.now()
	}
	return print == a.wanted || a.now().Sub(a.waiting) >= settleFor
}

// itemPrints fingerprints each item by the request that loads it.
func itemPrints(items []ikev2conf.Item) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		cmd, msg := it.Load()
		b, err := vici.Request(cmd, msg)
		if err != nil {
			b = []byte(err.Error()) // refused when loaded too; never equal
		}
		out[it.Key()] = l2tpconf.Fingerprint(string(b))
	}
	return out
}

// itemDiff is what strongSwan last had and the panel no longer wants, and
// what is new or changed since.
func (a *IKEv2Agent) itemDiff(items []ikev2conf.Item, prints map[string]string) (unload, load []ikev2conf.Item) {
	for _, it := range items {
		if a.items[it.Key()] != prints[it.Key()] {
			load = append(load, it)
		}
	}
	gone := make([]string, 0)
	for k := range a.items {
		if _, ok := prints[k]; !ok {
			gone = append(gone, k)
		}
	}
	sort.Strings(gone)
	for _, k := range gone {
		kind, name, _ := strings.Cut(k, ":")
		unload = append(unload, ikev2conf.Item{Kind: kind, Sec: &vici.Section{Name: name}})
	}
	return unload, load
}

// Loads is what strongSwan is asked to load again through swanctl.
type Loads struct {
	// All is everything: strongSwan was not answering, or the agent has
	// just started and does not know what it holds.
	All bool
	// Creds are the certificates and keys, Secrets the secrets file.
	Settings, Creds, Secrets, Pools, Conns bool
}

// loadsFor is what the files written or removed have strongSwan load.
func loadsFor(changed []string) Loads {
	var l Loads
	for _, c := range changed {
		dir, name := path.Split(c)
		switch ikev2conf.LoadOf(strings.TrimSuffix(dir, "/"), name) {
		case ikev2conf.LoadSettings:
			l.Settings = true
		case ikev2conf.LoadCreds:
			l.Creds = true
		case ikev2conf.LoadSecrets:
			l.Secrets = true
		case ikev2conf.LoadPools:
			l.Pools = true
		case ikev2conf.LoadConns:
			l.Conns = true
		}
	}
	return l
}

// swanctlCommands are the swanctl commands that load l, in order: the
// credentials and pools before the connections that use them. strongSwan's
// own settings are read again only when they changed.
func swanctlCommands(l Loads) [][]string {
	var out [][]string
	if l.All {
		out = append(out, []string{"--load-all", "--noprompt"})
	} else {
		if l.Creds || l.Secrets {
			out = append(out, []string{"--load-creds", "--noprompt"})
		}
		if l.Pools {
			out = append(out, []string{"--load-pools"})
		}
		if l.Conns {
			out = append(out, []string{"--load-conns"})
		}
	}
	if l.Settings {
		out = append(out, []string{"--reload-settings"})
	}
	return out
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
	// Password sign-ins share one connection; their account is the one
	// their address belongs to.
	byIP := map[netip.Addr]uint{}
	for _, acc := range want.Interface.Accounts {
		if ip, err := netip.ParseAddr(acc.IP); err == nil {
			byIP[ip] = acc.ID
		}
	}
	accountOf := func(sa IKESA) (uint, bool) {
		if sa.Conn == ikev2conf.RadiusConn {
			id, ok := byIP[sa.VIP]
			return id, ok || sa.VIP.IsValid()
		}
		return ikev2conf.ParseConnName(sa.Conn)
	}
	// The newest session of each account: the one established last.
	newest := map[uint]IKESA{}
	for _, sa := range sas {
		acc, ok := accountOf(sa)
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
		accID, ours := accountOf(sa)
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

// lookupFrom is who RADIUS lets in: every device on the tunnel, by its
// username, with its password and address.
func lookupFrom(s *ikev2conf.State) radiusd.Lookup {
	users := make(map[string]radiusd.User, len(s.Interface.Accounts))
	var dns []netip.Addr
	for _, d := range s.Interface.DNS {
		if a, err := netip.ParseAddr(d); err == nil {
			dns = append(dns, a)
		}
	}
	for _, a := range s.Interface.Accounts {
		ip, err := netip.ParseAddr(a.IP)
		if err != nil {
			continue
		}
		users[a.Username] = radiusd.User{Password: a.Secret, IP: ip, DNS: dns}
	}
	return func(name string) (radiusd.User, bool) {
		u, ok := users[name]
		return u, ok
	}
}

func (a *IKEv2Agent) teardown(ctx context.Context) {
	a.sys.StopRadius()
	if err := a.sys.RemoveSwanctl(ctx); err != nil {
		a.log.Warn("tearing down the tunnel", "step", "removing it from strongSwan", "error", err)
	}
	if err := a.files.RemoveReport(); err != nil {
		a.log.Warn("tearing down the tunnel", "step", "removing the report", "error", err)
	}
	a.state, a.applied, a.wanted, a.waiting, a.seen, a.items = nil, "", "", time.Time{}, nil, nil
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
