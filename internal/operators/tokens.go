package operators

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

// An operator's own API tokens.
//
// A reseller with a shop front, or an admin with a billing script, wants to
// make customers without a browser. The token is theirs: it is checked here
// and nothing more is decided here. What it may reach is worked out on every
// request from the operator as they are then (internal/api), so it can never
// hold more than its operator does.

// OperatorTokenPrefix marks an operator's token. Not the machine tokens'
// "wui_": the two are told apart before either is looked up, so neither can
// be taken for the other.
const OperatorTokenPrefix = "wuo_"

// DefaultTokenDays is how long a token lasts when its maker says nothing: a
// token forgotten in a script ends on its own.
const DefaultTokenDays = 90

// lastUsedEvery is how often a token's last use is written. Every request
// would be a write per call of a busy script, for a figure shown to the day.
const lastUsedEvery = time.Minute

// OperatorTokenPlain is a token as issued: the secret, shown this once.
type OperatorTokenPlain struct {
	Token string               `json:"token"`
	Meta  *model.OperatorToken `json:"meta"`
}

func hashOperatorToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// IssueToken makes a token for target, issued by caller (nil or the owner:
// the owner). days is how long it lasts: nil is DefaultTokenDays, zero is
// never, and more than a year is refused.
//
// Tokens are an admin's to give: an admin issues them for themselves and for
// their own resellers, each one spending a token of the admin's capacity in
// the same transaction as the token -- refused when none is left. A token
// revoked is spent for good. The owner issues them for any admin or reseller
// and spends nothing. A reseller issues none (refused in the API before it
// gets here, and here again).
func (s *Admins) IssueToken(ctx context.Context, caller, target *model.Admin, name string, days *int) (*OperatorTokenPlain, error) {
	if target == nil || (target.Role != model.RoleAdmin && target.Role != model.RoleReseller) {
		return nil, fmt.Errorf("%w: these tokens are an admin's or a reseller's; the owner issues machine tokens", fault.ErrInvalid)
	}
	if caller != nil && caller.Role == model.RoleReseller {
		return nil, fmt.Errorf("%w: a reseller's tokens are issued by their admin", fault.ErrInvalid)
	}
	if caller != nil && caller.Role == model.RoleAdmin && target.ID != caller.ID && target.ParentAdminID != caller.ID {
		return nil, fmt.Errorf("%w: operator", fault.ErrNotFound)
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return nil, fault.Field("name", "give the token a name of up to 64 characters, so you know what it is for")
	}
	d := DefaultTokenDays
	if days != nil {
		d = *days
	}
	if d < 0 || d > 365 {
		return nil, fault.Field("days", "a token lasts up to 365 days, or 0 for no end")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("service: generate token: %w", err)
	}
	secret := OperatorTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	row := model.OperatorToken{AdminID: target.ID, Name: name, Hash: hashOperatorToken(secret), Prefix: secret[:12]}
	if d > 0 {
		at := time.Now().UTC().AddDate(0, 0, d)
		row.ExpiresAt = &at
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if caller == nil || caller.Role == model.RoleOwner {
			return nil // the owner's: nothing spent
		}
		acc, err := capacity.Account(tx, caller.ID, model.CapacityTokens)
		if err != nil {
			return err
		}
		if err := capacity.Take(tx, acc, 1, capacity.Entry{Reason: model.CapacityIssue, ActorID: caller.ID, TokenID: row.ID}); err != nil {
			if errors.Is(err, capacity.ErrFull) {
				return fault.Field("tokens", "you have no tokens left to issue; ask the owner for more")
			}
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fault.ErrInvalid) || errors.Is(err, fault.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("service: store token: %w", err)
	}
	// The prefix only: the secret is never written anywhere but the answer.
	s.log.Info("operator token issued", "for", target.Username, "name", name, "prefix", row.Prefix)
	return &OperatorTokenPlain{Token: secret, Meta: &row}, nil
}

// TokenCapacity is what an operator has of tokens, for their page and their
// menu: an admin's capacity (granted, spent), and how many live tokens they
// hold. A reseller has no capacity of their own, only tokens issued to them.
type TokenCapacity struct {
	Granted int64 `json:"granted"`
	Used    int64 `json:"used"`
	Held    int64 `json:"held"`
}

// TokensOf reads an operator's token capacity and how many live tokens they
// hold, in two indexed reads.
func (s *Admins) TokensOf(ctx context.Context, a *model.Admin) (TokenCapacity, error) {
	var out TokenCapacity
	if a == nil {
		return out, nil
	}
	if acc, err := capacity.Account(s.db.WithContext(ctx), a.ID, model.CapacityTokens); err != nil {
		return out, err
	} else if acc != nil {
		out.Granted, out.Used = acc.Granted, acc.Used
	}
	err := s.db.WithContext(ctx).Model(&model.OperatorToken{}).
		Where("admin_id = ? AND (expires_at IS NULL OR expires_at > ?)", a.ID, time.Now().UTC()).Count(&out.Held).Error
	return out, err
}

// setTokens brings an admin's token capacity to limit, the owner's to set:
// a charge or a debit of the difference. A debit below what is spent cuts
// no token off; it leaves none to issue. Zero is no tokens to issue.
func setTokens(tx *gorm.DB, actor uint, target *model.Admin, limit int64) error {
	if limit < 0 || limit > capacity.MaxUnits {
		return fault.Field("tokenLimit", "tokens are given as a number from 0 to %d", capacity.MaxUnits)
	}
	if target.Role != model.RoleAdmin {
		return fault.Field("tokenLimit", "token capacity is an admin's; a reseller's tokens are issued by their admin")
	}
	acc, err := capacity.Account(tx, target.ID, model.CapacityTokens)
	if err != nil {
		return err
	}
	if acc == nil {
		if limit == 0 {
			return nil
		}
		if acc, err = capacity.Open(tx, target.ID, model.CapacityTokens, model.CapacityFixed); err != nil {
			return err
		}
	}
	diff := limit - acc.Granted
	e := capacity.Entry{ActorID: actor}
	switch {
	case diff > 0:
		e.Reason = model.CapacityCharge
		return capacity.Charge(tx, acc, diff, e)
	case diff < 0:
		e.Reason = model.CapacityDebit
		return capacity.Debit(tx, acc, -diff, e)
	}
	return nil
}

// TokenOperator is the operator a token belongs to, or an error when it is
// not a live token: unknown, expired, or its operator gone. Whether that
// operator is allowed in right now is the caller's to decide, with the same
// rules as a session.
func (s *Admins) TokenOperator(ctx context.Context, presented string) (*model.Admin, error) {
	if !strings.HasPrefix(presented, OperatorTokenPrefix) {
		return nil, fault.ErrNotFound
	}
	// By hash, which is unique and indexed: no loop whose length says how
	// many tokens there are.
	var row model.OperatorToken
	if err := s.db.WithContext(ctx).Where("hash = ?", hashOperatorToken(presented)).First(&row).Error; err != nil {
		return nil, fault.ErrNotFound
	}
	now := time.Now().UTC()
	if row.ExpiresAt != nil && !now.Before(*row.ExpiresAt) {
		return nil, fault.ErrNotFound
	}
	var a model.Admin
	if err := s.db.WithContext(ctx).First(&a, row.AdminID).Error; err != nil {
		return nil, fault.ErrNotFound
	}
	if a.Role != model.RoleAdmin && a.Role != model.RoleReseller {
		return nil, fault.ErrNotFound
	}
	if row.LastUsedAt == nil || now.Sub(*row.LastUsedAt) > lastUsedEvery {
		s.db.WithContext(ctx).Model(&model.OperatorToken{}).Where("id = ?", row.ID).UpdateColumn("last_used_at", now)
	}
	return &a, nil
}

// tokenHolders is whose tokens this operator may see and revoke: the owner
// every operator's, an admin their own and their resellers', a reseller
// their own. Nil is everyone's.
func (s *Admins) tokenHolders(ctx context.Context, me *model.Admin) ([]uint, error) {
	switch {
	case me == nil, me.Role == model.RoleOwner:
		return nil, nil
	case me.Role == model.RoleAdmin:
		resellers, err := s.ResellerIDs(ctx, me.ID)
		if err != nil {
			return nil, err
		}
		return append([]uint{me.ID}, resellers...), nil
	default:
		return []uint{me.ID}, nil
	}
}

// ListTokens is the tokens this operator may see, newest first, with whose
// each is when they are someone else's. No secret: there is none kept.
func (s *Admins) ListTokens(ctx context.Context, me *model.Admin) ([]model.OperatorToken, error) {
	holders, err := s.tokenHolders(ctx, me)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Order("created_at DESC, id DESC")
	if holders != nil {
		q = q.Where("admin_id IN ?", holders)
	}
	out := []model.OperatorToken{}
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("service: list operator tokens: %w", err)
	}
	ids := map[uint]bool{}
	for _, t := range out {
		ids[t.AdminID] = true
	}
	if len(ids) > 0 {
		list := make([]uint, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		var names []struct {
			ID       uint
			Username string
		}
		if err := s.db.WithContext(ctx).Model(&model.Admin{}).Where("id IN ?", list).Select("id, username").Scan(&names).Error; err != nil {
			return nil, fmt.Errorf("service: name operator tokens: %w", err)
		}
		byID := map[uint]string{}
		for _, n := range names {
			byID[n.ID] = n.Username
		}
		for i := range out {
			out[i].Username = byID[out[i].AdminID]
		}
	}
	return out, nil
}

// RevokeToken deletes a token this operator may see. One they may not is
// not found, so whether it exists is not told. The token it spent of its
// admin's capacity is not given back: a revoked token is spent for good, so
// revoking and reissuing is not a way to more.
func (s *Admins) RevokeToken(ctx context.Context, me *model.Admin, id uint) error {
	holders, err := s.tokenHolders(ctx, me)
	if err != nil {
		return err
	}
	q := s.db.WithContext(ctx).Where("id = ?", id)
	if holders != nil {
		q = q.Where("admin_id IN ?", holders)
	}
	res := q.Delete(&model.OperatorToken{})
	if res.Error != nil {
		return fmt.Errorf("service: revoke operator token: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: token", fault.ErrNotFound)
	}
	return nil
}

// DropTokensOf removes every token of an operator who is removed, inside the
// removal: a token of nobody's would be refused anyway, but should not be
// left behind to be read.
func DropTokensOf(tx *gorm.DB, adminID uint) error {
	return tx.Where("admin_id = ?", adminID).Delete(&model.OperatorToken{}).Error
}
