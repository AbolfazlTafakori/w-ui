package nodes

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
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

func (p *xuiPanel) read(ctx context.Context) ([]service.XUIInboundSeen, error) {
	rows, err := p.api.inbounds(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading its inbounds: %w", err)
	}
	p.rows = rows
	p.remote = map[string]*remoteXUIClient{}
	seen := make([]service.XUIInboundSeen, 0, len(rows))
	for _, r := range rows {
		var set xuiSettings
		var st xuiStream
		_ = objectOrString(r.Settings, &set)
		_ = objectOrString(r.Stream, &st)
		// The clients as that panel wrote them too, to send one back with
		// every field of its own.
		var raw struct {
			Clients []map[string]any `json:"clients"`
		}
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
	return seen, nil
}

func (p *xuiPanel) prepare(context.Context, []xrayFile) error { return nil }

func (p *xuiPanel) own(ctx context.Context, f xrayFile) xrayResult {
	email := service.XUIEmail(p.tag, &f.acc)
	res := xrayResult{name: email}
	in := f.iface.XUI.V
	desired := xuiClientFor(p.tag, &f.acc, in, f.client, f.on)
	rc, exists := p.remote[email]
	if exists {
		keepTheirs(&desired, rc.cfg, in.Kind)
	}
	switch {
	case !exists:
		if err := p.api.addClient(ctx, in.RemoteID, desired); err != nil {
			res.err = fmt.Errorf("adding %s to %s: %v", email, f.iface.Name, err)
			return res
		}
		res.changed = true
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
	case xuiDiffers(rc.cfg, desired):
		if err := p.api.updateClient(ctx, in.RemoteID, xuiKey(in.Kind, rc.cfg), desired); err != nil {
			res.err = fmt.Errorf("updating %s: %v", email, err)
			return res
		}
		res.changed = true
	}
	if exists && rc.hasStat {
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
	byClient := map[uint]*service.NodeUsage{}
	var devices []service.NodeDeviceUsage
	clientOf := map[uint]uint{}
	for _, u := range usage {
		if u.Up == 0 && u.Down == 0 {
			continue
		}
		raw += u.Up + u.Down
		nu := byClient[u.ClientID]
		if nu == nil {
			nu = &service.NodeUsage{OriginID: u.ClientID}
			byClient[u.ClientID] = nu
		}
		nu.Up += u.Up
		nu.Down += u.Down
		nu.Bytes += u.Up + u.Down
		devices = append(devices, service.NodeDeviceUsage{OriginID: u.AccountID, Up: u.Up, Down: u.Down})
		clientOf[u.AccountID] = u.ClientID
	}
	if err := service.RecordNodeTraffic(ctx, s.db, node.ID, raw, time.Now().UTC()); err != nil {
		s.log.Warn("could not record what a node carried", "node", node.Name, "error", err)
	}
	if len(byClient) == 0 || s.onReport == nil {
		return
	}
	list := make([]service.NodeUsage, 0, len(byClient))
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
