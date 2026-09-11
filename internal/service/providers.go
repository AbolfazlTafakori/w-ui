package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Providers are the three services 3x-ui's outbound page can build a hop
// from: Cloudflare WARP, NordVPN and Private Internet Access. Each one is a
// WireGuard endpoint behind an account, and what this service does is hold the
// account and turn a chosen server into a WireGuard hop outbound.
type Providers struct {
	db        *gorm.DB
	outbounds *Outbounds
	log       *slog.Logger
	http      *http.Client
}

// NewProviders builds the service.
func NewProviders(db *gorm.DB, outbounds *Outbounds, log *slog.Logger) *Providers {
	return &Providers{
		db:        db,
		outbounds: outbounds,
		log:       log,
		http:      &http.Client{Timeout: 20 * time.Second},
	}
}

const (
	ProviderWARP = "warp"
	ProviderNord = "nord"
	ProviderPIA  = "pia"

	// How much of a provider's answer is read. Their server lists are a few
	// megabytes; anything past this is not a server list.
	maxProviderBody = 10 << 20
)

// Account data is kept as one JSON blob per provider in the settings table,
// under these keys.
func providerKey(provider string) string { return "provider." + provider }

func (p *Providers) load(ctx context.Context, provider string, into any) (bool, error) {
	var row model.Setting
	err := p.db.WithContext(ctx).Where("key = ?", providerKey(provider)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && row.Value == "") {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("service: read %s account: %w", provider, err)
	}
	if err := json.Unmarshal([]byte(row.Value), into); err != nil {
		return false, fmt.Errorf("service: %s account is not readable: %w", provider, err)
	}
	return true, nil
}

func (p *Providers) store(ctx context.Context, provider string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	row := model.Setting{Key: providerKey(provider), Value: string(raw), UpdatedAt: time.Now().UTC()}
	err = p.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("service: save %s account: %w", provider, err)
	}
	return nil
}

func (p *Providers) forget(ctx context.Context, provider string) error {
	err := p.db.WithContext(ctx).Where("key = ?", providerKey(provider)).Delete(&model.Setting{}).Error
	if err != nil {
		return fmt.Errorf("service: forget %s account: %w", provider, err)
	}
	return nil
}

// do sends a request and hands back the body, with a provider's own error
// message surfaced when it gave one.
func (p *Providers) do(req *http.Request) ([]byte, error) {
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var env struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(body, &env) == nil && len(env.Errors) > 0 && env.Errors[0].Message != "" {
			return nil, fmt.Errorf("%w: %s", ErrInvalid, env.Errors[0].Message)
		}
		return nil, fmt.Errorf("%w: %s answered %s", ErrInvalid, req.URL.Host, resp.Status)
	}
	return body, nil
}

// upsertHop creates the WireGuard hop for a provider's server, or renews the
// keys on the one that already exists for it.
func (p *Providers) upsertHop(ctx context.Context, provider, ref string, in OutboundInput) (*model.Outbound, bool, error) {
	existing, err := p.outbounds.ByProvider(ctx, provider)
	if err != nil {
		return nil, false, err
	}
	for _, ob := range existing {
		if ob.ProviderRef != ref {
			continue
		}
		in.Tag = ob.Tag
		updated, err := p.outbounds.Update(ctx, ob.ID, in)
		if err != nil {
			return nil, false, err
		}
		return updated, true, nil
	}
	ob, err := p.outbounds.Create(ctx, in)
	if err != nil {
		return nil, false, err
	}
	if err := p.outbounds.SetOrigin(ctx, ob.ID, provider, ref, 0); err != nil {
		return nil, false, err
	}
	ob.Provider, ob.ProviderRef = provider, ref
	return ob, false, nil
}

// uniqueTag returns base, or base-2, base-3, ... until one is free.
func (p *Providers) uniqueTag(ctx context.Context, base string) (string, error) {
	for i := 1; i < 1000; i++ {
		tag := base
		if i > 1 {
			tag = fmt.Sprintf("%s-%d", base, i)
		}
		var n int64
		err := p.db.WithContext(ctx).Model(&model.Outbound{}).
			Where("LOWER(tag) = LOWER(?)", tag).Count(&n).Error
		if err != nil {
			return "", fmt.Errorf("service: check tag: %w", err)
		}
		if n == 0 {
			return tag, nil
		}
	}
	return "", fmt.Errorf("%w: no free tag starting with %q", ErrInvalid, base)
}
