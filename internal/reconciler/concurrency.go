package reconciler

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fleet"
)

// Holding a customer to the connections their plan allows.
//
// A plan sold to one person is one connection at a time, however many device
// files they hold, whichever protocol those files speak, and whichever server
// they reach: switching from the laptop to the phone is what the files are
// for, both at once is what the limit is for. The tunnel cannot count people,
// so it counts what it can see -- credentials with traffic moving right now,
// and the public addresses that traffic comes from -- and when more of those
// are live than the plan allows, the newest are held off for a short while.
// A WireGuard device held off has its peer removed and comes back on its own
// when the hold ends; an OpenVPN session is ended.
//
// What is deliberately not counted: a phone walking from wifi onto mobile
// data changes address once, which is not a second person. One credential
// used from two devices at the same time shows as an address that keeps
// flipping back and forth, and that is.
//
// Across servers the picture is assembled on the panel that sold the plan,
// which is the only place the whole of it exists. Every node reports what is
// live on it every few seconds; the panel adds that to what its own kernel
// sees, decides, and tells the node which device to hold off. A node keeps
// its own count only as a fallback for the time the panel is out of touch,
// so a customer cannot escape the limit by waiting for a network blip.

const (
	// activeWindow is how long since bytes last moved a session still counts
	// as connected. A WireGuard device with keepalive on sends something every
	// 25 seconds; three missed is gone.
	activeWindow = 75 * time.Second

	// holdFor is how long a device held off stays off. Long enough for the
	// customer to notice which device won, short enough that a mistake costs
	// them a couple of minutes.
	holdFor = 2 * time.Minute

	// remoteTTL is how long a node's last report of its live sessions is
	// believed. A node that has gone quiet for longer is not counted -- its
	// devices cannot be held off through it either -- and its own fallback
	// count takes over on that side.
	remoteTTL = 30 * time.Second

	// panelSilence is how long a node waits without hearing from its panel
	// before it holds customers to the limit on its own, with only its own
	// view. Longer than remoteTTL so the two do not both decide at once.
	panelSilence = 45 * time.Second
)

// activity is what has been seen of one credential on this server.
type activity struct {
	bytes uint64    // RX+TX at the last reading
	moved time.Time // when bytes last changed
	since time.Time // when this run of activity started
	addr  string    // the address the traffic comes from now
	// flips counts endpoint changes inside the window, flipAt the last one.
	// Two or more means two devices fighting over one credential.
	flips  int
	flipAt time.Time
	addrs  map[string]time.Time // address -> when traffic last came from it
	// sessions is each session of a credential that has several at once,
	// by where it comes from; nil while it has one.
	sessions map[string]*sessionActivity
}

// sessionActivity is one of several sessions of a credential.
type sessionActivity struct {
	bytes       uint64    // its own counters, which started with it
	first, last time.Time // when it first and last moved traffic
}

// remoteSession is one credential a node reported live.
type remoteSession struct {
	connections int
	since       time.Time
	addrs       []string
}

// remoteReport is what one node last said, and when.
type remoteReport struct {
	at       time.Time
	sessions map[uint]remoteSession // by the account's id on this panel
}

// concurrency is the state the reconciler keeps between ticks for this.
type concurrency struct {
	mu   sync.Mutex
	acts map[uint]*activity
	held map[uint]time.Time // account -> until when it is held off

	// remote is what each node reported live on it, keyed by node.
	remote map[uint]*remoteReport
	// nodeOf is which node each account is served by, 0 for this server.
	// Refreshed from the records every tick.
	nodeOf map[uint]uint
	// originOf is each account's id on the panel that owns it, for a node
	// reporting sessions back; 0 for accounts this panel owns.
	originOf map[uint]uint
	// clientOf is each account's customer, for the connections count.
	clientOf map[uint]uint
	// nameOf is "customer / device" for each account, for the log.
	nameOf map[uint]string
	// gone is what observe and expire noticed this tick, for the log: a
	// device that came on, one that went quiet.
	events []Event

	// panelHeard is when a managing panel last spoke to this server, for
	// the fallback on a node.
	panelHeard time.Time
}

func newConcurrency() *concurrency {
	return &concurrency{
		acts:     map[uint]*activity{},
		held:     map[uint]time.Time{},
		remote:   map[uint]*remoteReport{},
		nodeOf:   map[uint]uint{},
		originOf: map[uint]uint{},
		clientOf: map[uint]uint{},
		nameOf:   map[uint]string{},
	}
}

// Event is one thing worth a line in the log: a device connecting or
// going quiet, with where from and for how long.
type Event struct {
	Kind    string // "connected" | "disconnected"
	Account uint
	Name    string
	Addr    string
	For     time.Duration // how long it was on, when it went quiet
}

// Events hands back what was noticed since the last call.
func (c *concurrency) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.events
	c.events = nil
	return out
}

// observe folds one tick's readings from this server's own tunnels in.
//
// A WireGuard peer is one reading. A protocol with sessions of its own --
// L2TP, PPTP, OpenVPN -- can hand back several for one credential at once,
// each with counters that started with it; those are followed one by one
// (observeSessions) rather than compared against each other.
func (c *concurrency) observe(stats []backend.Stat, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var order []uint
	byAccount := map[uint][]backend.Stat{}
	for _, s := range stats {
		if _, ok := byAccount[s.AccountID]; !ok {
			order = append(order, s.AccountID)
		}
		byAccount[s.AccountID] = append(byAccount[s.AccountID], s)
	}
	for _, id := range order {
		if ss := byAccount[id]; len(ss) > 1 {
			c.observeSessions(id, ss, now)
		} else {
			c.observeOne(ss[0], now)
		}
	}
}

// observeOne folds in the one reading a credential has this tick.
func (c *concurrency) observeOne(s backend.Stat, now time.Time) {
	total := s.RX + s.TX
	a := c.acts[s.AccountID]
	if a == nil {
		// First sight: remembered, not counted. A peer that was on before
		// the panel started would otherwise look live for a window.
		c.acts[s.AccountID] = &activity{bytes: total, addr: whereFrom(s.Endpoint), addrs: map[string]time.Time{}}
		return
	}
	// Back to one session: what was followed of several is over.
	a.sessions = nil
	if total == a.bytes {
		return
	}
	if total < a.bytes {
		// Counters start over when a peer is re-added, the tunnel restarts
		// or a new session begins; that is not traffic.
		a.bytes = total
		return
	}
	a.bytes = total
	c.moved(s.AccountID, a, whereFrom(s.Endpoint), now)
}

// observeSessions folds in a credential with several sessions this tick.
//
// Each is followed on its own counters, keyed by where it comes from, and
// remembers when it first and last moved traffic. Two sessions are two
// devices only if those spans overlap: a phone back from another network
// leaves a session that has stopped moving beside the new one, and that is
// one device; two devices both keep moving.
func (c *concurrency) observeSessions(id uint, ss []backend.Stat, now time.Time) {
	a := c.acts[id]
	first := a == nil
	if first {
		a = &activity{addrs: map[string]time.Time{}}
		c.acts[id] = a
	}
	if a.sessions == nil {
		a.sessions = map[string]*sessionActivity{}
	}
	present := map[string]bool{}
	var total uint64
	var newest string
	for _, s := range ss {
		key := s.Endpoint
		present[key] = true
		bytes := s.RX + s.TX
		total += bytes
		sa := a.sessions[key]
		if sa == nil {
			// Remembered, not counted, as any first sight.
			a.sessions[key] = &sessionActivity{bytes: bytes}
			continue
		}
		if bytes > sa.bytes && !first {
			if sa.first.IsZero() {
				sa.first = now
			}
			sa.last = now
			if newest == "" || sa.first.After(a.sessions[newest].first) {
				newest = key
			}
		}
		sa.bytes = bytes
	}
	for key := range a.sessions {
		if !present[key] {
			delete(a.sessions, key)
		}
	}
	a.bytes = total
	if newest == "" {
		return
	}
	// The address the credential is used from now is its newest session's.
	c.moved(id, a, whereFrom(newest), now)
	for key, sa := range a.sessions {
		if !sa.last.IsZero() && now.Sub(sa.last) < activeWindow {
			if h := whereFrom(key); h != "" {
				a.addrs[h] = sa.last
			}
		}
	}
}

// moved records that a credential's traffic moved this tick, from host.
func (c *concurrency) moved(id uint, a *activity, host string, now time.Time) {
	wasLive := now.Sub(a.moved) < activeWindow
	a.moved = now
	if !wasLive {
		a.since = now
		a.flips = 0
		c.events = append(c.events, Event{Kind: "connected", Account: id, Name: c.nameOf[id], Addr: host})
	}
	if host != "" {
		if a.addr != "" && host != a.addr {
			if now.Sub(a.flipAt) > activeWindow {
				a.flips = 0
			}
			a.flips++
			a.flipAt = now
		}
		a.addr = host
		a.addrs[host] = now
	}
	for h, t := range a.addrs {
		if now.Sub(t) >= activeWindow {
			delete(a.addrs, h)
		}
	}
}

// remember refreshes which node, which panel id and which customer each
// account belongs to, from this tick's records, and notices the devices
// that have gone quiet since the last tick.
func (c *concurrency) remember(accounts []model.Account, clientNames map[uint]string, localNode uint, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodeOf = make(map[uint]uint, len(accounts))
	c.originOf = make(map[uint]uint, len(accounts))
	c.clientOf = make(map[uint]uint, len(accounts))
	c.nameOf = make(map[uint]string, len(accounts))
	for _, a := range accounts {
		node := a.NodeID
		if node == localNode {
			node = 0
		}
		c.nodeOf[a.ID] = node
		c.originOf[a.ID] = a.OriginID
		c.clientOf[a.ID] = a.ClientID
		c.nameOf[a.ID] = clientNames[a.ClientID] + " / " + a.DeviceName
	}
	// A device whose bytes stopped moving a window ago has gone. Said once:
	// the mark is cleared so the next quiet tick does not say it again.
	for id, a := range c.acts {
		if !a.since.IsZero() && !a.moved.IsZero() && now.Sub(a.moved) >= activeWindow {
			c.events = append(c.events, Event{Kind: "disconnected", Account: id, Name: c.nameOf[id], Addr: a.addr, For: a.moved.Sub(a.since)})
			a.since = time.Time{}
			a.moved = time.Time{}
		}
	}
	// What was deleted is forgotten, so a panel that issues and removes
	// devices all day does not carry every one of them in memory.
	for id := range c.acts {
		if _, ok := c.clientOf[id]; !ok {
			delete(c.acts, id)
			delete(c.held, id)
		}
	}
}

// connections is how many devices one credential is carrying right now:
// none when idle, one when live, and the live addresses when they fight.
func (a *activity) connections(now time.Time) int {
	if a == nil || now.Sub(a.moved) >= activeWindow {
		return 0
	}
	n := 1
	if a.flips >= 2 && now.Sub(a.flipAt) < activeWindow && len(a.addrs) > 1 {
		n = len(a.addrs)
	}
	if m := a.overlapping(now); m > n {
		n = m
	}
	return n
}

// overlapping is how many of a credential's sessions moved traffic at the
// same time, among those that moved within the window.
func (a *activity) overlapping(now time.Time) int {
	var spans []span
	for key, sa := range a.sessions {
		if sa.first.IsZero() || now.Sub(sa.last) >= activeWindow {
			continue
		}
		spans = append(spans, span{addr: key, first: sa.first, last: sa.last})
	}
	if len(spans) < 2 {
		return len(spans)
	}
	return concurrent(spans)
}

// liveAddrs lists the addresses a live credential is coming from.
func (a *activity) liveAddrs(now time.Time) []string {
	var out []string
	for h, t := range a.addrs {
		if now.Sub(t) < activeWindow {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// clients is a copy of which customer each account belongs to.
func (c *concurrency) clients() map[uint]uint {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[uint]uint, len(c.clientOf))
	for k, v := range c.clientOf {
		out[k] = v
	}
	return out
}

// lookup is what is known of one account right now, from wherever it is
// served: this server's own readings, or the last report from its node.
func (c *concurrency) lookup(id uint, now time.Time) (n int, since time.Time, ok bool) {
	if node := c.nodeOf[id]; node != 0 {
		rep := c.remote[node]
		if rep == nil || now.Sub(rep.at) > remoteTTL {
			return 0, time.Time{}, false
		}
		s, has := rep.sessions[id]
		if !has || s.connections == 0 {
			return 0, time.Time{}, false
		}
		return s.connections, s.since, true
	}
	a := c.acts[id]
	n = a.connections(now)
	if n == 0 {
		return 0, time.Time{}, false
	}
	return n, a.since, true
}

// setRemote stores what a node just reported.
func (c *concurrency) setRemote(nodeID uint, sessions []fleet.NodeSession, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rep := &remoteReport{at: now, sessions: make(map[uint]remoteSession, len(sessions))}
	for _, s := range sessions {
		if s.OriginID == 0 || s.Connections <= 0 {
			continue
		}
		rep.sessions[s.OriginID] = remoteSession{
			connections: s.Connections,
			// Reported as an age rather than a time, so two machines' clocks
			// need not agree.
			since: now.Add(-time.Duration(s.AgeSeconds) * time.Second),
			addrs: s.Addrs,
		}
	}
	c.remote[nodeID] = rep
}

// sessions is what this server reports upward when it is somebody's node:
// every managed credential that is live here, by its id on that panel.
func (c *concurrency) sessions(now time.Time) []fleet.NodeSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []fleet.NodeSession
	for id, a := range c.acts {
		origin := c.originOf[id]
		if origin == 0 {
			continue
		}
		n := a.connections(now)
		if n == 0 {
			continue
		}
		out = append(out, fleet.NodeSession{
			OriginID:    origin,
			Connections: n,
			AgeSeconds:  int(now.Sub(a.since) / time.Second),
			Addrs:       a.liveAddrs(now),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OriginID < out[j].OriginID })
	return out
}

// hold puts one account off until the given time, whoever decided it.
func (c *concurrency) hold(id uint, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur, ok := c.held[id]; ok && cur.After(until) {
		return
	}
	c.held[id] = until
}

// holds is every hold still in force, for a panel to carry to its nodes.
func (c *concurrency) holds(now time.Time) map[uint]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[uint]time.Time{}
	for id, until := range c.held {
		if now.Before(until) {
			out[id] = until
		}
	}
	return out
}

// heldOff reports whether the account is currently held off.
func (c *concurrency) heldOff(id uint, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.held[id]
	if !ok {
		return false
	}
	if now.After(until) {
		delete(c.held, id)
		// Its counters start over when the peer comes back; a fresh start
		// keeps a stale reading from counting it live at once.
		if a := c.acts[id]; a != nil {
			a.moved = time.Time{}
		}
		return false
	}
	return true
}

// panelSpoke notes that a managing panel is in touch.
func (c *concurrency) panelSpoke(now time.Time) {
	c.mu.Lock()
	c.panelHeard = now
	c.mu.Unlock()
}

// panelSilent reports whether the panel has been out of touch long enough
// for this node to fall back on its own count.
func (c *concurrency) panelSilent(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.panelHeard.IsZero() || now.Sub(c.panelHeard) > panelSilence
}

// heldAccount is one device held off, for the log and the driver.
type heldAccount struct {
	Account  model.Account
	ClientID uint
	Until    time.Time
	// Node is where the device is served: 0 for this server, otherwise the
	// node that has to be told.
	Node uint
}

// enforce looks at one customer's credentials, wherever they are served,
// and holds off whatever is over their limit, oldest connection kept. It
// returns what it held this tick, so the caller can end the sessions, tell
// the nodes, and say so.
func (c *concurrency) enforce(client *model.Client, accs []model.Account, now time.Time) []heldAccount {
	if client.DeviceLimit <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	type live struct {
		acc   model.Account
		n     int
		since time.Time
	}
	var lives []live
	total := 0
	for _, a := range accs {
		if until, ok := c.held[a.ID]; ok && now.Before(until) {
			continue // already off; not counted, not counted again
		}
		n, since, ok := c.lookup(a.ID, now)
		if !ok {
			continue
		}
		total += n
		lives = append(lives, live{acc: a, n: n, since: since})
	}
	if total <= client.DeviceLimit {
		return nil
	}
	// Oldest first: the device that was already connected stays. Ties --
	// two reports rounded to the same second -- go by id, so the answer is
	// the same on every tick rather than flapping between the two.
	sort.Slice(lives, func(i, j int) bool {
		if lives[i].since.Equal(lives[j].since) {
			return lives[i].acc.ID < lives[j].acc.ID
		}
		return lives[i].since.Before(lives[j].since)
	})
	var out []heldAccount
	kept := 0
	for _, l := range lives {
		// A credential fighting between two devices is over the limit on its
		// own; nothing but holding it off ends the fight.
		if l.n == 1 && kept+1 <= client.DeviceLimit {
			kept++
			continue
		}
		until := now.Add(holdFor)
		c.held[l.acc.ID] = until
		out = append(out, heldAccount{Account: l.acc, ClientID: client.ID, Until: until, Node: c.nodeOf[l.acc.ID]})
	}
	return out
}

// connectionsNow is what each of these customers is carrying right now,
// across every server, for the list.
func (c *concurrency) connectionsNow(clientIDs []uint, now time.Time) map[uint]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := make(map[uint]bool, len(clientIDs))
	for _, id := range clientIDs {
		want[id] = true
	}
	out := make(map[uint]int, len(clientIDs))
	for acc, client := range c.clientOf {
		if !want[client] {
			continue
		}
		if until, ok := c.held[acc]; ok && now.Before(until) {
			continue
		}
		if n, _, ok := c.lookup(acc, now); ok {
			out[client] += n
		}
	}
	return out
}

// liveClients is every customer with a connection in use right now, across
// every server, for the counters and the online filter.
func (c *concurrency) liveClients(now time.Time) []uint {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[uint]bool{}
	var out []uint
	for acc, client := range c.clientOf {
		if seen[client] {
			continue
		}
		if until, ok := c.held[acc]; ok && now.Before(until) {
			continue
		}
		if _, _, ok := c.lookup(acc, now); ok {
			seen[client] = true
			out = append(out, client)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// whereFrom is the identity of the place a session comes from: the address
// with its port. The port matters. Behind a relay every customer arrives
// from the relay's one address, and only the port tells one device from
// another -- a relay hands each flow a port of its own -- so on the host
// alone two devices fighting over a file would look like one. A single
// device keeps its port for a session; a NAT that rebinds moves it once,
// which is one flip, not the back-and-forth the fight check looks for.
func whereFrom(endpoint string) string {
	return strings.TrimSpace(endpoint)
}
