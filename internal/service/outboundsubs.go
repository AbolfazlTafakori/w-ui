package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// OutboundSubs keeps the outbound subscriptions: URLs that list outbounds,
// fetched on an interval, each replacing what it produced the time before.
//
// The list a URL answers with can be this panel's own export (a JSON array of
// outbounds), or a line-per-link list of proxies -- socks5://user:pass@host:port#tag
// and http:// the same -- optionally base64-encoded the way subscription
// services usually wrap theirs.
type OutboundSubs struct {
	db        *gorm.DB
	outbounds *Outbounds
	log       *slog.Logger
	http      *http.Client
}

// NewOutboundSubs builds the service.
func NewOutboundSubs(db *gorm.DB, outbounds *Outbounds, log *slog.Logger) *OutboundSubs {
	return &OutboundSubs{
		db:        db,
		outbounds: outbounds,
		log:       log,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// A redirect could send a public URL to a private one after the
			// check; each hop is checked again.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
}

// OutboundSubInput is what the form sends.
type OutboundSubInput struct {
	Remark       string `json:"remark"`
	URL          string `json:"url"`
	TagPrefix    string `json:"tagPrefix"`
	IntervalMin  int    `json:"intervalMin"`
	Enabled      *bool  `json:"enabled"`
	AllowPrivate bool   `json:"allowPrivate"`
	Prepend      bool   `json:"prepend"`
}

const (
	subDefaultIntervalMin = 10
	subMaxBody            = 2 << 20
)

func (s *OutboundSubs) List(ctx context.Context) ([]model.OutboundSub, error) {
	var rows []model.OutboundSub
	if err := s.db.WithContext(ctx).Order("position, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: list outbound subscriptions: %w", err)
	}
	return rows, nil
}

func (s *OutboundSubs) byID(ctx context.Context, id uint) (*model.OutboundSub, error) {
	var sub model.OutboundSub
	if err := s.db.WithContext(ctx).First(&sub, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no subscription %d", ErrNotFound, id)
	}
	return &sub, nil
}

func (s *OutboundSubs) validate(in *OutboundSubInput) error {
	in.URL = strings.TrimSpace(in.URL)
	in.Remark = strings.TrimSpace(in.Remark)
	in.TagPrefix = strings.TrimSpace(in.TagPrefix)
	if in.URL == "" {
		return invalidField("url", "a subscription needs a URL to fetch")
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidField("url", "that is not an http or https URL")
	}
	if !in.AllowPrivate {
		if err := refusePrivateHost(u.Hostname()); err != nil {
			return invalidField("url", "%v", err)
		}
	}
	for _, r := range in.TagPrefix {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return invalidField("tagPrefix", "a tag prefix can only contain letters, digits, - and _")
		}
	}
	if in.IntervalMin <= 0 {
		in.IntervalMin = subDefaultIntervalMin
	}
	if in.IntervalMin > 7*24*60 {
		return invalidField("intervalMin", "the longest interval is a week")
	}
	return nil
}

// refusePrivateHost keeps a subscription from pointing the panel at its own
// network. Names are resolved so a public name for a private address is
// caught too.
func refusePrivateHost(host string) error {
	addrs := []netip.Addr{}
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = append(addrs, a)
	} else {
		ips, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("the name %q does not resolve", host)
		}
		for _, ip := range ips {
			if a, ok := netip.AddrFromSlice(ip); ok {
				addrs = append(addrs, a.Unmap())
			}
		}
	}
	for _, a := range addrs {
		if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() ||
			a.IsLinkLocalMulticast() || a.IsMulticast() {
			return fmt.Errorf("%q is a private address; turn on \"Allow private address\" to use it", host)
		}
	}
	return nil
}

func (s *OutboundSubs) Create(ctx context.Context, in OutboundSubInput) (*model.OutboundSub, error) {
	if err := s.validate(&in); err != nil {
		return nil, err
	}
	var last model.OutboundSub
	s.db.WithContext(ctx).Order("position DESC").Limit(1).Find(&last)
	sub := model.OutboundSub{
		Remark:       in.Remark,
		URL:          in.URL,
		TagPrefix:    in.TagPrefix,
		IntervalMin:  in.IntervalMin,
		Enabled:      in.Enabled == nil || *in.Enabled,
		AllowPrivate: in.AllowPrivate,
		Prepend:      in.Prepend,
		Position:     last.Position + 1,
	}
	if err := s.db.WithContext(ctx).Create(&sub).Error; err != nil {
		return nil, fmt.Errorf("service: create subscription: %w", err)
	}
	// Fetched straight away, so the operator sees what it produced rather
	// than waiting an interval to learn the URL was wrong.
	if sub.Enabled {
		s.Refresh(ctx, sub.ID)
		return s.byID(ctx, sub.ID)
	}
	return &sub, nil
}

func (s *OutboundSubs) Update(ctx context.Context, id uint, in OutboundSubInput) (*model.OutboundSub, error) {
	sub, err := s.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.validate(&in); err != nil {
		return nil, err
	}
	updates := map[string]any{
		"remark":        in.Remark,
		"url":           in.URL,
		"tag_prefix":    in.TagPrefix,
		"interval_min":  in.IntervalMin,
		"allow_private": in.AllowPrivate,
		"prepend":       in.Prepend,
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if err := s.db.WithContext(ctx).Model(sub).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("service: update subscription: %w", err)
	}
	return s.byID(ctx, id)
}

// Delete removes a subscription and the outbounds it produced. One that a
// routing rule still points at is kept, detached, and named in the error.
func (s *OutboundSubs) Delete(ctx context.Context, id uint) error {
	sub, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	kept, err := s.removeProduced(ctx, sub.ID, nil)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(sub).Error; err != nil {
		return fmt.Errorf("service: delete subscription: %w", err)
	}
	if len(kept) > 0 {
		return fmt.Errorf("%w: subscription removed, but %s still carry routing rules and were kept",
			ErrInvalid, strings.Join(kept, ", "))
	}
	return nil
}

// Reorder sets the order subscriptions are listed in.
func (s *OutboundSubs) Reorder(ctx context.Context, ids []uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.OutboundSub{}).Where("id = ?", id).Update("position", i).Error; err != nil {
				return fmt.Errorf("service: reorder subscriptions: %w", err)
			}
		}
		return nil
	})
}

// removeProduced deletes the outbounds a subscription made, except those in
// keep (by tag). Ones that routing rules still point at are detached from the
// subscription and their tags returned.
func (s *OutboundSubs) removeProduced(ctx context.Context, subID uint, keep map[string]bool) ([]string, error) {
	rows, err := s.outbounds.BySub(ctx, subID)
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, ob := range rows {
		if keep[ob.Tag] {
			continue
		}
		if err := s.outbounds.Delete(ctx, ob.ID); err != nil {
			if errors.Is(err, ErrInvalid) {
				_ = s.outbounds.SetOrigin(ctx, ob.ID, "", "", 0)
				kept = append(kept, ob.Tag)
				continue
			}
			return nil, err
		}
	}
	return kept, nil
}

// Preview fetches a URL and says what it would produce, without saving.
func (s *OutboundSubs) Preview(ctx context.Context, in OutboundSubInput) ([]OutboundInput, error) {
	if err := s.validate(&in); err != nil {
		return nil, err
	}
	items, err := s.fetch(ctx, in.URL, in.AllowPrivate)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Tag = in.TagPrefix + items[i].Tag
	}
	return items, nil
}

// Refresh fetches one subscription now and replaces its outbounds. The
// outcome is written on the subscription; the error is also returned.
func (s *OutboundSubs) Refresh(ctx context.Context, id uint) error {
	sub, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	items, ferr := s.fetch(ctx, sub.URL, sub.AllowPrivate)
	now := time.Now().UTC()
	if ferr != nil {
		s.db.WithContext(ctx).Model(sub).Updates(map[string]any{
			"last_fetch_at": now, "last_error": ferr.Error(),
		})
		return ferr
	}

	// Tags that already exist from this subscription are updated in place so
	// their ids -- and the routing marks and rules hanging off them -- stay.
	existing, err := s.outbounds.BySub(ctx, sub.ID)
	if err != nil {
		return err
	}
	byTag := map[string]model.Outbound{}
	for _, ob := range existing {
		byTag[ob.Tag] = ob
	}
	keep := map[string]bool{}
	var ids []uint
	var problems []string
	for _, it := range items {
		it.Tag = sub.TagPrefix + it.Tag
		if len(it.Tag) > 64 {
			it.Tag = it.Tag[:64]
		}
		keep[it.Tag] = true
		if ob, ok := byTag[it.Tag]; ok {
			if _, err := s.outbounds.Update(ctx, ob.ID, it); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", it.Tag, err))
				continue
			}
			ids = append(ids, ob.ID)
			continue
		}
		ob, err := s.outbounds.Create(ctx, it)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", it.Tag, err))
			continue
		}
		if err := s.outbounds.SetOrigin(ctx, ob.ID, "", "", sub.ID); err != nil {
			return err
		}
		ids = append(ids, ob.ID)
	}
	if _, err := s.removeProduced(ctx, sub.ID, keep); err != nil {
		return err
	}
	if sub.Prepend {
		if err := s.prepend(ctx, ids); err != nil {
			return err
		}
	}

	lastErr := ""
	if len(problems) > 0 {
		lastErr = strings.Join(problems, "; ")
		if len(lastErr) > 500 {
			lastErr = lastErr[:500]
		}
	}
	s.db.WithContext(ctx).Model(sub).Updates(map[string]any{
		"last_fetch_at": now, "last_error": lastErr, "count": len(ids),
	})
	s.log.Info("outbound subscription refreshed", "id", sub.ID, "outbounds", len(ids), "problems", len(problems))
	if lastErr != "" {
		return fmt.Errorf("%w: %s", ErrInvalid, lastErr)
	}
	return nil
}

// prepend moves the given outbounds to just after the built-ins.
func (s *OutboundSubs) prepend(ctx context.Context, ids []uint) error {
	all, err := s.outbounds.List(ctx)
	if err != nil {
		return err
	}
	set := map[uint]bool{}
	for _, id := range ids {
		set[id] = true
	}
	var order []uint
	for _, ob := range all {
		if ob.Builtin {
			order = append(order, ob.ID)
		}
	}
	order = append(order, ids...)
	for _, ob := range all {
		if !ob.Builtin && !set[ob.ID] {
			order = append(order, ob.ID)
		}
	}
	return s.outbounds.Reorder(ctx, order)
}

// RefreshAll fetches every enabled subscription now.
func (s *OutboundSubs) RefreshAll(ctx context.Context) error {
	subs, err := s.List(ctx)
	if err != nil {
		return err
	}
	var errs []string
	for _, sub := range subs {
		if !sub.Enabled {
			continue
		}
		if err := s.Refresh(ctx, sub.ID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sub.Remark, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	return nil
}

// Run is the background loop: once a minute, each enabled subscription whose
// interval has passed is fetched.
func (s *OutboundSubs) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		subs, err := s.List(ctx)
		if err != nil {
			continue
		}
		now := time.Now()
		for _, sub := range subs {
			if !sub.Enabled {
				continue
			}
			if sub.LastFetchAt != nil && now.Sub(*sub.LastFetchAt) < time.Duration(sub.IntervalMin)*time.Minute {
				continue
			}
			if err := s.Refresh(ctx, sub.ID); err != nil {
				s.log.Warn("outbound subscription fetch failed", "id", sub.ID, "err", err)
			}
		}
	}
}

// fetch downloads and parses one subscription.
func (s *OutboundSubs) fetch(ctx context.Context, rawURL string, allowPrivate bool) ([]OutboundInput, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "w-ui")
	client := *s.http
	if !allowPrivate {
		inner := client.CheckRedirect
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if err := inner(req, via); err != nil {
				return err
			}
			return refusePrivateHost(req.URL.Hostname())
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, friendlyDialError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the URL answered %s", ErrInvalid, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, subMaxBody))
	if err != nil {
		return nil, err
	}
	items, err := parseSubscription(body)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: no outbounds found at this URL", ErrInvalid)
	}
	return items, nil
}

// parseSubscription reads a JSON array of outbounds, or a list of links,
// plain or base64.
func parseSubscription(body []byte) ([]OutboundInput, error) {
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
		return parseSubscriptionJSON(text)
	}
	if !strings.Contains(text, "://") {
		// Base64, with or without padding, as subscription services emit.
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if dec, err := enc.DecodeString(text); err == nil {
				text = strings.TrimSpace(string(dec))
				break
			}
		}
	}
	var out []OutboundInput
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Every link kind the form takes, the subscription takes: a list of
		// vless or vmess links becomes real exits, not skipped lines.
		in, err := parseShareLink(line)
		if err != nil {
			continue
		}
		if in.Tag == "" {
			in.Tag = fmt.Sprintf("%s-%d", in.Kind, i+1)
		}
		in.Tag = cleanTag(in.Tag)
		out = append(out, *in)
	}
	return out, nil
}

func parseSubscriptionJSON(text string) ([]OutboundInput, error) {
	var arr []OutboundInput
	if err := json.Unmarshal([]byte(text), &arr); err == nil {
		return arr, nil
	}
	var obj struct {
		Outbounds []OutboundInput `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(text), &obj); err != nil || obj.Outbounds == nil {
		return nil, fmt.Errorf("%w: expected a JSON array of outbounds", ErrInvalid)
	}
	return obj.Outbounds, nil
}

// cleanTag turns anything a link's remark may carry into a tag the panel
// accepts: letters, digits, - and _.
func cleanTag(tag string) string {
	var b strings.Builder
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}
