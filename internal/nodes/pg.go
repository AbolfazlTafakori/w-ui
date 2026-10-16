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

func (s *Syncer) pgOne(ctx context.Context, node model.Node) {
	s.report(node, s.pgRound(ctx, node))
}

func (s *Syncer) pgRound(ctx context.Context, node model.Node) error {
	tag, err := service.PanelTag(ctx, s.db)
	if err != nil {
		return fmt.Errorf("this panel's tag: %w", err)
	}
	api, err := newPGAPI(ctx, node)
	if err != nil {
		return err
	}
	return s.xrayRound(ctx, node, &pgPanel{s: s, node: node, tag: tag, api: api})
}

// pgPanel is a Pasarguard node for one round.
type pgPanel struct {
	s    *Syncer
	node model.Node
	tag  string
	api  *pgAPI

	// groupOf is this panel's group for each inbound, by its tag.
	groupOf map[string]int
	// ours are the users this panel made there; everyone every user, read
	// only when some were brought in from there.
	ours     map[string]pgUser
	everyone map[string]*pgUser
}

func (p *pgPanel) read(ctx context.Context) ([]service.XUIInboundSeen, error) {
	inbounds, err := p.api.inbounds(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading its inbounds: %w", err)
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
		if k, ok := service.InboundKindFor(model.KindPG, p.node.Version, in.Protocol); ok {
			x.Customers = k.Customers
		}
		if x.Network == "" {
			x.Network = "tcp"
		}
		seen = append(seen, service.XUIInboundSeen{XUIInbound: x, Port: in.portOf()})
	}
	return seen, nil
}

// prepare reads this panel's groups there, and its users: its own, and the
// ones brought in from there, by name.
func (p *pgPanel) prepare(ctx context.Context, files []xrayFile) error {
	p.groupOf = map[string]int{}
	if len(files) > 0 {
		gs, err := p.api.groups(ctx)
		if err != nil {
			return fmt.Errorf("reading its groups: %w", err)
		}
		for _, g := range gs {
			if len(g.InboundTags) == 1 && g.Name == pgGroupName(g.InboundTags[0]) {
				p.groupOf[g.InboundTags[0]] = g.ID
			}
		}
	}
	users, err := p.api.ourUsers(ctx)
	if err != nil {
		return fmt.Errorf("reading its users: %w", err)
	}
	var brought []string
	for _, f := range files {
		if f.acc.Adopted != "" {
			brought = append(brought, f.acc.Adopted)
		}
	}
	if len(brought) > 0 {
		theirs, err := p.api.usersNamed(ctx, brought)
		if err != nil {
			return fmt.Errorf("reading the users brought in: %w", err)
		}
		users = append(users, theirs...)
	}
	p.ours = make(map[string]pgUser, len(users))
	p.everyone = make(map[string]*pgUser, len(users))
	for i, u := range users {
		p.everyone[u.Username] = &users[i]
		if service.PGOwns(p.tag, u.Username) {
			p.ours[u.Username] = u
		}
	}
	return nil
}

func (p *pgPanel) own(ctx context.Context, f xrayFile) xrayResult {
	name := service.PGUsername(p.tag, &f.acc)
	res := xrayResult{name: name}
	status := "disabled"
	if f.on {
		status = "active"
	}
	inbound := f.iface.XUI.V.Tag
	gid, ok := p.groupOf[inbound]
	if !ok {
		g, err := p.api.createGroup(ctx, pgGroupName(inbound), inbound)
		if err != nil {
			res.err = fmt.Errorf("making the group for %s: %v", inbound, err)
			return res
		}
		gid = g.ID
		p.groupOf[inbound] = gid
	}
	u, exists := p.ours[name]
	switch {
	case !exists:
		made, err := p.api.createUser(ctx, name, status, f.client.Name, gid)
		if err != nil {
			res.err = fmt.Errorf("adding %s: %v", name, err)
			return res
		}
		u, res.changed = made, true
	case u.Status != status || !sameGroups(u.GroupIDs, gid):
		// Active or disabled, the two states it is told: a user there is
		// limited or expired only by limits this panel never sets.
		if err := p.api.modifyUser(ctx, name, status, gid); err != nil {
			res.err = fmt.Errorf("updating %s: %v", name, err)
			return res
		}
		res.changed = true
	}
	// Its usage is one number a user, used_traffic, kept in the counter that
	// holds a 3x-ui client's upload; it is charged as download.
	if exists {
		res.usage = &service.XUIUsage{AccountID: f.acc.ID, ClientID: f.acc.ClientID,
			Down: since(u.UsedTraffic, f.acc.XUIUp), SeenUp: u.UsedTraffic}
	}
	if f.on && (res.changed || f.links) {
		// The list of users leaves their subscription link out.
		if u.SubscriptionURL == "" {
			if full, err := p.api.user(ctx, name); err == nil {
				u.SubscriptionURL = full.SubscriptionURL
			}
		}
		got, err := p.api.links(ctx, u.SubscriptionURL)
		if err != nil {
			res.err = fmt.Errorf("reading the links of %s: %v", name, err)
		} else if joined := strings.Join(got, "\n"); joined != f.acc.XUILink && joined != "" {
			res.link = joined
		}
	}
	return res
}

func (p *pgPanel) adopt(ctx context.Context, f xrayFile, marks map[uint]adoptedMark) xrayResult {
	u, link, err := p.s.pgAdopted(ctx, p.api, &f.acc, p.everyone[f.acc.Adopted], f.on, f.links, marks)
	return xrayResult{usage: u, link: link, err: err}
}

// sweep takes away the users that are this panel's there and no file's here.
func (p *pgPanel) sweep(ctx context.Context, wanted map[string]bool) (bool, []string) {
	changed := false
	var failures []string
	for name := range p.ours {
		if wanted[name] {
			continue
		}
		changed = true
		if err := p.api.deleteUser(ctx, name); err != nil {
			failures = append(failures, fmt.Sprintf("removing %s: %v", name, err))
		}
	}
	return changed, failures
}

func (p *pgPanel) finish(context.Context, bool, []xrayFile) []string { return nil }

func sameGroups(have []int, want int) bool {
	return len(have) == 1 && have[0] == want
}

// askPG is the prober's question to a Pasarguard node: its system, and
// whether a node of its own is connected to carry anything.
func askPG(ctx context.Context, node model.Node) (Snapshot, error) {
	var snap Snapshot
	api, err := newPGAPI(ctx, node)
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
