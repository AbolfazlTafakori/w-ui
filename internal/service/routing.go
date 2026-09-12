package service

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/routing"
)

// Setting keys for the routing that is configured rather than ruled.
const (
	keyBlockBitTorrent = "routing.blockBitTorrent"
	keyBlockIPs        = "routing.blockIps"
	keyBlockDomains    = "routing.blockDomains"
	keyBlockPorts      = "routing.blockPorts"
	keyDirectIPs       = "routing.directIps"
	keyDirectDomains   = "routing.directDomains"
	keyDefaultOutbound = "routing.defaultOutbound"
	keyIPv4Domains     = "routing.ipv4Domains"
)

// domainRefresh is how often blocked and pinned names are looked up again.
//
// Long enough that a busy panel is not a resolver's worst customer, short
// enough that a service moving address is followed within the hour. A failed
// lookup keeps the previous answer rather than emptying the set: a resolver
// blip should not briefly unblock everything an operator forbade.
const domainRefresh = 15 * time.Minute

// BasicRouting is the routing an operator sets with switches and lists rather
// than with rules.
type BasicRouting struct {
	BlockBitTorrent bool     `json:"blockBitTorrent"`
	BlockIPs        []string `json:"blockIps"`
	BlockDomains    []string `json:"blockDomains"`
	BlockPorts      []string `json:"blockPorts"`
	DirectIPs       []string `json:"directIps"`
	DirectDomains   []string `json:"directDomains"`
	// IPv4Domains are names resolved over IPv4 only and sent direct -- the
	// row 3x-ui calls IPv4 Routing, for services that misbehave over v6.
	IPv4Domains     []string `json:"ipv4Domains"`
	DefaultOutbound string   `json:"defaultOutbound"`
}

// Routing owns the policy: what is blocked, what is pinned, and which outbound
// carries the rest.
type Routing struct {
	db  *gorm.DB
	log *slog.Logger

	mu sync.RWMutex
	// resolved caches what each name last pointed at, so a resolver that is
	// briefly unreachable does not empty the sets.
	resolved    map[string][]netip.Prefix
	lastResolve time.Time
	// panelTag is the outbound carrying the panel's own traffic; empty is
	// direct.
	panelTag string
	// domainStrategy is which families names resolve to: AsIs, UseIPv4 or
	// UseIPv6.
	domainStrategy string
}

// SetDomainStrategy chooses the families names in lists resolve to.
func (s *Routing) SetDomainStrategy(v string) {
	s.mu.Lock()
	s.domainStrategy = v
	s.mu.Unlock()
}

func NewRouting(db *gorm.DB, log *slog.Logger) *Routing {
	return &Routing{db: db, log: log, resolved: map[string][]netip.Prefix{}}
}

// Basic returns the switch-and-list routing.
func (s *Routing) Basic(ctx context.Context) (BasicRouting, error) {
	out := BasicRouting{DefaultOutbound: TagDirect}

	var rows []model.Setting
	if err := s.db.WithContext(ctx).
		Where("key LIKE ?", "routing.%").Find(&rows).Error; err != nil {
		return out, fmt.Errorf("service: read routing settings: %w", err)
	}
	stored := make(map[string]string, len(rows))
	for _, r := range rows {
		stored[r.Key] = r.Value
	}

	out.BlockBitTorrent = stored[keyBlockBitTorrent] == "true"
	out.BlockIPs = splitList(stored[keyBlockIPs])
	out.BlockDomains = splitList(stored[keyBlockDomains])
	out.BlockPorts = splitList(stored[keyBlockPorts])
	out.DirectIPs = splitList(stored[keyDirectIPs])
	out.DirectDomains = splitList(stored[keyDirectDomains])
	out.IPv4Domains = splitList(stored[keyIPv4Domains])
	// The default is the first outbound in the list, as Xray's is: moving a
	// hop to the top of the outbounds page makes it carry everything not
	// matched by a rule. Nothing is stored for it; the order is the setting.
	out.DefaultOutbound = s.firstOutbound(ctx)
	return out, nil
}

// firstOutbound is the tag at the top of the outbounds list -- the default.
func (s *Routing) firstOutbound(ctx context.Context) string {
	var ob model.Outbound
	err := s.db.WithContext(ctx).Order("position, id").Limit(1).Find(&ob).Error
	if err != nil || ob.ID == 0 {
		return TagDirect
	}
	return ob.Tag
}

// makeFirst moves an outbound to the top of the list, which is how the
// default is chosen. A balancer cannot be moved there; it is not a row on
// that page.
func (s *Routing) makeFirst(ctx context.Context, tag string) error {
	var rows []model.Outbound
	if err := s.db.WithContext(ctx).Order("position, id").Find(&rows).Error; err != nil {
		return fmt.Errorf("service: read outbounds: %w", err)
	}
	ids := make([]uint, 0, len(rows))
	var chosen uint
	for _, o := range rows {
		if o.Tag == tag {
			chosen = o.ID
			continue
		}
		ids = append(ids, o.ID)
	}
	if chosen == 0 {
		return invalidField("defaultOutbound", "%q is a balancer; the default has to be an outbound. Point a rule at the balancer instead", tag)
	}
	ids = append([]uint{chosen}, ids...)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.Outbound{}).Where("id = ?", id).Update("position", i).Error; err != nil {
				return fmt.Errorf("service: reorder outbounds: %w", err)
			}
		}
		return nil
	})
}

// SaveBasic validates and stores it.
//
// Everything is checked before anything is written. A list where the thirtieth
// entry is a typo must not leave the first twenty-nine applied and the rest
// discarded, because the operator would have no way to tell which state they
// are now in.
func (s *Routing) SaveBasic(ctx context.Context, in BasicRouting) (BasicRouting, error) {
	if _, err := routing.ParseTargets(in.BlockIPs); err != nil {
		return BasicRouting{}, fieldWrap("blockIps", err)
	}
	if _, err := routing.ParseTargets(in.DirectIPs); err != nil {
		return BasicRouting{}, fieldWrap("directIps", err)
	}
	if _, err := routing.ParsePorts(in.BlockPorts); err != nil {
		return BasicRouting{}, fieldWrap("blockPorts", err)
	}
	for _, d := range append(append(append([]string{}, in.BlockDomains...), in.DirectDomains...), in.IPv4Domains...) {
		if err := checkDomain(d); err != nil {
			return BasicRouting{}, err
		}
	}

	tag := strings.TrimSpace(in.DefaultOutbound)
	if tag == "" {
		tag = TagDirect
	}
	if enabled, ok, err := s.targetExists(ctx, tag); err != nil {
		return BasicRouting{}, err
	} else if !ok {
		return BasicRouting{}, invalidField("defaultOutbound",
			"there is no outbound or balancer called %q", tag)
	} else if !enabled {
		return BasicRouting{}, invalidField("defaultOutbound",
			"%q is switched off; everything not matched by a rule would stop working", tag)
	}

	writes := map[string]string{
		keyBlockBitTorrent: strconv.FormatBool(in.BlockBitTorrent),
		keyBlockIPs:        joinList(in.BlockIPs),
		keyBlockDomains:    joinList(in.BlockDomains),
		keyBlockPorts:      joinList(in.BlockPorts),
		keyDirectIPs:       joinList(in.DirectIPs),
		keyDirectDomains:   joinList(in.DirectDomains),
		keyIPv4Domains:     joinList(in.IPv4Domains),
		keyDefaultOutbound: tag,
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for k, v := range writes {
			row := model.Setting{Key: k, Value: v, UpdatedAt: time.Now().UTC()}
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return BasicRouting{}, fmt.Errorf("service: save routing settings: %w", err)
	}
	// Choosing the default is moving that outbound to the top of the list.
	if tag != s.firstOutbound(ctx) {
		if err := s.makeFirst(ctx, tag); err != nil {
			return BasicRouting{}, err
		}
	}

	s.namesChanged(ctx)

	s.log.Info("routing policy changed", "defaultOutbound", tag,
		"blockedNames", len(in.BlockDomains), "pinnedNames", len(in.DirectDomains))
	return s.Basic(ctx)
}

// namesChanged re-resolves the policy's domains without waiting for the timer.
//
// A rule that matches a domain matches the addresses that name resolves to, and
// until the resolver has seen it the rule matches nothing at all. Adding a rule
// and finding it does nothing for the next quarter of an hour is not a delay an
// operator would attribute to caching -- they would reasonably conclude the
// feature is broken and go and file that.
//
// Run in the background: the caller is answering an HTTP request and should not
// wait on somebody else's name server.
func (s *Routing) namesChanged(ctx context.Context) {
	go s.refreshDomains(context.WithoutCancel(ctx))
}

// ── rules ────────────────────────────────────────────────────────────────────

// ListRules returns the rules in evaluation order.
func (s *Routing) ListRules(ctx context.Context) ([]model.RoutingRule, error) {
	var out []model.RoutingRule
	err := s.db.WithContext(ctx).Order("position, id").Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("service: list routing rules: %w", err)
	}
	return out, nil
}

// RuleInput is what the form collects.
type RuleInput struct {
	Name        string `json:"name"`
	Enabled     *bool  `json:"enabled"`
	SourceIPs   string `json:"sourceIps"`
	SourcePorts string `json:"sourcePorts"`
	Network     string `json:"network"`
	DestIPs     string `json:"destIps"`
	Domains     string `json:"domains"`
	Ports       string `json:"ports"`
	Clients     string `json:"clients"`
	Groups      string `json:"groups"`
	Interfaces  string `json:"interfaces"`
	OutboundTag string `json:"outboundTag"`
	Note        string `json:"note"`
}

func (in RuleInput) columns() map[string]any {
	return map[string]any{
		"name":         in.Name,
		"source_ips":   in.SourceIPs,
		"source_ports": in.SourcePorts,
		"network":      in.Network,
		"dest_ips":     in.DestIPs,
		"domains":      in.Domains,
		"ports":        in.Ports,
		"clients":      in.Clients,
		"groups":       in.Groups,
		"interfaces":   in.Interfaces,
		"outbound_tag": in.OutboundTag,
		"note":         strings.TrimSpace(in.Note),
		"match":        "",
		"value":        "",
	}
}

// CreateRule adds a rule at the end of the list.
func (s *Routing) CreateRule(ctx context.Context, in RuleInput) (*model.RoutingRule, error) {
	if err := s.validateRule(ctx, &in); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)

	var last model.RoutingRule
	if err := db.Order("position DESC").Limit(1).Find(&last).Error; err != nil {
		return nil, fmt.Errorf("service: read rule order: %w", err)
	}

	rule := model.RoutingRule{
		Name:        in.Name,
		Enabled:     in.Enabled == nil || *in.Enabled,
		Position:    last.Position + 1,
		SourceIPs:   in.SourceIPs,
		SourcePorts: in.SourcePorts,
		Network:     in.Network,
		DestIPs:     in.DestIPs,
		Domains:     in.Domains,
		Ports:       in.Ports,
		Clients:     in.Clients,
		Groups:      in.Groups,
		Interfaces:  in.Interfaces,
		OutboundTag: in.OutboundTag,
		Note:        strings.TrimSpace(in.Note),
	}
	if err := db.Create(&rule).Error; err != nil {
		return nil, fmt.Errorf("service: create routing rule: %w", err)
	}
	s.namesChanged(ctx)
	s.log.Info("routing rule added", "name", rule.Name, "to", rule.OutboundTag)
	return &rule, nil
}

// UpdateRule changes one.
func (s *Routing) UpdateRule(ctx context.Context, id uint, in RuleInput) (*model.RoutingRule, error) {
	db := s.db.WithContext(ctx)

	var rule model.RoutingRule
	if err := db.First(&rule, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no routing rule %d", ErrNotFound, id)
	}
	if err := s.validateRule(ctx, &in); err != nil {
		return nil, err
	}

	updates := in.columns()
	updates["updated_at"] = time.Now().UTC()
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if err := db.Model(&rule).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("service: update routing rule: %w", err)
	}
	if err := db.First(&rule, id).Error; err != nil {
		return nil, fmt.Errorf("service: reload routing rule: %w", err)
	}
	s.namesChanged(ctx)
	return &rule, nil
}

// DeleteRule removes one.
func (s *Routing) DeleteRule(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&model.RoutingRule{}, id)
	if res.Error != nil {
		return fmt.Errorf("service: delete routing rule: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: no routing rule %d", ErrNotFound, id)
	}
	s.namesChanged(ctx)
	return nil
}

// ReorderRules sets evaluation order. First match wins, so this changes
// behaviour and not merely appearance.
func (s *Routing) ReorderRules(ctx context.Context, ids []uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.RoutingRule{}).Where("id = ?", id).
				Update("position", i).Error; err != nil {
				return fmt.Errorf("service: reorder rules: %w", err)
			}
		}
		return nil
	})
}

// MigrateRules moves rules stored in the old one-criterion form into the
// fields. Run once at start; a rule already in the new form is untouched.
func (s *Routing) MigrateRules(ctx context.Context) error {
	var rows []model.RoutingRule
	if err := s.db.WithContext(ctx).Where("match <> ''").Find(&rows).Error; err != nil {
		return fmt.Errorf("service: read old rules: %w", err)
	}
	for _, r := range rows {
		col := map[model.RouteMatchKind]string{
			model.MatchDomain:   "domains",
			model.MatchIP:       "dest_ips",
			model.MatchPort:     "ports",
			model.MatchProtocol: "network",
			model.MatchClient:   "clients",
			model.MatchGroup:    "groups",
		}[r.Match]
		if col == "" {
			continue
		}
		err := s.db.WithContext(ctx).Model(&r).Updates(map[string]any{col: r.Value, "match": "", "value": ""}).Error
		if err != nil {
			return fmt.Errorf("service: migrate rule %d: %w", r.ID, err)
		}
	}
	if len(rows) > 0 {
		s.log.Info("routing rules migrated to the criteria form", "rules", len(rows))
	}
	return nil
}

func (s *Routing) validateRule(ctx context.Context, in *RuleInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.OutboundTag = strings.TrimSpace(in.OutboundTag)
	in.Network = strings.ToLower(strings.TrimSpace(in.Network))
	for _, f := range []*string{&in.SourceIPs, &in.SourcePorts, &in.DestIPs, &in.Domains, &in.Ports, &in.Clients, &in.Groups, &in.Interfaces} {
		*f = joinList(splitList(*f))
	}

	if in.Name == "" {
		return invalidField("name", "give the rule a comment so the list can be read later")
	}
	if in.SourceIPs == "" && in.SourcePorts == "" && in.Network == "" && in.DestIPs == "" &&
		in.Domains == "" && in.Ports == "" && in.Clients == "" && in.Groups == "" && in.Interfaces == "" {
		return invalidField("destIps", "the rule matches nothing as written; fill in at least one criterion")
	}

	if _, err := routing.ParseTargets(splitList(in.SourceIPs)); err != nil {
		return fieldWrap("sourceIps", err)
	}
	if _, err := routing.ParseTargets(splitList(in.DestIPs)); err != nil {
		return fieldWrap("destIps", err)
	}
	if _, err := routing.ParsePorts(splitList(in.SourcePorts)); err != nil {
		return fieldWrap("sourcePorts", err)
	}
	if _, err := routing.ParsePorts(splitList(in.Ports)); err != nil {
		return fieldWrap("ports", err)
	}
	switch in.Network {
	case "", "tcp", "udp", "icmp":
	default:
		return invalidField("network", "%q is not a network the router matches; use tcp, udp or icmp", in.Network)
	}
	if in.Network == "icmp" && (in.Ports != "" || in.SourcePorts != "") {
		return invalidField("network", "icmp has no ports; drop the ports or pick tcp or udp")
	}
	for _, d := range splitList(in.Domains) {
		if err := checkDomain(d); err != nil {
			return fieldWrap("domains", err)
		}
	}
	for _, c := range splitList(in.Clients) {
		if _, err := strconv.ParseUint(c, 10, 64); err != nil {
			return invalidField("clients", "a client is named by id here, and %q is not one", c)
		}
	}
	for _, g := range splitList(in.Groups) {
		var n int64
		if err := s.db.WithContext(ctx).Model(&model.Group{}).Where("name = ?", g).Count(&n).Error; err != nil {
			return fmt.Errorf("service: check group: %w", err)
		}
		if n == 0 {
			return invalidField("groups", "there is no group called %q", g)
		}
	}
	for _, i := range splitList(in.Interfaces) {
		id, err := strconv.ParseUint(i, 10, 64)
		if err != nil {
			return invalidField("interfaces", "an inbound is named by id here, and %q is not one", i)
		}
		var n int64
		if err := s.db.WithContext(ctx).Model(&model.Interface{}).Where("id = ?", id).Count(&n).Error; err != nil {
			return fmt.Errorf("service: check interface: %w", err)
		}
		if n == 0 {
			return invalidField("interfaces", "there is no inbound with id %s", i)
		}
	}

	if _, ok, err := s.targetExists(ctx, in.OutboundTag); err != nil {
		return err
	} else if !ok {
		return invalidField("outboundTag", "there is no outbound or balancer called %q", in.OutboundTag)
	}
	return nil
}

// targetExists reports whether a tag names an outbound or a balancer, and
// whether that one is enabled.
func (s *Routing) targetExists(ctx context.Context, tag string) (enabled, ok bool, err error) {
	var ob model.Outbound
	if err := s.db.WithContext(ctx).Where("tag = ?", tag).Limit(1).Find(&ob).Error; err != nil {
		return false, false, fmt.Errorf("service: check rule outbound: %w", err)
	}
	if ob.ID != 0 {
		return ob.Enabled, true, nil
	}
	var bl model.Balancer
	if err := s.db.WithContext(ctx).Where("tag = ?", tag).Limit(1).Find(&bl).Error; err != nil {
		return false, false, fmt.Errorf("service: check rule balancer: %w", err)
	}
	if bl.ID != 0 {
		return bl.Enabled, true, nil
	}
	return false, false, nil
}

// ── building the policy ──────────────────────────────────────────────────────

// Policy assembles everything the kernel needs from the current configuration.
//
// Called by the reconciler on every tick. It is a read of the database and the
// resolver cache and nothing else, so it cannot block on the network however
// slow a name server is being.
func (s *Routing) Policy(ctx context.Context) (routing.Policy, error) {
	var p routing.Policy

	basic, err := s.Basic(ctx)
	if err != nil {
		return p, err
	}

	// A malformed stored entry is skipped rather than fatal: it was validated
	// when it was saved, and refusing to route at all because one row went bad
	// would take the whole server down over a typo.
	p.BlockAddrs, _ = routing.ParseTargets(basic.BlockIPs)
	p.DirectAddrs, _ = routing.ParseTargets(basic.DirectIPs)
	p.BlockPorts, _ = routing.ParsePorts(basic.BlockPorts)
	p.BlockBitTorrent = basic.BlockBitTorrent

	s.mu.RLock()
	for _, d := range basic.BlockDomains {
		p.BlockAddrs = append(p.BlockAddrs, s.resolved[d]...)
	}
	for _, d := range basic.DirectDomains {
		p.DirectAddrs = append(p.DirectAddrs, s.resolved[d]...)
	}
	// IPv4 Routing: those names' IPv4 addresses go direct, so a service
	// that misbehaves over IPv6 reaches the customer over the server's own
	// v4 address.
	for _, d := range basic.IPv4Domains {
		for _, pfx := range s.resolved[d] {
			if pfx.Addr().Is4() {
				p.DirectAddrs = append(p.DirectAddrs, pfx)
			}
		}
	}
	s.mu.RUnlock()

	db := s.db.WithContext(ctx)

	// The tunnel subnets. Marking is confined to these, which is what keeps
	// this panel's routing away from everything else on the machine.
	var ifaces []model.Interface
	if err := db.Where("enabled = ?", true).Find(&ifaces).Error; err != nil {
		return p, fmt.Errorf("service: read interfaces: %w", err)
	}
	for _, i := range ifaces {
		if pfx, err := netip.ParsePrefix(i.Subnet); err == nil {
			p.CustomerNets = append(p.CustomerNets, pfx)
			p.TunnelDevices = append(p.TunnelDevices, i.Name)
		}
	}

	var outbounds []model.Outbound
	if err := db.Order("position, id").Find(&outbounds).Error; err != nil {
		return p, fmt.Errorf("service: read outbounds: %w", err)
	}
	// Where a tag sends traffic: the mark to set, whether it drops, and
	// whether it is switched on. Outbounds and balancers share the space.
	type target struct {
		mark    uint32
		drop    bool
		enabled bool
	}
	byTag := make(map[string]target, len(outbounds))
	byOutbound := make(map[string]model.Outbound, len(outbounds))
	for _, o := range outbounds {
		byOutbound[o.Tag] = o
		byTag[o.Tag] = target{mark: o.Mark, drop: o.Kind == model.OutboundBlock, enabled: o.Enabled}
		if o.Kind.NeedsHop() && o.Mark != 0 {
			_, table, err := routing.AllocateMark(o.ID)
			if err != nil {
				continue
			}
			p.Hops = append(p.Hops, routing.Hop{
				Tag:     o.Tag,
				Mark:    o.Mark,
				Table:   table,
				Device:  hopDevice(o),
				Enabled: o.Enabled,
			})
		}
	}

	var balancers []model.Balancer
	if err := db.Order("id").Find(&balancers).Error; err != nil {
		return p, fmt.Errorf("service: read balancers: %w", err)
	}
	for _, b := range balancers {
		if b.Mark == 0 {
			continue
		}
		_, table, err := routing.AllocateMark(balancerMarkID(b.ID))
		if err != nil {
			continue
		}
		hop := routing.Hop{Tag: b.Tag, Mark: b.Mark, Table: table, Enabled: b.Enabled}
		hop.Nexthops = s.balancerDevices(b, byOutbound)
		if len(hop.Nexthops) == 1 {
			hop.Device, hop.Nexthops = hop.Nexthops[0], nil
		}
		p.Hops = append(p.Hops, hop)
		byTag[b.Tag] = target{mark: b.Mark, enabled: b.Enabled && (hop.Device != "" || len(hop.Nexthops) > 0)}
	}

	if def, ok := byTag[basic.DefaultOutbound]; ok && def.enabled {
		p.DefaultMark = def.mark
	}

	p.NoCounters = !OutboundCounters.Load()

	// The panel's own traffic. Only a hop with a device can carry it; the
	// endpoints of every hop stay direct so no tunnel is built through itself.
	p.PanelUID = -1
	if tag := s.panelOutbound(); tag != "" {
		if tg, ok := byTag[tag]; ok && tg.enabled && tg.mark != 0 && !tg.drop {
			p.PanelMark = tg.mark
			p.PanelUID = os.Getuid()
			for _, o := range outbounds {
				p.PanelExclude = append(p.PanelExclude, s.endpointPrefixes(o.Address)...)
			}
		}
	}

	rules, err := s.ListRules(ctx)
	if err != nil {
		return p, err
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		tg, ok := byTag[r.OutboundTag]
		if !ok || !tg.enabled {
			// A rule pointing at an outbound that is gone or switched off is
			// skipped rather than applied as a drop. Silently discarding a
			// customer's traffic because an operator disabled a hop is the
			// worst of the available behaviours.
			continue
		}
		mr := routing.MarkRule{Mark: tg.mark, Drop: tg.drop}
		if !s.fillMatch(ctx, &mr, r, ifaces) {
			continue
		}
		p.Rules = append(p.Rules, mr)
	}

	return p, nil
}

// SetPanelOutbound tells the policy which outbound carries the panel's own
// traffic. Read by the settings page's save, so it takes effect on the next
// tick without a restart.
func (s *Routing) SetPanelOutbound(tag string) {
	s.mu.Lock()
	s.panelTag = strings.TrimSpace(tag)
	s.mu.Unlock()
}

func (s *Routing) panelOutbound() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.panelTag
}

// endpointPrefixes turns an outbound's address into the prefixes that must
// stay direct: the address itself, or -- for a name -- whatever the resolver
// last found for it.
func (s *Routing) endpointPrefixes(address string) []netip.Prefix {
	host := strings.TrimSpace(address)
	if host == "" {
		return nil
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Prefix{netip.PrefixFrom(addr, addr.BitLen())}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolved[host]
}

// balancerDevices is the devices a balancer's traffic may leave through:
// its enabled members that have one, or -- for leastPing -- only the one
// that answered the last check fastest.
func (s *Routing) balancerDevices(b model.Balancer, byOutbound map[string]model.Outbound) []string {
	return pickBalancerDevices(b, byOutbound)
}

// pickBalancerDevices is where a balancer's traffic goes right now: the
// override if one is set and up; else the members that are up -- all of
// them for random, the fastest for leastPing; else the fallback, if it is
// up; else nowhere.
func pickBalancerDevices(b model.Balancer, byOutbound map[string]model.Outbound) []string {
	up := func(tag string) (model.Outbound, bool) {
		o, ok := byOutbound[tag]
		if !ok || !o.Enabled || hopDevice(o) == "" || o.LastError != "" {
			return o, false
		}
		return o, true
	}
	if b.Override != "" {
		if o, ok := up(b.Override); ok {
			return []string{hopDevice(o)}
		}
	}
	var devs []string
	var best *model.Outbound
	for _, tag := range splitList(b.Members) {
		o, ok := up(tag)
		if !ok {
			continue
		}
		if b.Strategy == model.BalancerLeastPing {
			if o.LatencyMS > 0 && (best == nil || o.LatencyMS < best.LatencyMS) {
				oc := o
				best = &oc
			}
			continue
		}
		devs = append(devs, hopDevice(o))
	}
	if b.Strategy == model.BalancerLeastPing && best != nil {
		devs = []string{hopDevice(*best)}
	}
	if len(devs) == 0 && b.Fallback != "" {
		if o, ok := up(b.Fallback); ok {
			devs = []string{hopDevice(o)}
		}
	}
	return devs
}

// balancerMarkID keeps balancer marks clear of outbound marks: the two
// share one 16-bit space, so balancers live in its upper half.
func balancerMarkID(id uint) uint { return 0x8000 + id }

// fillMatch turns a stored rule into a kernel match. It reports whether the
// rule ended up matching anything at all: a criterion that names something
// which resolves to nothing -- a domain the resolver has not seen, a group
// with no customers -- makes the whole rule match nothing, rather than
// being dropped from the conjunction and widening it.
func (s *Routing) fillMatch(ctx context.Context, mr *routing.MarkRule, r model.RoutingRule, ifaces []model.Interface) bool {
	if r.DestIPs != "" {
		mr.Addrs, _ = routing.ParseTargets(splitList(r.DestIPs))
		if len(mr.Addrs) == 0 {
			return false
		}
	}
	if r.Domains != "" {
		s.mu.RLock()
		var got []netip.Prefix
		for _, d := range splitList(r.Domains) {
			got = append(got, s.resolved[d]...)
		}
		s.mu.RUnlock()
		if len(got) == 0 {
			return false
		}
		mr.Addrs = append(mr.Addrs, got...)
	}
	if r.Ports != "" {
		mr.Ports, _ = routing.ParsePorts(splitList(r.Ports))
		if len(mr.Ports) == 0 {
			return false
		}
	}
	if r.SourcePorts != "" {
		mr.SourcePorts, _ = routing.ParsePorts(splitList(r.SourcePorts))
		if len(mr.SourcePorts) == 0 {
			return false
		}
	}
	mr.Protocol = strings.ToLower(r.Network)
	if r.SourceIPs != "" {
		mr.Sources, _ = routing.ParseTargets(splitList(r.SourceIPs))
		if len(mr.Sources) == 0 {
			return false
		}
	}
	if r.Clients != "" || r.Groups != "" {
		addrs := s.addressesFor(ctx, r)
		if len(addrs) == 0 {
			return false
		}
		for _, a := range addrs {
			mr.Sources = append(mr.Sources, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	if r.Interfaces != "" {
		byID := map[string]string{}
		for _, i := range ifaces {
			byID[strconv.FormatUint(uint64(i.ID), 10)] = i.Name
		}
		for _, id := range splitList(r.Interfaces) {
			if name := byID[id]; name != "" {
				mr.Inbounds = append(mr.Inbounds, name)
			}
		}
		if len(mr.Inbounds) == 0 {
			return false
		}
	}
	return !mr.Empty()
}

// addressesFor resolves a client or group rule to the tunnel addresses it
// covers, which is what the kernel can actually match on.
func (s *Routing) addressesFor(ctx context.Context, r model.RoutingRule) []netip.Addr {
	q := s.db.WithContext(ctx).Model(&model.Account{}).
		Joins("JOIN clients ON clients.id = accounts.client_id")

	// Customers named directly, or through a group -- either way in.
	ids := splitList(r.Clients)
	groups := splitList(r.Groups)
	switch {
	case len(ids) > 0 && len(groups) > 0:
		q = q.Where("accounts.client_id IN ? OR clients.\"group\" IN ?", ids, groups)
	case len(ids) > 0:
		q = q.Where("accounts.client_id IN ?", ids)
	case len(groups) > 0:
		q = q.Where("clients.\"group\" IN ?", groups)
	default:
		return nil
	}

	var addrs []string
	if err := q.Pluck("accounts.address", &addrs).Error; err != nil {
		s.log.Warn("could not read addresses for a routing rule",
			"rule", r.Name, "error", err)
		return nil
	}

	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		// Stored with a prefix length; the kernel match wants the address.
		if pfx, err := netip.ParsePrefix(a); err == nil {
			out = append(out, pfx.Addr())
			continue
		}
		if addr, err := netip.ParseAddr(a); err == nil {
			out = append(out, addr)
		}
	}
	return out
}

func hopDevice(o model.Outbound) string {
	if !o.Kind.NeedsHop() {
		return ""
	}
	return fmt.Sprintf("wuih%d", o.ID)
}

// HopDevice is the interface name an outbound's hop uses: a WireGuard
// device, or the tun an openvpn or xray process owns.
func HopDevice(o model.Outbound) string { return hopDevice(o) }

// ── name resolution ──────────────────────────────────────────────────────────

// StartResolver keeps the name-to-address cache fresh until ctx is done.
func (s *Routing) StartResolver(ctx context.Context) {
	s.refreshDomains(ctx)

	go func() {
		t := time.NewTicker(domainRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.refreshDomains(ctx)
			}
		}
	}()
}

// refreshDomains looks up every name the policy mentions.
func (s *Routing) refreshDomains(ctx context.Context) {
	basic, err := s.Basic(ctx)
	if err != nil {
		return
	}

	names := map[string]bool{}
	for _, d := range append(append(append([]string{}, basic.BlockDomains...), basic.DirectDomains...), basic.IPv4Domains...) {
		names[d] = true
	}
	if rules, err := s.ListRules(ctx); err == nil {
		for _, r := range rules {
			if r.Enabled {
				for _, d := range splitList(r.Domains) {
					names[d] = true
				}
			}
		}
	}
	// Hop endpoints given as names, so the panel-outbound chain can keep
	// them direct.
	var obs []model.Outbound
	if err := s.db.WithContext(ctx).Find(&obs).Error; err == nil {
		for _, o := range obs {
			host := strings.TrimSpace(o.Address)
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if host != "" {
				if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err != nil {
					names[host] = true
				}
			}
		}
	}
	if len(names) == 0 {
		s.mu.Lock()
		s.resolved = map[string][]netip.Prefix{}
		s.lastResolve = time.Now()
		s.mu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got = map[string][]netip.Prefix{}
		sem = make(chan struct{}, 8)
	)
	resolver := net.Resolver{}
	s.mu.RLock()
	family := "ip"
	switch s.domainStrategy {
	case "UseIPv4":
		family = "ip4"
	case "UseIPv6":
		family = "ip6"
	}
	s.mu.RUnlock()

	for name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ips, err := resolver.LookupNetIP(ctx, family, name)
			if err != nil || len(ips) == 0 {
				return
			}
			out := make([]netip.Prefix, 0, len(ips))
			for _, ip := range ips {
				ip = ip.Unmap()
				out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
			}
			mu.Lock()
			got[name] = out
			mu.Unlock()
		}(name)
	}
	wg.Wait()

	s.mu.Lock()
	// A name that failed keeps whatever it had. A resolver hiccup must not
	// briefly unblock something an operator forbade.
	for name := range names {
		if addrs, ok := got[name]; ok {
			s.resolved[name] = addrs
		}
	}
	// Names no longer mentioned are dropped so the cache cannot grow forever.
	for name := range s.resolved {
		if !names[name] {
			delete(s.resolved, name)
		}
	}
	s.lastResolve = time.Now()
	s.mu.Unlock()
}

// ResolverStatus is what the page shows about name resolution.
type ResolverStatus struct {
	Names     int       `json:"names"`
	Addresses int       `json:"addresses"`
	LastRun   time.Time `json:"lastRun"`
}

// ResolverStatus reports on the cache.
func (s *Routing) ResolverStatus() ResolverStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := 0
	for _, v := range s.resolved {
		n += len(v)
	}
	return ResolverStatus{Names: len(s.resolved), Addresses: n, LastRun: s.lastResolve}
}

// ── the route tester ─────────────────────────────────────────────────────────

// RouteTest is a question about where a connection would go.
type RouteTest struct {
	Target   string `json:"target"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	ClientID uint   `json:"clientId"`
	// InterfaceID is the inbound the connection arrives on, when it matters.
	InterfaceID uint `json:"interfaceId"`
	// SourceIP is the customer's tunnel address, when known.
	SourceIP string `json:"sourceIp"`
}

// RouteAnswer is what the router would decide, and why.
type RouteAnswer struct {
	Outbound string `json:"outbound"`
	// Reason names the rule or setting that decided it, so an operator can go
	// and change the right thing rather than guessing.
	Reason  string   `json:"reason"`
	Blocked bool     `json:"blocked"`
	Steps   []string `json:"steps"`
	// RuleID is the rule that decided it, when a rule did. Carried as an id
	// rather than left to the page to find by name: two rules may share a name,
	// and the one the operator needs to look at is a particular row.
	RuleID uint `json:"ruleId,omitempty"`
	// Balancer is set when the outbound is one, with the member it would
	// pick for this flow -- the fastest for leastPing, else one of them.
	Balancer string `json:"balancer,omitempty"`
}

// TestRoute answers where a connection would be sent, without sending one.
//
// It walks the same order the kernel does, and reports the first thing that
// matches. Being able to ask this is the difference between a routing table an
// operator trusts and one they poke at.
func (s *Routing) TestRoute(ctx context.Context, in RouteTest) (*RouteAnswer, error) {
	target := strings.TrimSpace(in.Target)
	if target == "" {
		return nil, invalidField("target", "give a domain or address to test")
	}

	basic, err := s.Basic(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the target the same way the policy does, so the answer is about
	// the addresses that would really be matched.
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(target); err == nil {
		addrs = []netip.Addr{a.Unmap()}
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var lookupResolver net.Resolver
		ips, lerr := lookupResolver.LookupNetIP(lookupCtx, "ip", target)
		if lerr != nil {
			return nil, invalidField("target", "%q does not resolve, so there is nothing to test", target)
		}
		for _, ip := range ips {
			addrs = append(addrs, ip.Unmap())
		}
	}

	ans := &RouteAnswer{}
	note := func(f string, a ...any) { ans.Steps = append(ans.Steps, fmt.Sprintf(f, a...)) }
	note("%s resolves to %s", target, joinAddrs(addrs))

	// 1. Blocked destinations.
	blocked, _ := routing.ParseTargets(basic.BlockIPs)
	s.mu.RLock()
	for _, d := range basic.BlockDomains {
		blocked = append(blocked, s.resolved[d]...)
	}
	s.mu.RUnlock()
	if p, hit := firstMatch(addrs, blocked); hit {
		ans.Outbound, ans.Blocked = TagBlocked, true
		ans.Reason = fmt.Sprintf("the blocked list covers %s", p)
		note("dropped by the blocked list")
		return ans, nil
	}

	// 2. Blocked ports and BitTorrent.
	if in.Port > 0 {
		if ports, _ := routing.ParsePorts(basic.BlockPorts); portMatches(ports, in.Port) {
			ans.Outbound, ans.Blocked = TagBlocked, true
			ans.Reason = fmt.Sprintf("port %d is on the blocked list", in.Port)
			return ans, nil
		}
		if basic.BlockBitTorrent && isBitTorrentPort(in.Port) {
			ans.Outbound, ans.Blocked = TagBlocked, true
			ans.Reason = fmt.Sprintf("port %d is blocked by the BitTorrent switch", in.Port)
			return ans, nil
		}
	}

	// 3. Pinned destinations.
	direct, _ := routing.ParseTargets(basic.DirectIPs)
	s.mu.RLock()
	for _, d := range basic.DirectDomains {
		direct = append(direct, s.resolved[d]...)
	}
	s.mu.RUnlock()
	if p, hit := firstMatch(addrs, direct); hit {
		ans.Outbound = TagDirect
		ans.Reason = fmt.Sprintf("pinned direct by %s", p)
		return ans, nil
	}

	// 4. Rules, in order.
	rules, err := s.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if s.ruleMatches(ctx, r, addrs, in) {
			ans.Outbound = r.OutboundTag
			ans.Reason = fmt.Sprintf("rule %q", r.Name)
			ans.Blocked = r.OutboundTag == TagBlocked
			ans.RuleID = r.ID
			note("first matching rule: %s", r.Name)
			s.resolveBalancer(ctx, ans)
			return ans, nil
		}
	}

	// 5. Whatever carries the rest.
	ans.Outbound = basic.DefaultOutbound
	ans.Reason = "no rule matched, so the default outbound carries it"
	s.resolveBalancer(ctx, ans)
	return ans, nil
}

// resolveBalancer, when the answer names a balancer, says which member it
// would send this flow to.
func (s *Routing) resolveBalancer(ctx context.Context, ans *RouteAnswer) {
	var b model.Balancer
	if err := s.db.WithContext(ctx).Where("tag = ?", ans.Outbound).Limit(1).Find(&b).Error; err != nil || b.ID == 0 {
		return
	}
	var outbounds []model.Outbound
	_ = s.db.WithContext(ctx).Find(&outbounds).Error
	byOutbound := map[string]model.Outbound{}
	byDevice := map[string]string{}
	for _, o := range outbounds {
		byOutbound[o.Tag] = o
		if d := hopDevice(o); d != "" {
			byDevice[d] = o.Tag
		}
	}
	devs := s.balancerDevices(b, byOutbound)
	ans.Balancer = b.Tag
	if len(devs) == 0 {
		ans.Outbound = ""
		ans.Reason += "; the balancer has no member that is up"
		return
	}
	ans.Outbound = byDevice[devs[0]]
	if len(devs) > 1 {
		ans.Steps = append(ans.Steps, fmt.Sprintf("balancer %s spreads flows over %s", b.Tag, strings.Join(devs, ", ")))
	}
}

func (s *Routing) ruleMatches(
	ctx context.Context, r model.RoutingRule, addrs []netip.Addr, in RouteTest,
) bool {
	if r.DestIPs != "" || r.Domains != "" {
		var ps []netip.Prefix
		if r.DestIPs != "" {
			ps, _ = routing.ParseTargets(splitList(r.DestIPs))
		}
		s.mu.RLock()
		for _, d := range splitList(r.Domains) {
			ps = append(ps, s.resolved[d]...)
		}
		s.mu.RUnlock()
		if _, hit := firstMatch(addrs, ps); !hit {
			return false
		}
	}
	if r.Ports != "" {
		ports, _ := routing.ParsePorts(splitList(r.Ports))
		if in.Port <= 0 || !portMatches(ports, in.Port) {
			return false
		}
	}
	if r.SourcePorts != "" {
		// The tester has no source port to offer; a rule that needs one
		// cannot be shown to match here.
		return false
	}
	if r.Network != "" && !strings.EqualFold(r.Network, in.Protocol) {
		return false
	}
	if r.SourceIPs != "" {
		src, err := netip.ParseAddr(strings.TrimSpace(in.SourceIP))
		if err != nil {
			return false
		}
		ps, _ := routing.ParseTargets(splitList(r.SourceIPs))
		if _, hit := firstMatch([]netip.Addr{src.Unmap()}, ps); !hit {
			return false
		}
	}
	if r.Clients != "" || r.Groups != "" {
		if in.ClientID == 0 {
			return false
		}
		ok := false
		for _, c := range splitList(r.Clients) {
			if id, err := strconv.ParseUint(c, 10, 64); err == nil && uint(id) == in.ClientID {
				ok = true
			}
		}
		if !ok && r.Groups != "" {
			var c model.Client
			if err := s.db.WithContext(ctx).Select("\"group\"").First(&c, in.ClientID).Error; err == nil {
				for _, g := range splitList(r.Groups) {
					if c.Group != "" && c.Group == g {
						ok = true
					}
				}
			}
		}
		if !ok {
			return false
		}
	}
	if r.Interfaces != "" {
		if in.InterfaceID == 0 {
			return false
		}
		ok := false
		for _, i := range splitList(r.Interfaces) {
			if id, err := strconv.ParseUint(i, 10, 64); err == nil && uint(id) == in.InterfaceID {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// ── small helpers ────────────────────────────────────────────────────────────

func firstMatch(addrs []netip.Addr, prefixes []netip.Prefix) (netip.Prefix, bool) {
	for _, p := range prefixes {
		for _, a := range addrs {
			if p.Contains(a) {
				return p, true
			}
		}
	}
	return netip.Prefix{}, false
}

func portMatches(ranges []routing.PortRange, port int) bool {
	for _, r := range ranges {
		if port >= int(r.From) && port <= int(r.To) {
			return true
		}
	}
	return false
}

func isBitTorrentPort(port int) bool {
	return (port >= 6881 && port <= 6889) || port == 6969 || port == 51413 || port == 1337
}

func joinAddrs(addrs []netip.Addr) string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// splitList reads a stored or submitted list. Commas and newlines both
// separate, because an operator pasting a column of addresses should not have
// to reformat it first.
func splitList(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(f))
	seen := map[string]bool{}
	for _, v := range f {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func joinList(v []string) string {
	clean := make([]string, 0, len(v))
	seen := map[string]bool{}
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			clean = append(clean, s)
		}
	}
	return strings.Join(clean, ",")
}

// checkDomain rejects what is plainly not a name, without trying to be a full
// grammar. The cost of a wrong reject here is an operator who cannot save a
// legitimate entry, which is worse than a bad entry that simply never resolves.
func checkDomain(d string) error {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil
	}
	if len(d) > 253 {
		return invalidField("blockDomains", "%q is too long to be a domain name", d)
	}
	if strings.ContainsAny(d, " /\\:@") {
		return invalidField("blockDomains",
			"%q does not look like a domain name; enter just the name, as in example.com", d)
	}
	if !strings.Contains(d, ".") {
		return invalidField("blockDomains", "%q has no dot in it, so it is not a domain name", d)
	}
	return nil
}

// fieldWrap attaches a field name to an error raised deeper down, so the form
// can put the message next to the input that caused it.
func fieldWrap(field string, err error) error {
	return &FieldError{Field: field, Err: err}
}
