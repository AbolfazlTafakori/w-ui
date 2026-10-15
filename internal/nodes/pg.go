package nodes

import (
	"context"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// A Pasarguard node, round by round, as a 3x-ui one (xui.go): its cores'
// inbounds held here as tunnels, and each file on one of them a user there.
//
// Pasarguard gives a user access to inbounds by group, so each inbound has a
// group of this panel's, named for it, holding that inbound alone, and each
// file's user is in its tunnel's group. A user per file, rather than per
// customer, keeps what each file spent and the links of each tunnel apart,
// as they are on every other kind of server. The user is made with no
// traffic limit and no expiry: the allowance is this panel's, and a user is
// disabled there when the customer runs out here.

// pgRemoteID is an inbound's stable number here, made from its tag: Pasarguard
// names inbounds by tag alone.
func pgRemoteID(tag string) int {
	return int(crc32.ChecksumIEEE([]byte(tag)) & 0x7fffffff)
}

// pgGroupName is the group this panel keeps for an inbound.
func pgGroupName(tag string) string {
	name := "wui " + tag
	if len(name) > 64 {
		name = fmt.Sprintf("%s %08x", name[:55], crc32.ChecksumIEEE([]byte(tag)))
	}
	return name
}

// PGUsername is a file's user on a Pasarguard panel.
func pgUsername(acc *model.Account) string { return fmt.Sprintf("wui_%d", acc.ID) }

// pgOwns reports whether a username there is one this panel made.
func pgOwns(name string) bool {
	rest, ok := strings.CutPrefix(name, "wui_")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Syncer) pgOne(ctx context.Context, node model.Node) {
	if err := s.pgRound(ctx, node); err != nil {
		s.report(node, err)
		return
	}
	s.report(node, nil)
}

func (s *Syncer) pgRound(ctx context.Context, node model.Node) error {
	api, err := newPGAPI(node)
	if err != nil {
		return err
	}
	inbounds, err := api.inbounds(ctx)
	if err != nil {
		return fmt.Errorf("reading its inbounds: %w", err)
	}
	seen := make([]service.XUIInboundSeen, 0, len(inbounds))
	for _, in := range inbounds {
		x := model.XUIInbound{
			RemoteID: pgRemoteID(in.Tag),
			Tag:      in.Tag,
			Remark:   in.Tag,
			Kind:     in.Protocol,
			Network:  in.Stream.Network,
			Security: in.Stream.Security,
			Method:   in.Settings.Method,
			Enabled:  true,
		}
		if x.Network == "" {
			x.Network = "tcp"
		}
		seen = append(seen, service.XUIInboundSeen{XUIInbound: x, Port: in.portOf()})
	}
	if s.XUIMirror == nil {
		return fmt.Errorf("this panel cannot hold a Pasarguard node's inbounds")
	}
	db := s.db.WithContext(ctx)
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
		if w, ok := was[f.ID]; ok && (w.ListenPort != f.ListenPort || xuiShape(w.XUI.V) != xuiShape(f.XUI.V)) {
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
		var rows []model.Client
		if err := db.Where("id IN ?", cids).Find(&rows).Error; err != nil {
			return fmt.Errorf("load clients: %w", err)
		}
		for _, c := range rows {
			clients[c.ID] = c
		}
	}

	// The groups: one per inbound that has files on it, made when missing.
	groupOf := map[string]int{}
	if len(accounts) > 0 {
		gs, err := api.groups(ctx)
		if err != nil {
			return fmt.Errorf("reading its groups: %w", err)
		}
		for _, g := range gs {
			if len(g.InboundTags) == 1 && g.Name == pgGroupName(g.InboundTags[0]) {
				groupOf[g.InboundTags[0]] = g.ID
			}
		}
	}

	users, err := api.ourUsers(ctx)
	if err != nil {
		return fmt.Errorf("reading its users: %w", err)
	}
	remote := make(map[string]pgUser, len(users))
	for _, u := range users {
		if pgOwns(u.Username) {
			remote[u.Username] = u
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
		name := pgUsername(&acc)
		want[name] = true
		status := "disabled"
		if !node.OverAllowance && c.Status.Serviceable() && acc.Enabled && iface.Enabled {
			status = "active"
		}
		tag := iface.XUI.V.Tag
		gid, ok := groupOf[tag]
		if !ok {
			g, err := api.createGroup(ctx, pgGroupName(tag), tag)
			if err != nil {
				failures = append(failures, fmt.Sprintf("making the group for %s: %v", tag, err))
				continue
			}
			gid = g.ID
			groupOf[tag] = gid
		}

		u, exists := remote[name]
		changed := false
		switch {
		case !exists:
			made, err := api.createUser(ctx, name, status, c.Name, gid)
			if err != nil {
				failures = append(failures, fmt.Sprintf("adding %s: %v", name, err))
				continue
			}
			u, changed = made, true
		case u.Status != status || !sameGroups(u.GroupIDs, gid):
			// Active or disabled, the two states it is told: a user there is
			// limited or expired only by limits this panel never sets.
			if err := api.modifyUser(ctx, name, status, gid); err != nil {
				failures = append(failures, fmt.Sprintf("updating %s: %v", name, err))
				continue
			}
			changed = true
		}

		if exists {
			usage = append(usage, service.XUIUsage{
				AccountID: acc.ID, ClientID: acc.ClientID,
				Down:   since(u.UsedTraffic, acc.XUIUp),
				SeenUp: u.UsedTraffic,
			})
		}
		if status == "active" && (changed || refreshLinks || moved[acc.InterfaceID] || acc.XUILink == "") {
			got, err := api.links(ctx, u.SubscriptionURL)
			if err != nil {
				failures = append(failures, fmt.Sprintf("reading the links of %s: %v", name, err))
			} else if joined := strings.Join(got, "\n"); joined != acc.XUILink && joined != "" {
				links[acc.ID] = joined
			}
		}
	}

	for name := range remote {
		if want[name] {
			continue
		}
		if err := api.deleteUser(ctx, name); err != nil {
			failures = append(failures, fmt.Sprintf("removing %s: %v", name, err))
		}
	}

	// Its usage is one number a user, used_traffic, kept in the counter that
	// holds a 3x-ui client's upload; it is charged as download.
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

func sameGroups(have []int, want int) bool {
	return len(have) == 1 && have[0] == want
}

// askPG is the prober's question to a Pasarguard node: its system, and
// whether a node of its own is connected to carry anything.
func askPG(ctx context.Context, node model.Node) (Snapshot, error) {
	var snap Snapshot
	api, err := newPGAPI(node)
	if err != nil {
		return snap, err
	}
	start := time.Now()
	sys, err := api.system(ctx)
	snap.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		return snap, err
	}
	snap.Version = "Pasarguard " + sys.Version
	snap.UptimeSec = sys.Uptime
	snap.CPUPercent = sys.CPU
	if sys.MemTotal > 0 {
		snap.MemPercent = float64(sys.MemUsed) * 100 / float64(sys.MemTotal)
	}
	snap.Enforcement = "reduced"
	ns, err := api.nodes(ctx)
	if err != nil {
		return snap, err
	}
	for _, n := range ns {
		if n.Status == "connected" {
			return snap, nil
		}
	}
	if len(ns) == 0 {
		return snap, fmt.Errorf("it has no node of its own to carry anything")
	}
	return snap, fmt.Errorf("none of its nodes is connected (%s: %s)", ns[0].Name, ns[0].Status)
}
