package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// Cloudflare WARP. A device is registered with a WireGuard public key and
// answers with the peer to dial; the account can be upgraded with a WARP+
// licence, and re-registering with a fresh key is how the exit address is
// changed.
const (
	warpAPIBase   = "https://api.cloudflareclient.com/v0a4005"
	warpClientVer = "a-6.30-3596"
	// What WARP works with over a plain WireGuard tunnel.
	warpEndpoint = "engage.cloudflareclient.com:2408"
	warpDNS      = "1.1.1.1"
	warpMTU      = 1280
)

// WarpAccount is what registering a device leaves behind.
type WarpAccount struct {
	AccessToken string `json:"accessToken"`
	DeviceID    string `json:"deviceId"`
	LicenseKey  string `json:"licenseKey"`
	PrivateKey  string `json:"privateKey"`
	ClientID    string `json:"clientId,omitempty"`
}

// WarpState is the dialog's view: the account, the live device record from
// Cloudflare, and whether an outbound has been made from it.
type WarpState struct {
	Registered bool            `json:"registered"`
	Account    *WarpAccount    `json:"account,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
	Outbound   *model.Outbound `json:"outbound,omitempty"`
}

func (p *Providers) warpRequest(ctx context.Context, method, path string, body any, token string) ([]byte, error) {
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, warpAPIBase+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("CF-Client-Version", warpClientVer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "okhttp/3.12.1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return p.do(req)
}

// WarpState reads the account and, when there is one, asks Cloudflare for the
// device's current record.
func (p *Providers) WarpState(ctx context.Context) (*WarpState, error) {
	var acct WarpAccount
	have, err := p.load(ctx, ProviderWARP, &acct)
	if err != nil {
		return nil, err
	}
	st := &WarpState{Registered: have}
	if !have {
		return st, nil
	}
	st.Account = &acct
	if raw, err := p.warpRequest(ctx, http.MethodGet, "/reg/"+acct.DeviceID, nil, acct.AccessToken); err == nil {
		st.Config = raw
	} else {
		p.log.Warn("warp: could not fetch the device record", "err", err)
	}
	st.Outbound = p.warpOutbound(ctx)
	return st, nil
}

func (p *Providers) warpOutbound(ctx context.Context) *model.Outbound {
	rows, err := p.outbounds.ByProvider(ctx, ProviderWARP)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return &rows[0]
}

// WarpRegister creates a fresh device with a new key pair.
func (p *Providers) WarpRegister(ctx context.Context) (*WarpState, error) {
	pair, err := wgkey.NewPair()
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	raw, err := p.warpRequest(ctx, http.MethodPost, "/reg", map[string]any{
		"key":   pair.Public.String(),
		"tos":   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"type":  "PC",
		"model": "w-ui",
		"name":  host,
	}, "")
	if err != nil {
		return nil, err
	}
	var rsp struct {
		ID      string `json:"id"`
		Token   string `json:"token"`
		Account struct {
			License string `json:"license"`
		} `json:"account"`
		Config struct {
			ClientID string `json:"client_id"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &rsp); err != nil || rsp.ID == "" || rsp.Token == "" {
		return nil, fmt.Errorf("%w: WARP answered without a device id and token", ErrInvalid)
	}
	acct := WarpAccount{
		AccessToken: rsp.Token,
		DeviceID:    rsp.ID,
		LicenseKey:  rsp.Account.License,
		PrivateKey:  pair.Private.String(),
		ClientID:    rsp.Config.ClientID,
	}
	if err := p.store(ctx, ProviderWARP, acct); err != nil {
		return nil, err
	}
	p.log.Info("warp: device registered", "device", rsp.ID)
	return &WarpState{Registered: true, Account: &acct, Config: raw, Outbound: p.warpOutbound(ctx)}, nil
}

// WarpSetLicense attaches a WARP+ licence to the device.
func (p *Providers) WarpSetLicense(ctx context.Context, license string) (*WarpState, error) {
	license = strings.TrimSpace(license)
	if len(license) < 26 {
		return nil, invalidField("license", "a WARP+ key is 26 characters")
	}
	var acct WarpAccount
	if have, err := p.load(ctx, ProviderWARP, &acct); err != nil {
		return nil, err
	} else if !have {
		return nil, fmt.Errorf("%w: no WARP device is registered", ErrInvalid)
	}
	_, err := p.warpRequest(ctx, http.MethodPut, "/reg/"+acct.DeviceID+"/account",
		map[string]string{"license": license}, acct.AccessToken)
	if err != nil {
		return nil, err
	}
	acct.LicenseKey = license
	if err := p.store(ctx, ProviderWARP, acct); err != nil {
		return nil, err
	}
	return p.WarpState(ctx)
}

// WarpDelete forgets the device. The outbound made from it stays: it still
// works until Cloudflare drops the key, and removing it is a routing decision.
func (p *Providers) WarpDelete(ctx context.Context) error {
	return p.forget(ctx, ProviderWARP)
}

// WarpChangeIP registers a new key, which gives the device a new address, and
// carries the licence and the outbound over to it.
func (p *Providers) WarpChangeIP(ctx context.Context) (*WarpState, error) {
	var old WarpAccount
	hadOld, err := p.load(ctx, ProviderWARP, &old)
	if err != nil {
		return nil, err
	}
	st, err := p.WarpRegister(ctx)
	if err != nil {
		return nil, err
	}
	if hadOld && len(old.LicenseKey) >= 26 {
		if st2, err := p.WarpSetLicense(ctx, old.LicenseKey); err != nil {
			p.log.Warn("warp: licence could not be re-applied to the new device", "err", err)
		} else {
			st = st2
		}
	}
	if st.Outbound != nil {
		if _, err := p.WarpAddOutbound(ctx); err != nil {
			return nil, err
		}
		st.Outbound = p.warpOutbound(ctx)
	}
	return st, nil
}

// WarpAddOutbound makes (or renews) the WireGuard hop that leaves through WARP.
func (p *Providers) WarpAddOutbound(ctx context.Context) (*model.Outbound, error) {
	var acct WarpAccount
	if have, err := p.load(ctx, ProviderWARP, &acct); err != nil {
		return nil, err
	} else if !have {
		return nil, fmt.Errorf("%w: register a WARP device first", ErrInvalid)
	}
	raw, err := p.warpRequest(ctx, http.MethodGet, "/reg/"+acct.DeviceID, nil, acct.AccessToken)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Config struct {
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
				} `json:"addresses"`
			} `json:"interface"`
			Peers []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					Host string `json:"host"`
				} `json:"endpoint"`
			} `json:"peers"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Config.Peers) == 0 {
		return nil, fmt.Errorf("%w: the WARP device record has no peer to dial", ErrInvalid)
	}
	endpoint := cfg.Config.Peers[0].Endpoint.Host
	if endpoint == "" {
		endpoint = warpEndpoint
	}
	tag := "warp"
	if p.warpOutbound(ctx) == nil {
		if tag, err = p.uniqueTag(ctx, tag); err != nil {
			return nil, err
		}
	}
	ob, _, err := p.upsertHop(ctx, ProviderWARP, acct.DeviceID, OutboundInput{
		Tag:        tag,
		Kind:       model.OutboundWireGuard,
		Address:    endpoint,
		PrivateKey: acct.PrivateKey,
		PeerPubKey: cfg.Config.Peers[0].PublicKey,
		HopAddress: cfg.Config.Interface.Addresses.V4,
		HopDNS:     warpDNS,
		HopMTU:     warpMTU,
		Note:       "Cloudflare WARP",
	})
	return ob, err
}
