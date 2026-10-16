package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Speaking to a Pasarguard panel added as a node.
//
// Its API answers under /api with an API key made on that panel (API keys,
// in its sidebar) in the X-Api-Key header, and answers in plain JSON. What
// this panel reads and writes: the cores' inbounds, a group per inbound,
// and a user per file in that inbound's group, whose credentials Pasarguard
// makes and whose links its subscription gives.

const pgTimeout = 20 * time.Second

type pgAPI struct {
	node   model.Node
	client *http.Client
	base   string
}

func newPGAPI(ctx context.Context, node model.Node) (*pgAPI, error) {
	c, err := clientFor(node, pgTimeout, nil)
	if err != nil {
		return nil, err
	}
	p := &pgAPI{node: node, client: c, base: strings.TrimRight(node.Address, "/")}
	if node.Token == "" && node.Username != "" {
		if _, ok := pgSessions.Load(pgSessionKey(node)); !ok {
			if err := p.login(ctx); err != nil {
				return nil, err
			}
		}
	}
	return p, nil
}

func (p *pgAPI) do(ctx context.Context, method, path string, body, into any) error {
	err := p.raw(ctx, method, p.base+path, body, into, true)
	// A kept sign-in that has lapsed: sign in again, once.
	if p.node.Token == "" && errors.Is(err, errPGRefused) {
		pgSessions.Delete(pgSessionKey(p.node))
		if lerr := p.login(ctx); lerr != nil {
			return lerr
		}
		err = p.raw(ctx, method, p.base+path, body, into, true)
	}
	return err
}

var errPGRefused = errors.New("the API key was refused; make a new one on that Pasarguard panel under API keys")

// pgSessions holds each signed-in Pasarguard node's access token, by node and
// the sign-in it was made with.
var pgSessions sync.Map

func pgSessionKey(node model.Node) string {
	return fmt.Sprintf("%d|%s|%s|%s", node.ID, node.Address, node.Username, node.Password)
}

// login signs in as an administrator, for a Pasarguard reached without an
// API key, and keeps the access token it is given.
func (p *pgAPI) login(ctx context.Context) error {
	form := url.Values{"username": {p.node.Username}, "password": {p.node.Password}, "grant_type": {"password"}}
	ctx, cancel := context.WithTimeout(ctx, pgTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/api/admin/token", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("bad address: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil || out.AccessToken == "" {
		return errors.New("the username or password was refused by the Pasarguard panel")
	}
	pgSessions.Store(pgSessionKey(p.node), out.AccessToken)
	return nil
}

func (p *pgAPI) raw(ctx context.Context, method, target string, body, into any, auth bool) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, pgTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return fmt.Errorf("bad address: %w", err)
	}
	if auth {
		if p.node.Token != "" {
			req.Header.Set("X-Api-Key", p.node.Token)
		} else if tok, ok := pgSessions.Load(pgSessionKey(p.node)); ok {
			req.Header.Set("Authorization", "Bearer "+tok.(string))
		}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errPGRefused
	case resp.StatusCode == http.StatusNotFound && into == nil && method == http.MethodDelete:
		return nil // already gone
	case resp.StatusCode >= 300:
		var e struct {
			Detail any `json:"detail"`
		}
		if json.Unmarshal(data, &e) == nil && e.Detail != nil {
			return fmt.Errorf("it answered %s: %v", resp.Status, e.Detail)
		}
		return fmt.Errorf("it answered %s", resp.Status)
	}
	if into == nil || len(data) == 0 {
		return nil
	}
	if s, ok := into.(*string); ok {
		*s = string(data)
		return nil
	}
	if err := json.Unmarshal(data, into); err != nil {
		return errors.New("it answered, but not like a Pasarguard panel")
	}
	return nil
}

type pgSystem struct {
	Version  string  `json:"version"`
	Uptime   int64   `json:"uptime_seconds"`
	MemTotal uint64  `json:"mem_total"`
	MemUsed  uint64  `json:"mem_used"`
	CPU      float64 `json:"cpu_usage"`
}

func (p *pgAPI) system(ctx context.Context) (pgSystem, error) {
	var s pgSystem
	err := p.do(ctx, http.MethodGet, "/api/system", nil, &s)
	return s, err
}

type pgNode struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (p *pgAPI) nodes(ctx context.Context) ([]pgNode, error) {
	var out struct {
		Nodes []pgNode `json:"nodes"`
	}
	err := p.do(ctx, http.MethodGet, "/api/nodes", nil, &out)
	return out.Nodes, err
}

// pgInbound is an inbound as a core's xray config has it.
type pgInbound struct {
	Tag      string          `json:"tag"`
	Port     json.RawMessage `json:"port"`
	Protocol string          `json:"protocol"`
	Settings struct {
		Method string `json:"method"`
	} `json:"settings"`
	Stream struct {
		Network  string `json:"network"`
		Security string `json:"security"`
	} `json:"streamSettings"`
}

// portOf reads an inbound's port, which xray takes as a number or a string.
func (i pgInbound) portOf() int {
	var n int
	if json.Unmarshal(i.Port, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(i.Port, &s) == nil {
		fmt.Sscanf(s, "%d", &n)
	}
	return n
}

func (p *pgAPI) inbounds(ctx context.Context) ([]pgInbound, error) {
	var cores struct {
		Cores []struct {
			Config struct {
				Inbounds []pgInbound `json:"inbounds"`
			} `json:"config"`
		} `json:"cores"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/cores", nil, &cores); err != nil {
		return nil, err
	}
	var out []pgInbound
	seen := map[string]bool{}
	for _, c := range cores.Cores {
		for _, in := range c.Config.Inbounds {
			if in.Tag == "" || seen[in.Tag] {
				continue
			}
			seen[in.Tag] = true
			out = append(out, in)
		}
	}
	return out, nil
}

type pgGroup struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	InboundTags []string `json:"inbound_tags"`
}

func (p *pgAPI) groups(ctx context.Context) ([]pgGroup, error) {
	var out struct {
		Groups []pgGroup `json:"groups"`
	}
	err := p.do(ctx, http.MethodGet, "/api/groups", nil, &out)
	return out.Groups, err
}

func (p *pgAPI) createGroup(ctx context.Context, name, tag string) (pgGroup, error) {
	var g pgGroup
	err := p.do(ctx, http.MethodPost, "/api/group", map[string]any{"name": name, "inbound_tags": []string{tag}}, &g)
	return g, err
}

type pgUser struct {
	ID              int    `json:"id"`
	Username        string `json:"username"`
	Status          string `json:"status"`
	UsedTraffic     uint64 `json:"used_traffic"`
	GroupIDs        []int  `json:"group_ids"`
	SubscriptionURL string `json:"subscription_url"`
	// What a user brought in from that panel has of its own.
	DataLimit            *uint64        `json:"data_limit"`
	Expire               any            `json:"expire"`
	OnHoldExpireDuration int64          `json:"on_hold_expire_duration"`
	Note                 string         `json:"note"`
	ProxySettings        map[string]any `json:"proxy_settings"`
}

// ourUsers is every user this panel made there, read a page at a time.
func (p *pgAPI) ourUsers(ctx context.Context) ([]pgUser, error) {
	return p.users(ctx, "wui_")
}

// users are the users whose names hold search, every one when it is empty,
// read a page at a time.
func (p *pgAPI) users(ctx context.Context, search string) ([]pgUser, error) {
	var all []pgUser
	const page = 500
	for offset := 0; ; offset += page {
		var out struct {
			Users []pgUser `json:"users"`
			Total int      `json:"total"`
		}
		q := url.Values{"offset": {fmt.Sprint(offset)}, "limit": {fmt.Sprint(page)}}
		if search != "" {
			q.Set("search", search)
		}
		if err := p.do(ctx, http.MethodGet, "/api/users?"+q.Encode(), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Users...)
		if len(out.Users) < page || offset+page >= out.Total {
			return all, nil
		}
	}
}

func (p *pgAPI) createUser(ctx context.Context, username, status, note string, group int) (pgUser, error) {
	var u pgUser
	err := p.do(ctx, http.MethodPost, "/api/user", map[string]any{
		"username": username, "status": status, "group_ids": []int{group},
		"proxy_settings": map[string]any{}, "data_limit": 0, "expire": nil, "note": note,
	}, &u)
	return u, err
}

// user reads one user, with what the list leaves out: its subscription link.
func (p *pgAPI) user(ctx context.Context, username string) (pgUser, error) {
	var u pgUser
	err := p.do(ctx, http.MethodGet, "/api/user/"+url.PathEscape(username), nil, &u)
	return u, err
}

func (p *pgAPI) modifyUser(ctx context.Context, username, status string, group int) error {
	return p.do(ctx, http.MethodPut, "/api/user/"+url.PathEscape(username), map[string]any{
		"status": status, "group_ids": []int{group},
	}, nil)
}

// usersNamed are the users of these names, asked for by name a few dozen at
// a time rather than by reading every user there, which a panel of thousands
// would answer with all of them every round. A panel that does not filter by
// name answers with more, and only the ones named are kept.
func (p *pgAPI) usersNamed(ctx context.Context, names []string) ([]pgUser, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []pgUser
	const chunk = 50
	for i := 0; i < len(names); i += chunk {
		part := names[i:min(i+chunk, len(names))]
		q := url.Values{"username": part, "limit": {fmt.Sprint(len(part))}}
		var page struct {
			Users []pgUser `json:"users"`
		}
		if err := p.do(ctx, http.MethodGet, "/api/users?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, u := range page.Users {
			if want[u.Username] {
				out = append(out, u)
				delete(want, u.Username)
			}
		}
	}
	return out, nil
}

// releaseUser switches a brought-in user and clears its own allowance and
// expiry, which are this panel's now; its groups are left as they are.
func (p *pgAPI) releaseUser(ctx context.Context, username, status string) error {
	return p.do(ctx, http.MethodPut, "/api/user/"+url.PathEscape(username), map[string]any{
		"status": status, "data_limit": 0, "expire": 0,
	}, nil)
}

func (p *pgAPI) deleteUser(ctx context.Context, username string) error {
	return p.do(ctx, http.MethodDelete, "/api/user/"+url.PathEscape(username), nil, nil)
}

// links are a user's links, from their subscription in its links form.
func (p *pgAPI) links(ctx context.Context, subURL string) ([]string, error) {
	if subURL == "" {
		return nil, errors.New("it gave no subscription link")
	}
	if strings.HasPrefix(subURL, "/") {
		subURL = p.base + subURL
	}
	var body string
	if err := p.raw(ctx, http.MethodGet, strings.TrimRight(subURL, "/")+"/links", nil, &body, false); err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); strings.Contains(l, "://") {
			out = append(out, l)
		}
	}
	return out, nil
}
