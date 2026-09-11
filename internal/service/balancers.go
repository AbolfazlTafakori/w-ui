package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/routing"
)

// Balancers spread traffic over several outbounds under one tag.
//
// A balancer is a routing table with several next hops: the kernel hashes
// each flow onto one of them, so a connection stays on the exit it started
// on and the load spreads evenly -- what 3x-ui's "random" strategy does.
// "leastPing" instead points the table at whichever member answered the
// last check fastest, re-evaluated on every tick.
type Balancers struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewBalancers builds the service.
func NewBalancers(db *gorm.DB, log *slog.Logger) *Balancers {
	return &Balancers{db: db, log: log}
}

// BalancerInput is what the form sends.
type BalancerInput struct {
	Tag      string   `json:"tag"`
	Enabled  *bool    `json:"enabled"`
	Strategy string   `json:"strategy"`
	Members  []string `json:"members"`
	Note     string   `json:"note"`
}

// BalancerView is a balancer with its members split out.
type BalancerView struct {
	model.Balancer
	MemberList []string `json:"memberList"`
}

func view(b model.Balancer) BalancerView {
	return BalancerView{Balancer: b, MemberList: splitList(b.Members)}
}

func (s *Balancers) List(ctx context.Context) ([]BalancerView, error) {
	var rows []model.Balancer
	if err := s.db.WithContext(ctx).Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: list balancers: %w", err)
	}
	out := make([]BalancerView, 0, len(rows))
	for _, b := range rows {
		out = append(out, view(b))
	}
	return out, nil
}

func (s *Balancers) validate(ctx context.Context, in *BalancerInput, selfID uint) error {
	in.Tag = strings.TrimSpace(in.Tag)
	in.Strategy = strings.TrimSpace(in.Strategy)
	if in.Tag == "" {
		return invalidField("tag", "a balancer needs a tag; rules refer to it by that name")
	}
	for _, r := range in.Tag {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return invalidField("tag", "a tag can only contain letters, digits, - and _ (found %q)", string(r))
		}
	}
	if in.Strategy == "" {
		in.Strategy = model.BalancerRandom
	}
	if in.Strategy != model.BalancerRandom && in.Strategy != model.BalancerLeastPing {
		return invalidField("strategy", "%q is not a strategy; use random or leastPing", in.Strategy)
	}

	// The tag is one space with outbounds: a rule pointing at "de" must
	// mean one thing.
	var n int64
	if err := s.db.WithContext(ctx).Model(&model.Outbound{}).Where("LOWER(tag) = LOWER(?)", in.Tag).Count(&n).Error; err != nil {
		return fmt.Errorf("service: check balancer tag: %w", err)
	}
	if n > 0 {
		return invalidField("tag", "an outbound is already called %q", in.Tag)
	}
	q := s.db.WithContext(ctx).Model(&model.Balancer{}).Where("LOWER(tag) = LOWER(?)", in.Tag)
	if selfID != 0 {
		q = q.Where("id != ?", selfID)
	}
	if err := q.Count(&n).Error; err != nil {
		return fmt.Errorf("service: check balancer tag: %w", err)
	}
	if n > 0 {
		return invalidField("tag", "a balancer called %q already exists", in.Tag)
	}

	seen := map[string]bool{}
	var members []string
	for _, m := range in.Members {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		var ob model.Outbound
		if err := s.db.WithContext(ctx).Where("tag = ?", m).Limit(1).Find(&ob).Error; err != nil {
			return fmt.Errorf("service: check member: %w", err)
		}
		if ob.ID == 0 {
			return invalidField("members", "there is no outbound called %q", m)
		}
		if !ob.Kind.NeedsHop() {
			return invalidField("members", "%q has no device to balance over; a balancer's members are hops", m)
		}
		seen[m] = true
		members = append(members, m)
	}
	if len(members) == 0 {
		return invalidField("members", "a balancer needs at least one outbound to send traffic to")
	}
	in.Members = members
	return nil
}

func (s *Balancers) Create(ctx context.Context, in BalancerInput) (*BalancerView, error) {
	if err := s.validate(ctx, &in, 0); err != nil {
		return nil, err
	}
	b := model.Balancer{
		Tag:      in.Tag,
		Enabled:  in.Enabled == nil || *in.Enabled,
		Strategy: in.Strategy,
		Members:  joinList(in.Members),
		Note:     strings.TrimSpace(in.Note),
	}
	if err := s.db.WithContext(ctx).Create(&b).Error; err != nil {
		return nil, fmt.Errorf("service: create balancer: %w", err)
	}
	mark, _, err := routing.AllocateMark(balancerMarkID(b.ID))
	if err != nil {
		s.db.WithContext(ctx).Delete(&b)
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&b).Update("mark", mark).Error; err != nil {
		return nil, fmt.Errorf("service: assign balancer mark: %w", err)
	}
	b.Mark = mark
	s.log.Info("balancer created", "tag", b.Tag, "members", len(in.Members))
	v := view(b)
	return &v, nil
}

func (s *Balancers) Update(ctx context.Context, id uint, in BalancerInput) (*BalancerView, error) {
	var b model.Balancer
	if err := s.db.WithContext(ctx).First(&b, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no balancer %d", ErrNotFound, id)
	}
	if err := s.validate(ctx, &in, id); err != nil {
		return nil, err
	}
	updates := map[string]any{
		"tag": in.Tag, "strategy": in.Strategy, "members": joinList(in.Members),
		"note": strings.TrimSpace(in.Note), "updated_at": time.Now().UTC(),
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	oldTag := b.Tag
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&b).Updates(updates).Error; err != nil {
			return err
		}
		if in.Tag != oldTag {
			// Rules follow the rename, as they do for an outbound.
			return tx.Model(&model.RoutingRule{}).Where("outbound_tag = ?", oldTag).
				Update("outbound_tag", in.Tag).Error
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("service: update balancer: %w", err)
	}
	if err := s.db.WithContext(ctx).First(&b, id).Error; err != nil {
		return nil, err
	}
	v := view(b)
	return &v, nil
}

// RunChecks keeps leastPing balancers informed: while one exists, every
// member outbound is measured once a minute, so "fastest" means fastest
// now and not whenever somebody last pressed Test all.
func (s *Balancers) RunChecks(ctx context.Context, outbounds *Outbounds) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var n int64
		if err := s.db.WithContext(ctx).Model(&model.Balancer{}).
			Where("enabled = ? AND strategy = ?", true, model.BalancerLeastPing).Count(&n).Error; err != nil || n == 0 {
			continue
		}
		if _, err := outbounds.CheckAll(ctx, ModeTCP); err != nil {
			s.log.Warn("balancer check failed", "err", err)
		}
	}
}

func (s *Balancers) Delete(ctx context.Context, id uint) error {
	var b model.Balancer
	if err := s.db.WithContext(ctx).First(&b, id).Error; err != nil {
		return fmt.Errorf("%w: no balancer %d", ErrNotFound, id)
	}
	var rules int64
	if err := s.db.WithContext(ctx).Model(&model.RoutingRule{}).Where("outbound_tag = ?", b.Tag).Count(&rules).Error; err != nil {
		return fmt.Errorf("service: count rules: %w", err)
	}
	if rules > 0 {
		return fmt.Errorf("%w: %d routing rule(s) still send traffic to %q; change or remove them first", ErrInvalid, rules, b.Tag)
	}
	if err := s.db.WithContext(ctx).Delete(&b).Error; err != nil {
		return fmt.Errorf("service: delete balancer: %w", err)
	}
	s.log.Info("balancer removed", "tag", b.Tag)
	return nil
}
