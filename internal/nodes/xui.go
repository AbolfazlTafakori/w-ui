package nodes

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// A 3x-ui node, round by round: its inbounds read and held here as tunnels,
// this panel's customers on them made its clients -- added, switched on or
// off, and removed -- and what each spent read from its counters.
//
// The 3x-ui panel is never asked to enforce anything of its own. A client
// is made with no traffic limit and no expiry there: the allowance is one
// number spent across every server, which only this panel can see, so it
// switches a client off there when the customer runs out here. What that
// panel does enforce is the one thing it can see better: how many addresses
// use a client at once, its IP limit, which is the customer's one user per
// file.

// xuiLinksEvery is how many rounds go by between rereading every client's
// links, which change when the inbound does: its port, its address, its
// Reality keys.
const xuiLinksEvery = 15

// xuiOne runs one round against a 3x-ui node.
func (s *Syncer) xuiOne(ctx context.Context, node model.Node) {
	if err := s.xuiRound(ctx, node); err != nil {
		s.report(node, err)
		return
	}
	s.report(node, nil)
}

func (s *Syncer) xuiRound(ctx context.Context, node model.Node) error {
	api, err := newXUIAPI(node)
	if err != nil {
		return err
	}
	rows, err := api.inbounds(ctx)
	if err != nil {
		return fmt.Errorf("reading its inbounds: %w", err)
	}

	// What the 3x-ui panel holds: each inbound, and every client on it by
	// email with its settings and its counters.
	type remoteClient struct {
		inbound int
		cfg     xuiInboundClient
		stat    xuiClientStat
		hasStat bool
	}
	remote := map[string]*remoteClient{}
	seen := make([]service.XUIInboundSeen, 0, len(rows))
	for _, r := range rows {
		var set xuiSettings
		var st xuiStream
		_ = objectOrString(r.Settings, &set)
		_ = objectOrString(r.Stream, &st)
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
		if r.Protocol == "vless" && (st.Network == "tcp" || st.Network == "raw") &&
			(st.Security == "reality" || st.Security == "tls") {
			in.Flow = "xtls-rprx-vision"
		}
		seen = append(seen, service.XUIInboundSeen{XUIInbound: in, Port: r.Port})
		for _, c := range set.Clients {
			remote[c.Email] = &remoteClient{inbound: r.ID, cfg: c}
		}
		for _, cs := range r.ClientStats {
			if rc, ok := remote[cs.Email]; ok && rc.inbound == r.ID {
				rc.stat, rc.hasStat = cs, true
			}
		}
	}

	if s.XUIMirror == nil {
		return fmt.Errorf("this panel cannot hold a 3x-ui node's inbounds")
	}
	db := s.db.WithContext(ctx)
	// What the tunnels were before this read, so a client whose inbound
	// changed there -- its port, its keys -- is given its new links now
	// rather than at the next periodic reread.
	var before []model.Interface
	if err := db.Where("node_id = ? AND protocol = ?", node.ID, model.ProtocolXray).Find(&before).Error; err != nil {
		return fmt.Errorf("load its tunnels: %w", err)
	}
	was := make(map[uint]model.Interface, len(before))
	for _, f := range before {
		was[f.ID] = f
	}
	if err := s.XUIMirror(ctx, node.ID, xuiHost(node.Address), seen); err != nil {
		return err
	}

	var ifaces []model.Interface
	if err := db.Where("node_id = ? AND protocol = ?", node.ID, model.ProtocolXray).Find(&ifaces).Error; err != nil {
		return fmt.Errorf("load its tunnels: %w", err)
	}
	byID := make(map[uint]model.Interface, len(ifaces))
	ids := make([]uint, 0, len(ifaces))
	moved := map[uint]bool{}
	for _, f := range ifaces {
		byID[f.ID] = f
		ids = append(ids, f.ID)
		if w, ok := was[f.ID]; ok && (w.ListenPort != f.ListenPort || w.EndpointHost != f.EndpointHost || xuiShape(w.XUI.V) != xuiShape(f.XUI.V)) {
			moved[f.ID] = true
		}
	}
	var accounts []model.Account
	if len(ids) > 0 {
		if err := db.Where("interface_id IN ?", ids).Find(&accounts).Error; err != nil {
			return fmt.Errorf("load accounts: %w", err)
		}
	}
	clients := map[uint]model.Client{}
	if len(accounts) > 0 {
		cids := make([]uint, 0, len(accounts))
		for _, a := range accounts {
			cids = append(cids, a.ClientID)
		}
		var rowsC []model.Client
		if err := db.Where("id IN ?", cids).Find(&rowsC).Error; err != nil {
			return fmt.Errorf("load clients: %w", err)
		}
		for _, c := range rowsC {
			clients[c.ID] = c
		}
	}

	s.mu.Lock()
	if s.xuiRounds == nil {
		s.xuiRounds = map[uint]int{}
	}
	s.xuiRounds[node.ID]++
	refreshLinks := s.xuiRounds[node.ID]%xuiLinksEvery == 1
	s.mu.Unlock()

	var failures []string
	want := map[string]bool{}
	var usage []service.XUIUsage
	links := map[uint]string{}
	for _, acc := range accounts {
		iface, ok := byID[acc.InterfaceID]
		if !ok || iface.XUI.V.Gone || !model.XUISupported(iface.XUI.V.Kind) {
			continue
		}
		c, ok := clients[acc.ClientID]
		if !ok {
			continue
		}
		email := service.XUIEmail(&acc)
		want[email] = true
		desired := xuiClientFor(&acc, iface.XUI.V, c,
			!node.OverAllowance && c.Status.Serviceable() && acc.Enabled && iface.Enabled)

		rc, exists := remote[email]
		changed := false
		switch {
		case !exists:
			if err := api.addClient(ctx, iface.XUI.V.RemoteID, desired); err != nil {
				failures = append(failures, fmt.Sprintf("adding %s to %s: %v", email, iface.Name, err))
				continue
			}
			changed = true
		case rc.inbound != iface.XUI.V.RemoteID:
			// On another inbound there than the one it belongs to here:
			// moved by hand on that panel. Put back where it belongs.
			if err := api.deleteClient(ctx, email); err != nil {
				failures = append(failures, fmt.Sprintf("moving %s: %v", email, err))
				continue
			}
			if err := api.addClient(ctx, iface.XUI.V.RemoteID, desired); err != nil {
				failures = append(failures, fmt.Sprintf("moving %s: %v", email, err))
				continue
			}
			changed = true
		case xuiDiffers(rc.cfg, desired):
			if err := api.updateClient(ctx, desired); err != nil {
				failures = append(failures, fmt.Sprintf("updating %s: %v", email, err))
				continue
			}
			changed = true
		}

		if exists && rc.hasStat {
			u := service.XUIUsage{AccountID: acc.ID, ClientID: acc.ClientID, SeenUp: rc.stat.Up, SeenDown: rc.stat.Down}
			u.Up = since(rc.stat.Up, acc.XUIUp)
			u.Down = since(rc.stat.Down, acc.XUIDown)
			if u.Up > 0 || u.Down > 0 || u.SeenUp != acc.XUIUp || u.SeenDown != acc.XUIDown {
				usage = append(usage, u)
			}
		}

		if changed || refreshLinks || moved[acc.InterfaceID] || acc.XUILink == "" {
			got, err := api.links(ctx, email)
			if err != nil {
				failures = append(failures, fmt.Sprintf("reading the links of %s: %v", email, err))
			} else if joined := strings.Join(got, "\n"); joined != acc.XUILink {
				links[acc.ID] = joined
			}
		}
	}

	// Ours there and no longer anybody's here: a file removed, a customer
	// deleted, a tunnel taken away.
	for email := range remote {
		if want[email] || !service.XUIOwns(email) {
			continue
		}
		if err := api.deleteClient(ctx, email); err != nil {
			failures = append(failures, fmt.Sprintf("removing %s: %v", email, err))
		}
	}

	if err := service.SaveXUICounters(ctx, s.db, usage, links); err != nil {
		return fmt.Errorf("could not keep its counters: %w", err)
	}
	s.chargeXUI(ctx, node, usage)

	if len(failures) > 0 {
		if len(failures) > 3 {
			failures = append(failures[:3], fmt.Sprintf("and %d more", len(failures)-3))
		}
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

// xuiShape is what of an inbound goes into its links; its counters and
// client count change every round and do not.
func xuiShape(in model.XUIInbound) model.XUIInbound {
	in.Clients, in.Up, in.Down = 0, 0, 0
	return in
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
func xuiClientFor(acc *model.Account, in model.XUIInbound, c model.Client, enabled bool) xuiClient {
	out := xuiClient{
		Email:   service.XUIEmail(acc),
		Enable:  enabled,
		SubID:   service.XUIEmail(acc),
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
	default:
		out.Password = acc.Secret
	}
	return out
}

// xuiDiffers reports whether a client there is not as this panel wants it.
func xuiDiffers(have xuiInboundClient, want xuiClient) bool {
	return have.Enable != want.Enable || have.LimitIP != want.LimitIP ||
		have.ID != want.ID || have.Password != want.Password || have.Auth != want.Auth ||
		have.Flow != want.Flow
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
	api, err := newXUIAPI(node)
	if err != nil {
		return snap, err
	}
	start := time.Now()
	st, err := api.status(ctx)
	snap.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		return snap, err
	}
	snap.Version = "3x-ui " + st.PanelVersion
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
