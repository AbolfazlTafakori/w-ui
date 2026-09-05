package service

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
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
	if v := strings.TrimSpace(stored[keyDefaultOutbound]); v != "" {
		out.DefaultOutbound = v
	}
	return out, nil
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
	for _, d := range append(append([]string{}, in.BlockDomains...), in.DirectDomains...) {
		if err := checkDomain(d); err != nil {
			return BasicRouting{}, err
		}
	}

	tag := strings.TrimSpace(in.DefaultOutbound)
	if tag == "" {
		tag = TagDirect
	}
	var ob model.Outbound
	if err := s.db.WithContext(ctx).Where("tag = ?", tag).Limit(1).
		Find(&ob).Error; err != nil {
		return BasicRouting{}, fmt.Errorf("service: check default outbound: %w", err)
	}
	if ob.ID == 0 {
		return BasicRouting{}, invalidField("defaultOutbound",
			"there is no outbound called %q", tag)
	}
	if !ob.Enabled {
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
	Name        string               `json:"name"`
	Enabled     *bool                `json:"enabled"`
	Match       model.RouteMatchKind `json:"match"`
	Value       string               `json:"value"`
	OutboundTag string               `json:"outboundTag"`
	Note        string               `json:"note"`
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
		Match:       in.Match,
		Value:       in.Value,
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

	updates := map[string]any{
		"name":         in.Name,
		"match":        in.Match,
		"value":        in.Value,
		"outbound_tag": in.OutboundTag,
		"note":         strings.TrimSpace(in.Note),
		"updated_at":   time.Now().UTC(),
	}
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

func (s *Routing) validateRule(ctx context.Context, in *RuleInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Value = strings.TrimSpace(in.Value)
	in.OutboundTag = strings.TrimSpace(in.OutboundTag)

	if in.Name == "" {
		return invalidField("name", "give the rule a name so the list can be read later")
	}
	if !in.Match.Valid() {
		return invalidField("match", "%q is not something a rule can match on", in.Match)
	}
	if in.Value == "" {
		return invalidField("value", "the rule matches nothing as written")
	}

	switch in.Match {
	case model.MatchIP:
		if _, err := routing.ParseTargets(splitList(in.Value)); err != nil {
			return fieldWrap("value", err)
		}
	case model.MatchPort:
		if _, err := routing.ParsePorts(splitList(in.Value)); err != nil {
			return fieldWrap("value", err)
		}
	case model.MatchProtocol:
		switch strings.ToLower(in.Value) {
		case "tcp", "udp", "icmp":
		default:
			return invalidField("value", "%q is not a protocol the router matches; use tcp, udp or icmp", in.Value)
		}
	case model.MatchDomain:
		for _, d := range splitList(in.Value) {
			if err := checkDomain(d); err != nil {
				return err
			}
		}
	case model.MatchClient:
		if _, err := strconv.ParseUint(in.Value, 10, 64); err != nil {
			return invalidField("value", "this rule matches one client by id, and %q is not one", in.Value)
		}
	case model.MatchGroup:
		// A group is a label carried by the client rather than a row of its
		// own, so the rule stores the name. Checked against what exists so a
		// typo is caught here rather than becoming a rule that never fires.
		var n int64
		if err := s.db.WithContext(ctx).Model(&model.Group{}).
			Where("name = ?", in.Value).Count(&n).Error; err != nil {
			return fmt.Errorf("service: check group: %w", err)
		}
		if n == 0 {
			return invalidField("value", "there is no group called %q", in.Value)
		}
	}

	var ob model.Outbound
	if err := s.db.WithContext(ctx).Where("tag = ?", in.OutboundTag).Limit(1).
		Find(&ob).Error; err != nil {
		return fmt.Errorf("service: check rule outbound: %w", err)
	}
	if ob.ID == 0 {
		return invalidField("outboundTag", "there is no outbound called %q", in.OutboundTag)
	}
	return nil
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
	byTag := make(map[string]model.Outbound, len(outbounds))
	for _, o := range outbounds {
		byTag[o.Tag] = o
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

	if def, ok := byTag[basic.DefaultOutbound]; ok && def.Enabled {
		p.DefaultMark = def.Mark
	}

	rules, err := s.ListRules(ctx)
	if err != nil {
		return p, err
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		ob, ok := byTag[r.OutboundTag]
		if !ok || !ob.Enabled {
			// A rule pointing at an outbound that is gone or switched off is
			// skipped rather than applied as a drop. Silently discarding a
			// customer's traffic because an operator disabled a hop is the
			// worst of the available behaviours.
			continue
		}
		mr := routing.MarkRule{Mark: ob.Mark, Drop: ob.Kind == model.OutboundBlock}
		if !s.fillMatch(ctx, &mr, r) {
			continue
		}
		p.Rules = append(p.Rules, mr)
	}

	return p, nil
}

// fillMatch turns a stored rule into a kernel match. It reports whether the
// rule ended up matching anything at all.
func (s *Routing) fillMatch(ctx context.Context, mr *routing.MarkRule, r model.RoutingRule) bool {
	switch r.Match {
	case model.MatchIP:
		mr.Addrs, _ = routing.ParseTargets(splitList(r.Value))
		return len(mr.Addrs) > 0

	case model.MatchDomain:
		s.mu.RLock()
		for _, d := range splitList(r.Value) {
			mr.Addrs = append(mr.Addrs, s.resolved[d]...)
		}
		s.mu.RUnlock()
		return len(mr.Addrs) > 0

	case model.MatchPort:
		mr.Ports, _ = routing.ParsePorts(splitList(r.Value))
		return len(mr.Ports) > 0

	case model.MatchProtocol:
		mr.Protocol = strings.ToLower(r.Value)
		return mr.Protocol != ""

	case model.MatchClient, model.MatchGroup:
		mr.Sources = s.addressesFor(ctx, r)
		return len(mr.Sources) > 0
	}
	return false
}

// addressesFor resolves a client or group rule to the tunnel addresses it
// covers, which is what the kernel can actually match on.
func (s *Routing) addressesFor(ctx context.Context, r model.RoutingRule) []netip.Addr {
	q := s.db.WithContext(ctx).Model(&model.Account{}).
		Joins("JOIN clients ON clients.id = accounts.client_id")

	if r.Match == model.MatchClient {
		id, err := strconv.ParseUint(r.Value, 10, 64)
		if err != nil {
			return nil
		}
		q = q.Where("accounts.client_id = ?", id)
	} else {
		q = q.Where("clients.\"group\" = ?", r.Value)
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
	if o.Kind != model.OutboundWireGuard {
		// A proxy is dialled in userspace and has no interface of its own.
		return ""
	}
	return fmt.Sprintf("wuih%d", o.ID)
}

// HopDevice is the interface name a WireGuard outbound uses.
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
	for _, d := range append(append([]string{}, basic.BlockDomains...), basic.DirectDomains...) {
		names[d] = true
	}
	if rules, err := s.ListRules(ctx); err == nil {
		for _, r := range rules {
			if r.Match == model.MatchDomain && r.Enabled {
				for _, d := range splitList(r.Value) {
					names[d] = true
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

	for name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ips, err := resolver.LookupNetIP(ctx, "ip", name)
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
			return ans, nil
		}
	}

	// 5. Whatever carries the rest.
	ans.Outbound = basic.DefaultOutbound
	ans.Reason = "no rule matched, so the default outbound carries it"
	return ans, nil
}

func (s *Routing) ruleMatches(
	ctx context.Context, r model.RoutingRule, addrs []netip.Addr, in RouteTest,
) bool {
	switch r.Match {
	case model.MatchIP:
		ps, _ := routing.ParseTargets(splitList(r.Value))
		_, hit := firstMatch(addrs, ps)
		return hit
	case model.MatchDomain:
		s.mu.RLock()
		var ps []netip.Prefix
		for _, d := range splitList(r.Value) {
			ps = append(ps, s.resolved[d]...)
		}
		s.mu.RUnlock()
		_, hit := firstMatch(addrs, ps)
		return hit
	case model.MatchPort:
		ports, _ := routing.ParsePorts(splitList(r.Value))
		return in.Port > 0 && portMatches(ports, in.Port)
	case model.MatchProtocol:
		return in.Protocol != "" && strings.EqualFold(r.Value, in.Protocol)
	case model.MatchClient:
		id, err := strconv.ParseUint(r.Value, 10, 64)
		return err == nil && in.ClientID != 0 && uint(id) == in.ClientID
	case model.MatchGroup:
		if in.ClientID == 0 {
			return false
		}
		var c model.Client
		if err := s.db.WithContext(ctx).Select("\"group\"").
			First(&c, in.ClientID).Error; err != nil {
			return false
		}
		return c.Group != "" && c.Group == r.Value
	}
	return false
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
