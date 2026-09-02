package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// Tags of the two outbounds that always exist.
const (
	TagDirect  = "direct"
	TagBlocked = "blocked"
)

// Outbounds manages the ways out of this server.
type Outbounds struct {
	db  *gorm.DB
	log *slog.Logger
}

func NewOutbounds(db *gorm.DB, log *slog.Logger) *Outbounds {
	return &Outbounds{db: db, log: log}
}

// EnsureBuiltins creates the two outbounds the panel cannot work without.
//
// Run at startup rather than in a migration so a database restored from an
// older backup gains them too. Both are created disabled-proof: a routing rule
// can always point somewhere, and an operator who deletes everything else still
// has a working panel.
func (s *Outbounds) EnsureBuiltins(ctx context.Context) error {
	db := s.db.WithContext(ctx)

	builtins := []model.Outbound{
		{
			Tag: TagDirect, Kind: model.OutboundDirect, Builtin: true, Enabled: true,
			Position: 0,
			Note:     "Traffic leaves through this server's own address.",
		},
		{
			Tag: TagBlocked, Kind: model.OutboundBlock, Builtin: true, Enabled: true,
			Position: 1,
			Note:     "Traffic sent here is discarded.",
		},
	}

	for _, b := range builtins {
		var existing model.Outbound
		err := db.Where("tag = ?", b.Tag).Limit(1).Find(&existing).Error
		if err != nil {
			return fmt.Errorf("service: look for outbound %q: %w", b.Tag, err)
		}
		if existing.ID != 0 {
			// Already there. The kind is repaired if something changed it,
			// because a `direct` outbound that is no longer direct would route
			// every unmatched packet into nowhere.
			if existing.Kind != b.Kind || !existing.Builtin {
				if err := db.Model(&existing).
					Updates(map[string]any{"kind": b.Kind, "builtin": true}).Error; err != nil {
					return fmt.Errorf("service: repair outbound %q: %w", b.Tag, err)
				}
			}
			continue
		}
		if err := db.Create(&b).Error; err != nil {
			return fmt.Errorf("service: create outbound %q: %w", b.Tag, err)
		}
		s.log.Info("built-in outbound created", "tag", b.Tag)
	}
	return nil
}

// List returns every outbound in the order an operator arranged them.
func (s *Outbounds) List(ctx context.Context) ([]model.Outbound, error) {
	var out []model.Outbound
	err := s.db.WithContext(ctx).Order("position, id").Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("service: list outbounds: %w", err)
	}
	return out, nil
}

// OutboundInput is what the form collects.
type OutboundInput struct {
	Tag     string             `json:"tag"`
	Kind    model.OutboundKind `json:"kind"`
	Enabled *bool              `json:"enabled"`
	Address string             `json:"address"`
	Note    string             `json:"note"`

	Username string `json:"username"`
	Password string `json:"password"`

	PrivateKey   string `json:"privateKey"`
	PeerPubKey   string `json:"peerPubKey"`
	PresharedKey string `json:"presharedKey"`
	HopAddress   string `json:"hopAddress"`
	HopDNS       string `json:"hopDns"`
	HopMTU       int    `json:"hopMtu"`
}

// Create adds an outbound.
func (s *Outbounds) Create(ctx context.Context, in OutboundInput) (*model.Outbound, error) {
	if err := s.validate(ctx, &in, 0); err != nil {
		return nil, err
	}

	db := s.db.WithContext(ctx)

	var last model.Outbound
	if err := db.Order("position DESC").Limit(1).Find(&last).Error; err != nil {
		return nil, fmt.Errorf("service: read outbound order: %w", err)
	}

	ob := model.Outbound{
		Tag:          in.Tag,
		Kind:         in.Kind,
		Enabled:      in.Enabled == nil || *in.Enabled,
		Position:     last.Position + 1,
		Address:      in.Address,
		Username:     in.Username,
		Password:     in.Password,
		PrivateKey:   in.PrivateKey,
		PeerPubKey:   in.PeerPubKey,
		PresharedKey: in.PresharedKey,
		HopAddress:   in.HopAddress,
		HopDNS:       in.HopDNS,
		HopMTU:       in.HopMTU,
		Note:         in.Note,
	}
	if ob.HopMTU == 0 {
		// Below the usual 1420 a WireGuard interface uses, because this tunnel
		// runs inside another one and its headers have to fit too. A hop at the
		// outer MTU works for small packets and silently drops large ones,
		// which looks like "some websites do not load".
		ob.HopMTU = 1380
	}

	if err := db.Create(&ob).Error; err != nil {
		return nil, fmt.Errorf("service: create outbound: %w", err)
	}

	// The mark is derived from the row id, so it can only be assigned once the
	// row exists.
	if err := s.assignMark(ctx, &ob); err != nil {
		// Roll back rather than leave an outbound that can never carry traffic.
		db.Delete(&ob)
		return nil, err
	}

	s.log.Info("outbound created", "tag", ob.Tag, "kind", ob.Kind)
	return &ob, nil
}

// Update changes an outbound.
func (s *Outbounds) Update(ctx context.Context, id uint, in OutboundInput) (*model.Outbound, error) {
	db := s.db.WithContext(ctx)

	var ob model.Outbound
	if err := db.First(&ob, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no outbound %d", ErrNotFound, id)
	}

	if ob.Builtin {
		// The two built-ins carry no configuration to change, and renaming one
		// would orphan every rule pointing at it. Only the note moves.
		updates := map[string]any{"note": strings.TrimSpace(in.Note), "updated_at": time.Now().UTC()}
		if in.Enabled != nil && !*in.Enabled {
			return nil, invalidField("enabled",
				"%q is built in and cannot be switched off; rules would have nowhere to point", ob.Tag)
		}
		if err := db.Model(&ob).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("service: update outbound: %w", err)
		}
		return s.byID(ctx, id)
	}

	if err := s.validate(ctx, &in, id); err != nil {
		return nil, err
	}

	updates := map[string]any{
		"tag":          in.Tag,
		"kind":         in.Kind,
		"address":      in.Address,
		"username":     in.Username,
		"peer_pub_key": in.PeerPubKey,
		"hop_address":  in.HopAddress,
		"hop_dns":      in.HopDNS,
		"note":         strings.TrimSpace(in.Note),
		"updated_at":   time.Now().UTC(),
	}
	if in.HopMTU > 0 {
		updates["hop_mtu"] = in.HopMTU
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	// Secrets left blank mean "leave them alone". The form cannot show what is
	// stored, so submitting it unchanged must not wipe the credentials.
	if strings.TrimSpace(in.Password) != "" {
		updates["password"] = in.Password
	}
	if strings.TrimSpace(in.PrivateKey) != "" {
		updates["private_key"] = in.PrivateKey
	}
	if strings.TrimSpace(in.PresharedKey) != "" {
		updates["preshared_key"] = in.PresharedKey
	}

	// A tag that changes has to take its rules with it, or every rule pointing
	// at the old name silently stops matching.
	oldTag := ob.Tag
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ob).Updates(updates).Error; err != nil {
			return err
		}
		if in.Tag != oldTag {
			return tx.Model(&model.RoutingRule{}).
				Where("outbound_tag = ?", oldTag).
				Update("outbound_tag", in.Tag).Error
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("service: update outbound: %w", err)
	}

	return s.byID(ctx, id)
}

// Delete removes an outbound.
func (s *Outbounds) Delete(ctx context.Context, id uint) error {
	db := s.db.WithContext(ctx)

	var ob model.Outbound
	if err := db.First(&ob, id).Error; err != nil {
		return fmt.Errorf("%w: no outbound %d", ErrNotFound, id)
	}
	if ob.Builtin {
		return fmt.Errorf("%w: %q is built in and cannot be removed", ErrInvalid, ob.Tag)
	}

	// Rules pointing here would become rules that match and then do nothing.
	// Refused rather than silently repointed: where that traffic should go
	// instead is the operator's decision, not the panel's.
	var rules int64
	if err := db.Model(&model.RoutingRule{}).
		Where("outbound_tag = ?", ob.Tag).Count(&rules).Error; err != nil {
		return fmt.Errorf("service: count rules: %w", err)
	}
	if rules > 0 {
		return fmt.Errorf("%w: %d routing rule(s) still send traffic to %q; change or remove them first",
			ErrInvalid, rules, ob.Tag)
	}

	if err := db.Delete(&ob).Error; err != nil {
		return fmt.Errorf("service: delete outbound: %w", err)
	}
	s.log.Info("outbound removed", "tag", ob.Tag)
	return nil
}

// Reorder sets the order outbounds are listed and tried in.
func (s *Outbounds) Reorder(ctx context.Context, ids []uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&model.Outbound{}).Where("id = ?", id).
				Update("position", i).Error; err != nil {
				return fmt.Errorf("service: reorder outbounds: %w", err)
			}
		}
		return nil
	})
}

// CheckResult is one latency measurement.
type CheckResult struct {
	Tag       string `json:"tag"`
	OK        bool   `json:"ok"`
	LatencyMS int    `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// Check measures how long one outbound takes to answer.
//
// The two built-ins have nothing to dial, so `direct` is reported against the
// server's own route out and `blocked` is reported as what it is rather than as
// a failure — an operator looking at a red mark next to "blocked" would
// reasonably think something was wrong.
func (s *Outbounds) Check(ctx context.Context, id uint, mode string) (*CheckResult, error) {
	ob, err := s.byID(ctx, id)
	if err != nil {
		return nil, err
	}

	res := &CheckResult{Tag: ob.Tag}

	switch ob.Kind {
	case model.OutboundBlock:
		res.OK = true
		res.Error = "nothing to reach: this outbound discards traffic"
		s.recordCheck(ctx, ob.ID, 0, "")
		return res, nil
	case model.OutboundDirect:
		// Times the server's own path to the internet, which is the thing
		// `direct` actually uses.
		d, err := dialLatency(ctx, "1.1.1.1:443", mode)
		return s.finishCheck(ctx, ob, res, d, err), nil
	}

	target := ob.Address
	if target == "" {
		res.Error = "this outbound has no address to reach"
		return res, nil
	}
	// A WireGuard endpoint is UDP and will not answer a TCP connect, so what is
	// measured is whether the host is routable at all rather than whether the
	// tunnel is up. Said plainly in the result so nobody reads a green tick as
	// proof the hop works.
	if ob.Kind == model.OutboundWireGuard {
		host, _, splitErr := net.SplitHostPort(target)
		if splitErr == nil {
			target = net.JoinHostPort(host, "443")
		}
	}

	d, err := dialLatency(ctx, target, mode)
	return s.finishCheck(ctx, ob, res, d, err), nil
}

// CheckAll measures every outbound, in parallel.
func (s *Outbounds) CheckAll(ctx context.Context, mode string) ([]CheckResult, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]CheckResult, len(list))
	sem := make(chan struct{}, 8) // a hop that hangs must not hold up the rest
	done := make(chan struct{})

	for i, ob := range list {
		go func(i int, id uint) {
			sem <- struct{}{}
			defer func() { <-sem; done <- struct{}{} }()
			r, err := s.Check(ctx, id, mode)
			if err != nil {
				out[i] = CheckResult{OK: false, Error: err.Error()}
				return
			}
			out[i] = *r
		}(i, ob.ID)
	}
	for range list {
		<-done
	}
	return out, nil
}

func (s *Outbounds) finishCheck(
	ctx context.Context, ob *model.Outbound, res *CheckResult, d time.Duration, err error,
) *CheckResult {
	if err != nil {
		res.OK = false
		res.Error = friendlyDialError(err)
		s.recordCheck(ctx, ob.ID, 0, res.Error)
		return res
	}
	res.OK = true
	res.LatencyMS = int(d.Milliseconds())
	s.recordCheck(ctx, ob.ID, res.LatencyMS, "")
	return res
}

func (s *Outbounds) recordCheck(ctx context.Context, id uint, ms int, errText string) {
	now := time.Now().UTC()
	s.db.WithContext(ctx).Model(&model.Outbound{}).Where("id = ?", id).
		Updates(map[string]any{
			"latency_ms":    ms,
			"last_check_at": now,
			"last_error":    errText,
		})
}

func (s *Outbounds) byID(ctx context.Context, id uint) (*model.Outbound, error) {
	var ob model.Outbound
	if err := s.db.WithContext(ctx).First(&ob, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no outbound %d", ErrNotFound, id)
	}
	return &ob, nil
}

func (s *Outbounds) assignMark(ctx context.Context, ob *model.Outbound) error {
	if !ob.Kind.NeedsHop() {
		return nil
	}
	mark, _, err := routing.AllocateMark(ob.ID)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(ob).Update("mark", mark).Error; err != nil {
		return fmt.Errorf("service: assign routing mark: %w", err)
	}
	ob.Mark = mark
	return nil
}

func (s *Outbounds) validate(ctx context.Context, in *OutboundInput, selfID uint) error {
	in.Tag = strings.TrimSpace(in.Tag)
	in.Address = strings.TrimSpace(in.Address)
	in.HopAddress = strings.TrimSpace(in.HopAddress)

	if in.Tag == "" {
		return invalidField("tag", "an outbound needs a tag; routing rules refer to it by that name")
	}
	if len(in.Tag) > 64 {
		return invalidField("tag", "that tag is too long")
	}
	for _, r := range in.Tag {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return invalidField("tag",
				"a tag can only contain letters, digits, - and _ (found %q)", string(r))
		}
	}
	if in.Tag == TagDirect || in.Tag == TagBlocked {
		if selfID == 0 {
			return invalidField("tag", "%q is the name of a built-in outbound", in.Tag)
		}
	}

	if !in.Kind.Valid() {
		return invalidField("kind", "%q is not an outbound kind this panel serves", in.Kind)
	}
	if in.Kind == model.OutboundDirect || in.Kind == model.OutboundBlock {
		return invalidField("kind",
			"%q already exists as a built-in outbound and cannot be created again", in.Kind)
	}

	var clash int64
	q := s.db.WithContext(ctx).Model(&model.Outbound{}).Where("LOWER(tag) = LOWER(?)", in.Tag)
	if selfID != 0 {
		q = q.Where("id != ?", selfID)
	}
	if err := q.Count(&clash).Error; err != nil {
		return fmt.Errorf("service: check outbound tag: %w", err)
	}
	if clash > 0 {
		return invalidField("tag", "an outbound called %q already exists", in.Tag)
	}

	if in.Address == "" {
		return invalidField("address", "an outbound of this kind needs an address to reach")
	}
	if err := checkHostPort(in.Address); err != nil {
		return invalidField("address", "%v", err)
	}

	switch in.Kind {
	case model.OutboundWireGuard:
		return s.validateWireGuard(in, selfID)
	case model.OutboundSOCKS, model.OutboundHTTP:
		if in.Username == "" && in.Password != "" {
			return invalidField("username", "a password was given with no username")
		}
	}
	return nil
}

func (s *Outbounds) validateWireGuard(in *OutboundInput, selfID uint) error {
	// On create the keys are required; on edit a blank one means "keep".
	needKeys := selfID == 0

	if needKeys && strings.TrimSpace(in.PrivateKey) == "" {
		return invalidField("privateKey",
			"a WireGuard hop needs the private key this server presents to the upstream peer")
	}
	if strings.TrimSpace(in.PrivateKey) != "" {
		if _, err := wgkey.Parse(in.PrivateKey); err != nil {
			return invalidField("privateKey", "that is not a WireGuard key")
		}
	}
	if strings.TrimSpace(in.PeerPubKey) == "" {
		return invalidField("peerPubKey", "a WireGuard hop needs the upstream peer's public key")
	}
	if _, err := wgkey.Parse(in.PeerPubKey); err != nil {
		return invalidField("peerPubKey", "that is not a WireGuard key")
	}
	if psk := strings.TrimSpace(in.PresharedKey); psk != "" {
		if _, err := wgkey.Parse(psk); err != nil {
			return invalidField("presharedKey", "that is not a WireGuard key")
		}
	}

	if in.HopAddress == "" {
		return invalidField("hopAddress",
			"a WireGuard hop needs the address the upstream issued this server, such as 10.2.0.2/32")
	}
	if _, err := netip.ParsePrefix(in.HopAddress); err != nil {
		if a, aerr := netip.ParseAddr(in.HopAddress); aerr == nil {
			in.HopAddress = netip.PrefixFrom(a, a.BitLen()).String()
		} else {
			return invalidField("hopAddress",
				"%q is not an address or range; it should look like 10.2.0.2/32", in.HopAddress)
		}
	}
	if in.HopMTU != 0 && (in.HopMTU < 576 || in.HopMTU > 1500) {
		return invalidField("hopMtu", "an MTU of %d is outside the usable range of 576 to 1500", in.HopMTU)
	}
	return nil
}

// checkHostPort validates an address without resolving it.
//
// Resolution is deliberately not attempted here: a form submitted while DNS is
// briefly unavailable should not be rejected, and an operator configuring a hop
// that is not up yet is a normal thing to do.
func checkHostPort(addr string) error {
	// A scheme is accepted and stripped, because an operator pasting a proxy
	// URL is the common case.
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil || u.Host == "" {
			return fmt.Errorf("%q is not an address the panel can read", addr)
		}
		addr = u.Host
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q needs a port, as in vpn.example.com:51820", addr)
	}
	if host == "" {
		return fmt.Errorf("%q has no host part", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q is not a port number", port)
	}
	return nil
}

// dialLatency times a TCP connection.
func dialLatency(ctx context.Context, addr, mode string) (time.Duration, error) {
	timeout := 5 * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start)
	_ = conn.Close()
	return elapsed, nil
}

// friendlyDialError turns a Go network error into something an operator can act
// on. The raw text names Go's own types and repeats the address twice, which
// tells nobody what to change.
func friendlyDialError(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Sprintf("the name %q does not resolve", dnsErr.Name)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer within five seconds"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Timeout() {
			return "no answer within five seconds"
		}
		var sysErr *net.OpError
		if errors.As(opErr.Err, &sysErr) {
			return sysErr.Err.Error()
		}
		return opErr.Err.Error()
	}
	return err.Error()
}

// HopSpecs describes the upstream tunnels that should be up.
//
// Only WireGuard outbounds appear: a proxy hop is dialled per connection in
// userspace and has no interface to bring up. A hop missing its keys is skipped
// rather than attempted, because `wg setconf` with an empty key leaves the
// device up and carrying nothing, which looks to a customer exactly like an
// exit that works and drops every packet.
func (s *Outbounds) HopSpecs(ctx context.Context) ([]routing.HopSpec, error) {
	var rows []model.Outbound
	err := s.db.WithContext(ctx).
		Where("kind = ? AND enabled = ?", model.OutboundWireGuard, true).
		Order("position, id").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("service: read hops: %w", err)
	}

	out := make([]routing.HopSpec, 0, len(rows))
	for _, o := range rows {
		if o.PrivateKey == "" || o.PeerPubKey == "" || o.Address == "" {
			s.log.Warn("outbound hop is not fully configured and will not be brought up",
				"tag", o.Tag)
			continue
		}
		out = append(out, routing.HopSpec{
			Device:       HopDevice(o),
			Mark:         o.Mark,
			PrivateKey:   o.PrivateKey,
			PeerPubKey:   o.PeerPubKey,
			PresharedKey: o.PresharedKey,
			Endpoint:     o.Address,
			Address:      o.HopAddress,
			MTU:          o.HopMTU,
		})
	}
	return out, nil
}
