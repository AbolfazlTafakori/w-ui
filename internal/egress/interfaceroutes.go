package egress

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/listfield"
)

// Which tunnels leave through an outbound or a balancer, chosen on its own
// form.
//
// It is a routing rule underneath -- one that matches nothing but tunnels and
// points at that tag -- so the rules page shows it, the same evaluation order
// applies, and nothing new reaches the kernel. The form is a shorter way to
// write the one rule operators want most: "everyone on this tunnel goes out
// there". A rule with any other criterion is the operator's own and is never
// read or changed from here.

// tunnelOnly reports whether a rule matches by tunnel and nothing else.
func tunnelOnly(r model.RoutingRule) bool {
	return r.Interfaces != "" && r.SourceIPs == "" && r.SourcePorts == "" && r.Network == "" &&
		r.DestIPs == "" && r.Domains == "" && r.Ports == "" && r.Clients == "" && r.Groups == "" &&
		r.Match == "" && r.Value == ""
}

// TunnelsByTarget is, for every outbound and balancer, the tunnels its
// tunnel-only rules send to it.
func (s *Routing) TunnelsByTarget(ctx context.Context) (map[string][]uint, error) {
	var rules []model.RoutingRule
	if err := s.db.WithContext(ctx).Where("enabled = ?", true).Order("position, id").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("service: read routing rules: %w", err)
	}
	out := map[string][]uint{}
	seen := map[uint]bool{}
	for _, r := range rules {
		if !tunnelOnly(r) {
			continue
		}
		for _, v := range listfield.Split(r.Interfaces) {
			id, err := strconv.ParseUint(v, 10, 64)
			// The first rule a tunnel matches is the one that sends it.
			if err != nil || seen[uint(id)] {
				continue
			}
			seen[uint(id)] = true
			out[r.OutboundTag] = append(out[r.OutboundTag], uint(id))
		}
	}
	return out, nil
}

// CheckTunnels says whether every id is a tunnel, before anything is saved.
func (s *Routing) CheckTunnels(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return s.validateRule(ctx, &RuleInput{Name: "tunnels", Interfaces: joinIDs(ids), OutboundTag: TagDirect})
}

// SetTunnels makes exactly these tunnels leave through tag.
//
// A tunnel can leave through one place only, so one taken from another
// outbound's or balancer's tunnel-only rule is taken out of it; a rule left
// with no tunnels is removed. The rule for tag is created at the end of the
// list when there is none: the operator's narrower rules above it -- a
// blocked site, a country sent direct -- still come first.
func (s *Routing) SetTunnels(ctx context.Context, tag string, ids []uint) error {
	if err := s.CheckTunnels(ctx, ids); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[strconv.FormatUint(uint64(id), 10)] = true
	}

	var rules []model.RoutingRule
	if err := s.db.WithContext(ctx).Order("position, id").Find(&rules).Error; err != nil {
		return fmt.Errorf("service: read routing rules: %w", err)
	}
	var own *model.RoutingRule
	changed := false
	for i := range rules {
		r := &rules[i]
		if !tunnelOnly(*r) {
			continue
		}
		if r.OutboundTag == tag {
			if own == nil {
				own = r
				continue
			}
			// A second rule of its own: folded into the first.
			if err := s.setRuleTunnels(ctx, r, nil); err != nil {
				return err
			}
			changed = true
			continue
		}
		var kept []string
		for _, v := range listfield.Split(r.Interfaces) {
			if !want[v] {
				kept = append(kept, v)
			}
		}
		if len(kept) != len(listfield.Split(r.Interfaces)) {
			if err := s.setRuleTunnels(ctx, r, kept); err != nil {
				return err
			}
			changed = true
		}
	}

	list := make([]string, 0, len(want))
	for v := range want {
		list = append(list, v)
	}
	sortNumeric(list)
	switch {
	case own != nil:
		if listfield.Join(list) != own.Interfaces || !own.Enabled {
			if err := s.setRuleTunnels(ctx, own, list); err != nil {
				return err
			}
			changed = true
		}
	case len(list) > 0:
		if _, err := s.CreateRule(ctx, RuleInput{Name: tag + " tunnels", Interfaces: listfield.Join(list), OutboundTag: tag,
			Note: "Set on the " + tag + " form"}); err != nil {
			return err
		}
		return nil // CreateRule has announced the change
	}
	if changed {
		s.namesChanged(ctx)
	}
	return nil
}

// setRuleTunnels writes a tunnel-only rule's tunnels, removing it when none
// are left.
func (s *Routing) setRuleTunnels(ctx context.Context, r *model.RoutingRule, list []string) error {
	db := s.db.WithContext(ctx)
	if len(list) == 0 {
		if err := db.Delete(&model.RoutingRule{}, r.ID).Error; err != nil {
			return fmt.Errorf("service: delete routing rule: %w", err)
		}
		return nil
	}
	if err := db.Model(&model.RoutingRule{}).Where("id = ?", r.ID).
		Updates(map[string]any{"interfaces": listfield.Join(list), "enabled": true}).Error; err != nil {
		return fmt.Errorf("service: update routing rule: %w", err)
	}
	return nil
}

func joinIDs(ids []uint) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.FormatUint(uint64(id), 10))
	}
	return listfield.Join(out)
}

func sortNumeric(v []string) {
	sort.Slice(v, func(i, j int) bool {
		a, _ := strconv.Atoi(v[i])
		b, _ := strconv.Atoi(v[j])
		return a < b
	})
}
