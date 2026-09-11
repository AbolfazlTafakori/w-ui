package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// NordVPN. An access token from the account page yields the NordLynx private
// key; every server publishes its WireGuard public key in the server list.
const (
	nordAPIBase = "https://api.nordvpn.com"
	// What a NordLynx client is issued and resolves with.
	nordHopAddress = "10.5.0.2/32"
	nordDNS        = "103.86.96.100"
	nordPort       = "51820"
)

// NordAccount is what logging in leaves behind.
type NordAccount struct {
	Token      string `json:"token"`
	PrivateKey string `json:"privateKey"`
}

// NordState is the dialog's view.
type NordState struct {
	LoggedIn bool             `json:"loggedIn"`
	Account  *NordAccount     `json:"account,omitempty"`
	Added    []model.Outbound `json:"added"`
}

// NordCountry is one entry of the country list, with its cities.
type NordCountry struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Cities []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"cities"`
}

// NordServer is one WireGuard-capable server.
type NordServer struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Hostname  string `json:"hostname"`
	Station   string `json:"station"`
	Load      int    `json:"load"`
	CityID    int    `json:"cityId"`
	City      string `json:"city"`
	PublicKey string `json:"publicKey"`
}

func (p *Providers) NordState(ctx context.Context) (*NordState, error) {
	var acct NordAccount
	have, err := p.load(ctx, ProviderNord, &acct)
	if err != nil {
		return nil, err
	}
	added, err := p.outbounds.ByProvider(ctx, ProviderNord)
	if err != nil {
		return nil, err
	}
	st := &NordState{LoggedIn: have, Added: added}
	if have {
		st.Account = &acct
	}
	return st, nil
}

// NordLogin fetches the NordLynx key with an access token.
func (p *Providers) NordLogin(ctx context.Context, token string) (*NordState, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, invalidField("token", "an access token is needed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nordAPIBase+"/v1/users/services/credentials", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("token", token)
	body, err := p.do(req)
	if err != nil {
		return nil, err
	}
	var creds struct {
		Key string `json:"nordlynx_private_key"`
	}
	if err := json.Unmarshal(body, &creds); err != nil || creds.Key == "" {
		return nil, fmt.Errorf("%w: NordVPN did not return a NordLynx private key", ErrInvalid)
	}
	if err := p.store(ctx, ProviderNord, NordAccount{Token: token, PrivateKey: creds.Key}); err != nil {
		return nil, err
	}
	return p.NordState(ctx)
}

// NordSetKey stores a NordLynx private key typed in by hand.
func (p *Providers) NordSetKey(ctx context.Context, key string) (*NordState, error) {
	key = strings.TrimSpace(key)
	if _, err := wgkey.Parse(key); err != nil {
		return nil, invalidField("privateKey", "that is not a WireGuard key")
	}
	if err := p.store(ctx, ProviderNord, NordAccount{PrivateKey: key}); err != nil {
		return nil, err
	}
	return p.NordState(ctx)
}

func (p *Providers) NordLogout(ctx context.Context) error {
	return p.forget(ctx, ProviderNord)
}

// NordCountries lists the countries with WireGuard servers.
func (p *Providers) NordCountries(ctx context.Context) ([]NordCountry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		nordAPIBase+"/v1/servers/countries?filters[servers_technologies][identifier]=wireguard_udp", nil)
	if err != nil {
		return nil, err
	}
	body, err := p.do(req)
	if err != nil {
		return nil, err
	}
	var out []NordCountry
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: NordVPN's country list is not readable", ErrInvalid)
	}
	return out, nil
}

// NordServers lists one country's WireGuard servers with their public keys.
func (p *Providers) NordServers(ctx context.Context, countryID int) ([]NordServer, error) {
	if countryID <= 0 {
		return nil, invalidField("countryId", "pick a country")
	}
	q := url.Values{}
	q.Set("limit", "0")
	q.Set("filters[servers_technologies][identifier]", "wireguard_udp")
	q.Set("filters[country_id]", fmt.Sprint(countryID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nordAPIBase+"/v1/servers?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	body, err := p.do(req)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Hostname     string `json:"hostname"`
		Station      string `json:"station"`
		Load         int    `json:"load"`
		Technologies []struct {
			Identifier string `json:"identifier"`
			Metadata   []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"metadata"`
		} `json:"technologies"`
		Locations []struct {
			Country struct {
				City struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
				} `json:"city"`
			} `json:"country"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: NordVPN's server list is not readable", ErrInvalid)
	}
	out := make([]NordServer, 0, len(raw))
	for _, r := range raw {
		s := NordServer{ID: r.ID, Name: r.Name, Hostname: r.Hostname, Station: r.Station, Load: r.Load}
		for _, t := range r.Technologies {
			if t.Identifier != "wireguard_udp" {
				continue
			}
			for _, m := range t.Metadata {
				if m.Name == "public_key" {
					s.PublicKey = m.Value
				}
			}
		}
		if len(r.Locations) > 0 {
			s.CityID = r.Locations[0].Country.City.ID
			s.City = r.Locations[0].Country.City.Name
		}
		out = append(out, s)
	}
	return out, nil
}

// NordAddOutbound makes (or renews) the hop to one NordVPN server.
func (p *Providers) NordAddOutbound(ctx context.Context, hostname, publicKey string) (*model.Outbound, bool, error) {
	var acct NordAccount
	if have, err := p.load(ctx, ProviderNord, &acct); err != nil {
		return nil, false, err
	} else if !have {
		return nil, false, fmt.Errorf("%w: log in to NordVPN first", ErrInvalid)
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, false, invalidField("hostname", "pick a server")
	}
	if _, err := wgkey.Parse(publicKey); err != nil {
		return nil, false, invalidField("publicKey", "the selected server does not advertise a NordLynx public key")
	}
	// de1234.nordvpn.com -> nord-de1234
	base := "nord-" + strings.SplitN(hostname, ".", 2)[0]
	tag := base
	existing, err := p.outbounds.ByProvider(ctx, ProviderNord)
	if err != nil {
		return nil, false, err
	}
	renew := false
	for _, ob := range existing {
		if ob.ProviderRef == hostname {
			renew = true
		}
	}
	if !renew {
		if tag, err = p.uniqueTag(ctx, base); err != nil {
			return nil, false, err
		}
	}
	return p.upsertHop(ctx, ProviderNord, hostname, OutboundInput{
		Tag:        tag,
		Kind:       model.OutboundWireGuard,
		Address:    hostname + ":" + nordPort,
		PrivateKey: acct.PrivateKey,
		PeerPubKey: publicKey,
		HopAddress: nordHopAddress,
		HopDNS:     nordDNS,
		Note:       "NordVPN " + hostname,
	})
}
