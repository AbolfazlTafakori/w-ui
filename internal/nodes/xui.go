package nodes

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fleet"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// A 3x-ui node, round by round (xrayround.go): its inbounds read and held
// here as tunnels, this panel's customers on them made its clients -- added,
// switched on or off, and removed -- and what each spent read from its
// counters.
//
// The 3x-ui panel is never asked to enforce anything of its own. A client
// is made with no traffic limit and no expiry there: the allowance is one
// number spent across every server, which only this panel can see, so it
// switches a client off there when the customer runs out here. What that
// panel does enforce is the one thing it can see better: how many addresses
// use a client at once, its IP limit, which is the customer's one user per
// file.

// xuiOne runs one round against a 3x-ui node.
func (s *Syncer) xuiOne(ctx context.Context, node model.Node) {
	s.report(node, s.xuiRound(ctx, node))
}

func (s *Syncer) xuiRound(ctx context.Context, node model.Node) error {
	tag, err := service.PanelTag(ctx, s.db)
	if err != nil {
		return fmt.Errorf("this panel's tag: %w", err)
	}
	api, err := newXUIAPI(ctx, node)
	if err != nil {
		return err
	}
	return s.xrayRound(ctx, node, &xuiPanel{s: s, node: node, tag: tag, api: api})
}

// xuiPanel is a 3x-ui node for one round.
type xuiPanel struct {
	s    *Syncer
	node model.Node
	tag  string
	api  *xuiAPI

	rows []xuiInboundRow
	// remote is every client there by email, with its settings and its
	// counters.
	remote map[string]*remoteXUIClient
}

func (p *xuiPanel) read(ctx context.Context) ([]service.XUIInboundSeen, map[int]string, error) {
	rows, err := p.api.inbounds(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading its inbounds: %w", err)
	}
	p.rows = rows
	p.remote = map[string]*remoteXUIClient{}
	seen := make([]service.XUIInboundSeen, 0, len(rows))
	prints := make(map[int]string, len(rows))
	for _, r := range rows {
		prints[r.ID] = inboundPrint(r.Protocol, r.Port, r.Remark, r.Settings, r.Stream)
		var set xuiSettings
		var st xuiStream
		// A field another panel or a stored setting may lack or hold in another shape: missing is empty, and the code below works with empty.
		_ = objectOrString(r.Settings, &set)
		// A field another panel or a stored setting may lack or hold in another shape: missing is empty, and the code below works with empty.
		_ = objectOrString(r.Stream, &st)
		// The clients as that panel wrote them too, to send one back with
		// every field of its own.
		var raw struct {
			Clients []map[string]any `json:"clients"`
		}
		// A field another panel or a stored setting may lack or hold in another shape: missing is empty, and the code below works with empty.
		_ = objectOrString(r.Settings, &raw)
		in := model.XUIInbound{
			RemoteID: r.ID,
			Tag:      r.Tag,
			Remark:   r.Remark,
			Kind:     r.Protocol,
			Network:  st.Network,
			Security: st.Security,
			Method:   set.Method,
			Enabled:  r.Enable,
			Clients:  len(set.Clients),
			Up:       r.Up,
			Down:     r.Down,
		}
		if k, ok := service.InboundKindFor(model.KindXUI, p.api.version, r.Protocol); ok {
			in.Customers = k.Customers
		}
		if r.Protocol == "vless" && (st.Network == "tcp" || st.Network == "raw") &&
			(st.Security == "reality" || st.Security == "tls") {
			in.Flow = "xtls-rprx-vision"
		}
		seen = append(seen, service.XUIInboundSeen{XUIInbound: in, Port: r.Port})
		for i, c := range set.Clients {
			rc := &remoteXUIClient{inbound: r.ID, kind: r.Protocol, cfg: c}
			if i < len(raw.Clients) {
				rc.raw = raw.Clients[i]
			}
			p.remote[c.Email] = rc
		}
		for _, cs := range r.ClientStats {
			if rc, ok := p.remote[cs.Email]; ok && rc.inbound == r.ID {
				rc.stat, rc.hasStat = cs, true
			}
		}
	}
	return seen, prints, nil
}

func (p *xuiPanel) prepare(context.Context, []xrayFile) error { return nil }

func (p *xuiPanel) own(ctx context.Context, f xrayFile) xrayResult {
	email := service.XUIEmail(p.tag, &f.acc)
	res := xrayResult{name: email}
	in := f.iface.XUI.V
	desired := xuiClientFor(p.tag, &f.acc, in, f.client, f.on)
	rc, exists := p.remote[email]
	// left is a client under its name it never made: another customer's,
	// left by a restore. Taken away, with its credential and counters, and
	// the file's own made in its place -- or, where 3x-ui 2 keeps it as the
	// last on its inbound, made the file's own where it is, its credential
	// replaced and the traffic it already counted never charged here.
	left := exists && !f.acc.XUIPlaced
	if left {
		if err := p.api.deleteClient(ctx, rc.inbound, email, xuiKey(rc.kind, rc.cfg)); err == nil {
			delete(p.remote, email)
			exists, left = false, false
		} else if rc.inbound != in.RemoteID || !lastOf(p.remote, rc.inbound, email) {
			res.err = fmt.Errorf("taking away the client left under %s: %v", email, err)
			return res
		}
	}
	if exists && !left {
		keepTheirs(&desired, rc.cfg, in.Kind)
	}
	switch {
	case !exists:
		if err := p.api.addClient(ctx, in.RemoteID, desired); err != nil {
			res.err = fmt.Errorf("adding %s to %s: %v", email, f.iface.Name, err)
			return res
		}
		res.changed, res.placed = true, true
	case rc.inbound != in.RemoteID:
		// On another inbound there than the one it belongs to here: moved
		// by hand on that panel. Put back where it belongs.
		if err := p.api.deleteClient(ctx, rc.inbound, email, xuiKey(rc.kind, rc.cfg)); err != nil {
			res.err = fmt.Errorf("moving %s: %v", email, err)
			return res
		}
		if err := p.api.addClient(ctx, in.RemoteID, desired); err != nil {
			res.err = fmt.Errorf("moving %s: %v", email, err)
			return res
		}
		res.changed = true
	case xuiDiffers(rc.cfg, desired) || left:
		if err := p.api.updateClient(ctx, in.RemoteID, xuiKey(in.Kind, rc.cfg), desired); err != nil {
			res.err = fmt.Errorf("updating %s: %v", email, err)
			return res
		}
		res.changed = true
		res.placed = res.placed || left
	}
	if left && rc.hasStat {
		// What it counted was the other customer's: the starting point.
		res.usage = &service.XUIUsage{AccountID: f.acc.ID, ClientID: f.acc.ClientID, SeenUp: rc.stat.Up, SeenDown: rc.stat.Down}
	} else if exists && rc.hasStat {
		u := service.XUIUsage{AccountID: f.acc.ID, ClientID: f.acc.ClientID, SeenUp: rc.stat.Up, SeenDown: rc.stat.Down,
			Up: since(rc.stat.Up, f.acc.XUIUp), Down: since(rc.stat.Down, f.acc.XUIDown)}
		if u.Up > 0 || u.Down > 0 || u.SeenUp != f.acc.XUIUp || u.SeenDown != f.acc.XUIDown {
			res.usage = &u
		}
	}
	if res.changed || f.links {
		got, err := p.api.links(ctx, email)
		if err != nil {
			res.err = fmt.Errorf("reading the links of %s: %v", email, err)
		} else if joined := strings.Join(got, "\n"); joined != f.acc.XUILink {
			res.link = joined
		}
	}
	return res
}

func (p *xuiPanel) adopt(ctx context.Context, f xrayFile, marks map[uint]adoptedMark) xrayResult {
	changed, u, link, err := p.s.xuiAdopted(ctx, p.api, &f.acc, p.remote[f.acc.Adopted], f.on, f.links, marks)
	return xrayResult{usage: u, link: link, changed: changed, err: err}
}

// sweep takes away the clients that are this panel's there and no file's
// here: a file removed, a customer deleted, a tunnel taken away.
func (p *xuiPanel) sweep(ctx context.Context, wanted map[string]bool) (bool, []string) {
	changed := false
	var failures []string
	for email, rc := range p.remote {
		if wanted[email] || !service.XUIOwns(p.tag, email) {
			continue
		}
		if !rc.cfg.Enable && lastOf(p.remote, rc.inbound, email) {
			continue // the one left on its inbound there, already off
		}
		changed = true
		if err := p.api.deleteClient(ctx, rc.inbound, email, xuiKey(rc.kind, rc.cfg)); err != nil {
			// 3x-ui before 3 keeps at least one client on an inbound: the
			// last one is switched off instead, and left.
			if lastOf(p.remote, rc.inbound, email) {
				off := xuiClient{ID: rc.cfg.ID, Password: rc.cfg.Password, Auth: rc.cfg.Auth, Flow: rc.cfg.Flow,
					Method: rc.cfg.Method, Email: email, SubID: email, Enable: false}
				if uerr := p.api.updateClient(ctx, rc.inbound, xuiKey(rc.kind, rc.cfg), off); uerr == nil {
					continue
				}
			}
			failures = append(failures, fmt.Sprintf("removing %s: %v", email, err))
		}
	}
	return changed, failures
}

// finish checks that panel's xray still runs after a change of ours, and
// puts this panel's outbound there for its customers.
func (p *xuiPanel) finish(ctx context.Context, touched bool, files []xrayFile) []string {
	var failures []string
	// A change of ours there must not stop that panel's xray: every one of
	// its inbounds, its own operator's users with them, would stop. Said
	// loudly when it has, with xray's own reason.
	if touched {
		if st, err := p.api.status(ctx); err == nil && st.Xray.State != "" && st.Xray.State != "running" {
			p.s.log.Error("a 3x-ui node's xray is not running after this panel changed its clients",
				"node", p.node.Name, "state", st.Xray.State, "error", st.Xray.Error)
			failures = append(failures, "its xray is not running: "+st.Xray.Error)
		}
	}
	var brought []string
	for _, f := range files {
		if f.acc.Adopted != "" {
			brought = append(brought, f.acc.Adopted)
		}
	}
	if err := p.s.xuiEgress(ctx, p.api, p.node, egressRoute(p.tag, brought)); err != nil {
		failures = append(failures, err.Error())
	}
	return failures
}

// remoteXUIClient is a client on a 3x-ui panel: its inbound there, its
// settings and its counters.
type remoteXUIClient struct {
	inbound int
	kind    string
	cfg     xuiInboundClient
	raw     map[string]any // as that panel wrote it
	stat    xuiClientStat
	hasStat bool
}

// since is what a counter that only grows has added since it read last; a
// smaller number is a counter started again, and all of it is new.
func since(now, last uint64) uint64 {
	if now >= last {
		return now - last
	}
	return now
}

// chargeXUI hands what the node's clients spent to the customers' totals, at
// the node's price, and records what crossed the machine.
func (s *Syncer) chargeXUI(ctx context.Context, node model.Node, usage []service.XUIUsage) {
	var raw uint64
	byClient := map[uint]*fleet.NodeUsage{}
	var devices []fleet.NodeDeviceUsage
	clientOf := map[uint]uint{}
	for _, u := range usage {
		if u.Up == 0 && u.Down == 0 {
			continue
		}
		raw += u.Up + u.Down
		nu := byClient[u.ClientID]
		if nu == nil {
			nu = &fleet.NodeUsage{OriginID: u.ClientID}
			byClient[u.ClientID] = nu
		}
		nu.Up += u.Up
		nu.Down += u.Down
		nu.Bytes += u.Up + u.Down
		devices = append(devices, fleet.NodeDeviceUsage{OriginID: u.AccountID, Up: u.Up, Down: u.Down})
		clientOf[u.AccountID] = u.ClientID
	}
	if err := fleet.RecordNodeTraffic(ctx, s.db, node.ID, raw, time.Now().UTC()); err != nil {
		s.log.Warn("could not record what a node carried", "node", node.Name, "error", err)
	}
	if len(byClient) == 0 || s.onReport == nil {
		return
	}
	list := make([]fleet.NodeUsage, 0, len(byClient))
	for _, nu := range byClient {
		list = append(list, *nu)
	}
	s.onReport(chargeReport(list, devices, node.UsageCoefficient, clientOf))
}

// xuiClientFor is a file as a 3x-ui client.
func xuiClientFor(tag string, acc *model.Account, in model.XUIInbound, c model.Client, enabled bool) xuiClient {
	out := xuiClient{
		Email:   service.XUIEmail(tag, acc),
		Enable:  enabled,
		SubID:   service.XUIEmail(tag, acc),
		Comment: c.Name,
	}
	// One user per file, as everywhere on this panel; a customer sold any
	// number of users at once has no limit there either.
	if c.DeviceLimit > 0 {
		out.LimitIP = 1
	}
	switch in.Kind {
	case "vless":
		out.ID, out.Flow = acc.Secret, in.Flow
	case "vmess":
		out.ID = acc.Secret
	case "hysteria", "hysteria2":
		out.Auth = acc.Secret
	case "tuic":
		// An id and a password, kept as one: id|password.
		id, pass, _ := strings.Cut(acc.Secret, "|")
		out.ID, out.Password = id, pass
	case "wireguard", "amneziawg":
		// The device's own key pair; its address is that panel's to give.
		if k, err := wgkey.Parse(acc.Secret); err == nil {
			if pub, err := k.Public(); err == nil {
				out.PrivateKey, out.PublicKey = k.String(), pub.String()
			}
		}
	case "mtproto":
		// Made by that panel, for the inbound's own fake-TLS site.
	case "masque":
		out.Password = acc.Secret
	case "shadowsocks":
		out.Password = acc.Secret
		if !strings.HasPrefix(in.Method, "2022-") {
			out.Method = in.Method
		}
	default:
		out.Password = acc.Secret
	}
	return out
}

// lastOf reports whether a client is the only one on its inbound there.
func lastOf(remote map[string]*remoteXUIClient, inbound int, email string) bool {
	for e, rc := range remote {
		if e != email && rc.inbound == inbound {
			return false
		}
	}
	return true
}

// xuiKey is the credential the older 3x-ui API finds a client by, as its
// inbound's protocol names it.
func xuiKey(kind string, c xuiInboundClient) string {
	switch kind {
	case "trojan":
		return c.Password
	case "shadowsocks":
		return c.Email
	case "hysteria", "hysteria2":
		return c.Auth
	case "mtproto":
		return c.Secret
	case "wireguard", "amneziawg":
		return c.Email
	}
	return c.ID
}

// xuiDiffers reports whether a client there is not as this panel wants it.
// keepTheirs carries over what a client has there that this panel leaves to
// that panel: an MTProto secret it made for the inbound's site.
func keepTheirs(want *xuiClient, have xuiInboundClient, kind string) {
	if kind == "mtproto" {
		want.Secret = have.Secret
	}
}

func xuiDiffers(have xuiInboundClient, want xuiClient) bool {
	return have.Enable != want.Enable || have.LimitIP != want.LimitIP ||
		have.ID != want.ID || have.Password != want.Password || have.Auth != want.Auth ||
		have.Flow != want.Flow || have.Method != want.Method ||
		have.Secret != want.Secret || have.PrivateKey != want.PrivateKey ||
		have.TotalGB != want.TotalGB || have.ExpiryTime != want.ExpiryTime
}

// xuiHost is the address a 3x-ui node's customers dial: its panel's host.
func xuiHost(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// askXUI is the prober's question to a 3x-ui node.
func askXUI(ctx context.Context, node model.Node) (Snapshot, error) {
	var snap Snapshot
	start := time.Now()
	// The version is learned again on every probe: a panel upgraded on
	// its own is spoken to as it now is.
	xuiInfos.Delete(xuiInfoKey(node))
	api, err := newXUIAPI(ctx, node)
	if err != nil {
		return snap, err
	}
	st, err := api.status(ctx)
	snap.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		return snap, err
	}
	snap.Version = "3x-ui " + st.PanelVersion
	if st.PanelVersion == "" {
		snap.Version = "3x-ui"
	}
	snap.UptimeSec = st.Uptime
	snap.CPUPercent = st.CPU
	if st.Mem.Total > 0 {
		snap.MemPercent = float64(st.Mem.Current) * 100 / float64(st.Mem.Total)
	}
	// What the IP limit there can do, not what this panel's own devices do.
	snap.Enforcement = "reduced"
	if st.Xray.State != "" && st.Xray.State != "running" {
		msg := strings.TrimSpace(st.Xray.Error)
		if msg == "" {
			msg = st.Xray.State
		}
		return snap, fmt.Errorf("its xray is not running: %s", msg)
	}
	return snap, nil
}

// xuiIPsEvery is how often each online file's addresses are asked for: a
// call per online file, so not every few seconds. Measured against ten
// 3x-ui nodes with 200 files online on each, every 15s was 800 calls a
// node a minute; every 30s, four at a time beside the round, is half that
// and never holds the round up.
const xuiIPsEvery = 30 * time.Second

// xuiIPsAtOnce is how many address questions go to one node at a time.
const xuiIPsAtOnce = 4

// xuiAddrWindow is how recently an address must have been seen to count as
// one of a file's connections now: the reconciler's own window for a
// device that has gone quiet.
const xuiAddrWindow = fleet.LiveWindow

// What the panel cannot count on a node, said on the node as a code the
// panel's pages put into words.
const (
	// warnNoOnlines: it does not say who is online, so connections there are
	// not counted toward a customer's limit.
	warnNoOnlines = "noOnlines"
	// warnNoAddrs: it does not say when each address was seen, so two
	// devices on one file there are held only by 3x-ui's own IP limit, which
	// needs its fail2ban job running.
	warnNoAddrs = "noAddrs"
)

// xuiSessions tells the reconciler which of this panel's files are live on
// a 3x-ui node, and from how many places, so a customer's connections are
// counted across it with their others. A one-user plan with a WireGuard
// device and an xray one at once is over its limit like any other two; so
// is one xray file used from two addresses at once. What that 3x-ui does
// not say is said on the node (LimitWarning) rather than assumed.
func (s *Syncer) xuiSessions(ctx context.Context, node model.Node) {
	tag, err := service.PanelTag(ctx, s.db)
	if err != nil {
		s.log.Warn("could not read this panel's tag to count who is live on a node", "node", node.Name, "error", err)
		return
	}
	api, err := newXUIAPI(ctx, node)
	if err != nil {
		return // said by the next round, which fails the same way
	}
	emails, err := api.onlines(ctx)
	if err != nil {
		s.setLimitWarning(ctx, node, warnNoOnlines)
		return
	}
	var accs []model.Account
	if err := s.db.WithContext(ctx).Where("node_id = ?", node.ID).Find(&accs).Error; err != nil {
		s.log.Warn("could not read a node's files to count who is live on it", "node", node.Name, "error", err)
		return
	}
	byEmail := make(map[string]uint, len(accs))
	for i := range accs {
		byEmail[service.XUIEmail(tag, &accs[i])] = accs[i].ID
	}
	now := time.Now()

	// Where each online file comes from, asked every xuiIPsEvery.
	s.mu.Lock()
	if s.xuiAddrs == nil {
		s.xuiAddrs, s.xuiAddrsAt = map[uint]map[string]int{}, map[uint]time.Time{}
	}
	ask := now.Sub(s.xuiAddrsAt[node.ID]) >= xuiIPsEvery
	s.mu.Unlock()
	if ask {
		var mine []string
		for _, e := range emails {
			if _, ours := byEmail[e]; ours {
				mine = append(mine, e)
			}
		}
		s.mu.Lock()
		s.xuiAddrsAt[node.ID] = now
		s.mu.Unlock()
		// Beside the round, a few at a time: what it finds is counted from
		// the next round on, and a slow node never holds this one up.
		s.addrsWG.Add(1)
		go func() {
			defer s.addrsWG.Done()
			s.askAddrs(context.WithoutCancel(ctx), node, api, mine)
		}()
	}

	s.mu.Lock()
	if s.xuiSeen == nil {
		s.xuiSeen = map[uint]map[uint]time.Time{}
	}
	seen := s.xuiSeen[node.ID]
	if seen == nil {
		seen = map[uint]time.Time{}
		s.xuiSeen[node.ID] = seen
	}
	addrs := s.xuiAddrs[node.ID]
	live := map[uint]bool{}
	out := make([]fleet.NodeSession, 0, len(emails))
	for _, e := range emails {
		id, ok := byEmail[e]
		if !ok || live[id] {
			continue
		}
		live[id] = true
		// Since when, as far as this panel has watched: how the oldest
		// connection is told from the newest when one has to go.
		since, had := seen[id]
		if !had {
			since = now
			seen[id] = now
		}
		n := max(addrs[e], 1)
		out = append(out, fleet.NodeSession{OriginID: id, Connections: n, AgeSeconds: int(now.Sub(since) / time.Second)})
	}
	for id := range seen {
		if !live[id] {
			delete(seen, id)
		}
	}
	s.mu.Unlock()
	nodesDebug.Debug("live on a 3x-ui", "node", node.Name, "online", len(emails), "ours", len(out))
	if s.Sessions != nil {
		s.Sessions(node.ID, out)
	}
}

// askAddrs asks a 3x-ui where each of these online files is connected
// from, a few at a time, and keeps the counts for the next rounds.
func (s *Syncer) askAddrs(ctx context.Context, node model.Node, api *xuiAPI, emails []string) {
	ctx, cancel := context.WithTimeout(ctx, xuiIPsEvery)
	defer cancel()
	var (
		mu     sync.Mutex
		counts = map[string]int{}
		dated  = true
		wg     sync.WaitGroup
		slots  = make(chan struct{}, xuiIPsAtOnce)
	)
	for _, e := range emails {
		wg.Add(1)
		slots <- struct{}{}
		go func(e string) {
			defer wg.Done()
			defer func() { <-slots }()
			ips, err := api.clientIPs(ctx, e)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				dated = false
				return
			}
			counts[e] = liveAddrs(ips)
			if counts[e] == 0 && len(ips) > 0 {
				dated = false // addresses without times: not trusted to count
			}
		}(e)
	}
	wg.Wait()
	s.mu.Lock()
	s.xuiAddrs[node.ID] = counts
	s.mu.Unlock()
	if dated {
		s.setLimitWarning(ctx, node, "")
	} else {
		s.setLimitWarning(ctx, node, warnNoAddrs)
	}
}

// liveAddrs is how many of a file's addresses were seen within the window
// of the newest one: measured against that node's own clock, so the two
// machines' clocks need not agree. Addresses without a time count none.
func liveAddrs(ips map[string]int64) int {
	var newest int64
	for _, seen := range ips {
		newest = max(newest, seen)
	}
	if newest == 0 {
		return 0
	}
	n := 0
	for _, seen := range ips {
		if seen > 0 && newest-seen < int64(xuiAddrWindow/time.Second) {
			n++
		}
	}
	return n
}

// setLimitWarning stores what the panel cannot count on a node, when it
// changed.
func (s *Syncer) setLimitWarning(ctx context.Context, node model.Node, warning string) {
	var have string
	if err := s.db.WithContext(ctx).Model(&model.Node{}).Where("id = ?", node.ID).
		Select("limit_warning").Scan(&have).Error; err != nil || have == warning {
		return
	}
	if err := s.db.WithContext(ctx).Model(&model.Node{}).Where("id = ?", node.ID).
		UpdateColumn("limit_warning", warning).Error; err != nil {
		s.log.Warn("could not note what is not counted on a node", "node", node.Name, "error", err)
		return
	}
	if warning != "" {
		s.log.Warn("a node's connections are not all counted toward customers' limits", "node", node.Name, "why", warning)
	}
}

// nodesDebug is the nodes area's debug lines (WUI_DEBUG=nodes).
var nodesDebug = logger.Area(nil, logger.AreaNodes)
