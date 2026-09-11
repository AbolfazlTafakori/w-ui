package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// Private Internet Access. A username and password buy a token good for a
// day; the token and a WireGuard public key are presented to the chosen
// server's own key-registration port, which answers with the peer to dial.
const (
	piaTokenURL      = "https://www.privateinternetaccess.com/api/client/v2/token"
	piaServerListURL = "https://serverlist.piaservers.net/vpninfo/servers/v6"
	piaAddKeyPort    = "1337"
	piaTokenTTL      = 24 * time.Hour
)

// PIA's servers present certificates from its own authority, so the key
// registration is verified against that rather than the system roots.
//
//go:embed piatrust/ca.rsa.4096.crt
var piaCA []byte

// PIAAccount is what logging in leaves behind. The password is not kept: a
// token lasts a day and logging in again is one form.
type PIAAccount struct {
	Username string    `json:"username"`
	TokenAt  time.Time `json:"tokenAt"`
	HasToken bool      `json:"hasToken"`
}

// piaStored is the on-disk shape, with the token.
type piaStored struct {
	Username string    `json:"username"`
	Token    string    `json:"token"`
	TokenAt  time.Time `json:"tokenAt"`
}

// PIAState is the dialog's view.
type PIAState struct {
	LoggedIn bool             `json:"loggedIn"`
	Account  *PIAAccount      `json:"account,omitempty"`
	Added    []model.Outbound `json:"added"`
}

// PIARegion is one region from the server list with its WireGuard servers.
type PIARegion struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Country string      `json:"country"`
	Servers []PIAServer `json:"servers"`
}

// PIAServer is one WireGuard server: the address to reach it and the name
// its certificate carries.
type PIAServer struct {
	IP       string `json:"ip"`
	CN       string `json:"cn"`
	Hostname string `json:"hostname"`
}

func (p *Providers) piaLoad(ctx context.Context) (*piaStored, bool, error) {
	var st piaStored
	have, err := p.load(ctx, ProviderPIA, &st)
	return &st, have, err
}

func (p *Providers) PIAState(ctx context.Context) (*PIAState, error) {
	st, have, err := p.piaLoad(ctx)
	if err != nil {
		return nil, err
	}
	added, err := p.outbounds.ByProvider(ctx, ProviderPIA)
	if err != nil {
		return nil, err
	}
	out := &PIAState{LoggedIn: have, Added: added}
	if have {
		out.Account = &PIAAccount{
			Username: st.Username,
			TokenAt:  st.TokenAt,
			HasToken: st.Token != "" && time.Since(st.TokenAt) < piaTokenTTL,
		}
	}
	return out, nil
}

// PIALogin trades a username and password for a token.
func (p *Providers) PIALogin(ctx context.Context, username, password string) (*PIAState, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, invalidField("username", "enter the PIA username and password")
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("username", username)
	_ = w.WriteField("password", password)
	_ = w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, piaTokenURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	raw, err := p.do(req)
	if err != nil {
		return nil, err
	}
	var rsp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &rsp); err != nil || rsp.Token == "" {
		return nil, fmt.Errorf("%w: PIA did not return a token", ErrInvalid)
	}
	err = p.store(ctx, ProviderPIA, piaStored{Username: username, Token: rsp.Token, TokenAt: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	return p.PIAState(ctx)
}

func (p *Providers) PIALogout(ctx context.Context) error {
	return p.forget(ctx, ProviderPIA)
}

// PIARegions fetches the server list. It arrives as one JSON line followed
// by a signature; the line is what is read.
func (p *Providers) PIARegions(ctx context.Context) ([]PIARegion, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, piaServerListURL, nil)
	if err != nil {
		return nil, err
	}
	raw, err := p.do(req)
	if err != nil {
		return nil, err
	}
	first, _, _ := bytes.Cut(raw, []byte("\n"))
	var env struct {
		Regions []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Country string `json:"country"`
			Offline bool   `json:"offline"`
			Servers struct {
				WG []PIAServer `json:"wg"`
			} `json:"servers"`
		} `json:"regions"`
	}
	if err := json.Unmarshal(first, &env); err != nil {
		return nil, fmt.Errorf("%w: PIA's server list is not readable", ErrInvalid)
	}
	out := make([]PIARegion, 0, len(env.Regions))
	for _, r := range env.Regions {
		if r.Offline || len(r.Servers.WG) == 0 {
			continue
		}
		servers := r.Servers.WG
		for i := range servers {
			if servers[i].Hostname == "" {
				servers[i].Hostname = servers[i].CN
			}
		}
		out = append(out, PIARegion{ID: r.ID, Name: r.Name, Country: strings.ToUpper(r.Country), Servers: servers})
	}
	return out, nil
}

// PIAAddOutbound registers a fresh key with the chosen server and makes (or
// renews) the hop to it.
func (p *Providers) PIAAddOutbound(ctx context.Context, regionID, hostname string) (*model.Outbound, bool, error) {
	st, have, err := p.piaLoad(ctx)
	if err != nil {
		return nil, false, err
	}
	if !have || st.Token == "" {
		return nil, false, fmt.Errorf("%w: log in to PIA first", ErrInvalid)
	}
	if time.Since(st.TokenAt) >= piaTokenTTL {
		return nil, false, fmt.Errorf("%w: the PIA token has expired; log in again", ErrInvalid)
	}
	regions, err := p.PIARegions(ctx)
	if err != nil {
		return nil, false, err
	}
	var server *PIAServer
	var region *PIARegion
	for i := range regions {
		if regions[i].ID != regionID {
			continue
		}
		region = &regions[i]
		for j := range regions[i].Servers {
			if hostname == "" || regions[i].Servers[j].Hostname == hostname || regions[i].Servers[j].CN == hostname {
				server = &regions[i].Servers[j]
				break
			}
		}
	}
	if region == nil || server == nil {
		return nil, false, invalidField("hostname", "that server is not in PIA's list")
	}

	pair, err := wgkey.NewPair()
	if err != nil {
		return nil, false, err
	}
	reg, err := p.piaRegisterKey(ctx, *server, st.Token, pair.Public.String())
	if err != nil {
		return nil, false, err
	}

	base := "pia-" + strings.SplitN(server.Hostname, ".", 2)[0]
	tag := base
	existing, err := p.outbounds.ByProvider(ctx, ProviderPIA)
	if err != nil {
		return nil, false, err
	}
	renew := false
	for _, ob := range existing {
		if ob.ProviderRef == server.Hostname {
			renew = true
		}
	}
	if !renew {
		if tag, err = p.uniqueTag(ctx, base); err != nil {
			return nil, false, err
		}
	}
	dns := ""
	if len(reg.DNS) > 0 {
		dns = reg.DNS[0]
	}
	return p.upsertHop(ctx, ProviderPIA, server.Hostname, OutboundInput{
		Tag:        tag,
		Kind:       model.OutboundWireGuard,
		Address:    net.JoinHostPort(reg.ServerIP, fmt.Sprint(reg.ServerPort)),
		PrivateKey: pair.Private.String(),
		PeerPubKey: reg.ServerKey,
		HopAddress: reg.PeerIP,
		HopDNS:     dns,
		Note:       "PIA " + region.Name,
	})
}

type piaRegistration struct {
	PeerIP     string
	ServerKey  string
	ServerIP   string
	ServerPort int
	DNS        []string
}

func (p *Providers) piaRegisterKey(ctx context.Context, server PIAServer, token, pubkey string) (*piaRegistration, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(piaCA) {
		return nil, fmt.Errorf("service: the built-in PIA certificate authority is not readable")
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tr := &http.Transport{
		// The certificate names the server's CN; the connection goes to its
		// address, which no public DNS resolves.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, net.JoinHostPort(server.IP, piaAddKeyPort))
		},
		TLSClientConfig:       &tls.Config{ServerName: server.CN, RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 12 * time.Second,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 20 * time.Second}

	q := url.Values{}
	q.Set("pt", token)
	q.Set("pubkey", pubkey)
	u := url.URL{Scheme: "https", Host: net.JoinHostPort(server.CN, piaAddKeyPort), Path: "/addKey", RawQuery: q.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: the PIA server could not be reached: %v", ErrInvalid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: PIA rejected the token; log in again", ErrInvalid)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: PIA key registration answered %s", ErrInvalid, resp.Status)
	}
	var payload struct {
		Status     string   `json:"status"`
		PeerIP     string   `json:"peer_ip"`
		ServerKey  string   `json:"server_key"`
		ServerIP   string   `json:"server_ip"`
		ServerPort int      `json:"server_port"`
		DNSServers []string `json:"dns_servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: PIA key registration returned malformed JSON", ErrInvalid)
	}
	if payload.Status != "OK" {
		return nil, fmt.Errorf("%w: the PIA server rejected the key", ErrInvalid)
	}
	return &piaRegistration{
		PeerIP:     payload.PeerIP,
		ServerKey:  payload.ServerKey,
		ServerIP:   payload.ServerIP,
		ServerPort: payload.ServerPort,
		DNS:        payload.DNSServers,
	}, nil
}
