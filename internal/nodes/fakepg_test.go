package nodes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A Pasarguard panel with one node of its own, as far as this panel speaks
// to it, kept in memory: one core's inbounds, groups of inbounds, users in
// groups, hosts, and each user's subscription. Signed in by an API key or an
// administrator's password.
//
// It keeps one thing of its node's that matters here: a Shadowsocks-2022
// inbound started with no user is run by xray as a one-key server, which
// takes no user later -- and the node's sync of every user then fails.

type fakePGUser struct {
	ID        int
	Username  string
	Status    string
	Groups    []int
	DataLimit uint64
	Expire    *time.Time
	OnHold    int64 // on_hold_expire_duration, seconds
	Used      uint64
	Note      string
	Password  string // its proxies' credential, made by the panel
}

type fakePG struct {
	t      *testing.T
	mu     sync.Mutex
	apiKey string
	user   string
	pass   string
	token  string

	inbounds []map[string]any
	groups   map[int]*pgGroup
	users    map[string]*fakePGUser
	hosts    []map[string]any
	nextID   int
	restarts int
	// singleKey are the Shadowsocks-2022 inbounds its node started with no
	// user; syncBroken that a user was given one since.
	singleKey  map[string]bool
	syncBroken bool
	// everyUser counts the listings of every user it was asked for.
	everyUser int
	linkReads int // the subscriptions read
	srv       *httptest.Server
}

func newFakePG(t *testing.T) *fakePG {
	t.Helper()
	f := &fakePG{t: t, apiKey: "pg_key_test", user: "pgadmin", pass: "pw", token: "jwt-test",
		groups: map[int]*pgGroup{}, users: map[string]*fakePGUser{}, nextID: 1, singleKey: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePG) address() string { return f.srv.URL }

func (f *fakePG) id() int { f.nextID++; return f.nextID }

// addInbound puts an inbound in its core as its operator would.
func (f *fakePG) addInbound(in map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inbounds = append(f.inbounds, in)
	f.restartNode()
}

// addGroup and addUser are its operator's own.
func (f *fakePG) addGroup(name string, tags ...string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := &pgGroup{ID: f.id(), Name: name, InboundTags: tags}
	f.groups[g.ID] = g
	return g.ID
}

func (f *fakePG) addUser(u fakePGUser) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u.ID = f.id()
	if u.Password == "" {
		u.Password = fmt.Sprintf("pw-%s", u.Username)
	}
	f.users[u.Username] = &u
}

func (f *fakePG) get(name string) *fakePGUser {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[name]; ok {
		c := *u
		return &c
	}
	return nil
}

// restartNode is its node started again with the core as it is: a
// Shadowsocks-2022 inbound that no active user reaches comes up one-key.
func (f *fakePG) restartNode() {
	f.restarts++
	f.singleKey = map[string]bool{}
	for _, in := range f.inbounds {
		set, _ := in["settings"].(map[string]any)
		method, _ := set["method"].(string)
		if in["protocol"] == "shadowsocks" && strings.HasPrefix(method, "2022-") && !f.reached(fmt.Sprint(in["tag"])) {
			f.singleKey[fmt.Sprint(in["tag"])] = true
		}
	}
}

func (f *fakePG) reached(tag string) bool {
	for _, u := range f.users {
		if u.Status != "active" {
			continue
		}
		for _, gid := range u.Groups {
			if g := f.groups[gid]; g != nil && containsTag(g.InboundTags, tag) {
				return true
			}
		}
	}
	return false
}

// synced is a user handed to its node: into a one-key inbound, it breaks
// the sync of everyone.
func (f *fakePG) synced(u *fakePGUser) {
	for _, gid := range u.Groups {
		if g := f.groups[gid]; g != nil {
			for _, tag := range g.InboundTags {
				if f.singleKey[tag] {
					f.syncBroken = true
				}
			}
		}
	}
}

func (f *fakePG) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/sub/") {
		f.serveSub(w, r)
		return
	}
	if r.URL.Path == "/api/admin/token" {
		_ = r.ParseForm()
		if r.PostForm.Get("username") != f.user || r.PostForm.Get("password") != f.pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSONBody(w, map[string]any{"access_token": f.token, "token_type": "bearer"})
		return
	}
	if r.Header.Get("X-Api-Key") != f.apiKey && r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	switch {
	case path == "/api/system":
		writeJSONBody(w, map[string]any{"version": "5.4.1", "uptime_seconds": 60, "mem_total": 4, "mem_used": 1, "cpu_usage": 2.0})
	case path == "/api/nodes":
		writeJSONBody(w, map[string]any{"nodes": []any{map[string]any{"name": "foreign", "status": "connected"}}})
	case path == "/api/cores":
		writeJSONBody(w, map[string]any{"cores": []any{f.core()}})
	case path == "/api/core/1" && r.Method == http.MethodGet:
		writeJSONBody(w, f.core())
	case path == "/api/core/1" && r.Method == http.MethodPut:
		var in struct {
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(body, &in)
		list, _ := in.Config["inbounds"].([]any)
		f.inbounds = nil
		for _, x := range list {
			f.inbounds = append(f.inbounds, x.(map[string]any))
		}
		if r.URL.Query().Get("restart_nodes") == "true" {
			f.restartNode()
		}
		writeJSONBody(w, f.core())
	case path == "/api/groups":
		var gs []pgGroup
		for _, g := range f.groups {
			gs = append(gs, *g)
		}
		sort.Slice(gs, func(i, j int) bool { return gs[i].ID < gs[j].ID })
		writeJSONBody(w, map[string]any{"groups": gs, "total": len(gs)})
	case path == "/api/group" && r.Method == http.MethodPost:
		var g pgGroup
		_ = json.Unmarshal(body, &g)
		g.ID = f.id()
		f.groups[g.ID] = &g
		writeJSONBody(w, g)
	case path == "/api/hosts":
		list := make([]any, len(f.hosts))
		for i, h := range f.hosts {
			list[i] = h
		}
		writeJSONBody(w, list)
	case path == "/api/host/" && r.Method == http.MethodPost:
		var h map[string]any
		_ = json.Unmarshal(body, &h)
		f.hosts = append(f.hosts, h)
		writeJSONBody(w, h)
	case path == "/api/users":
		f.serveUsers(w, r.URL.Query())
	case path == "/api/user" && r.Method == http.MethodPost:
		var in struct {
			Username string `json:"username"`
			Status   string `json:"status"`
			GroupIDs []int  `json:"group_ids"`
			Note     string `json:"note"`
		}
		_ = json.Unmarshal(body, &in)
		if _, ok := f.users[in.Username]; ok {
			w.WriteHeader(http.StatusConflict)
			writeJSONBody(w, map[string]any{"detail": "User already exists"})
			return
		}
		u := &fakePGUser{ID: f.id(), Username: in.Username, Status: in.Status, Groups: in.GroupIDs, Note: in.Note, Password: "pw-" + in.Username}
		f.users[u.Username] = u
		if u.Status == "active" {
			f.synced(u)
		}
		writeJSONBody(w, f.userJSON(u))
	case strings.HasPrefix(path, "/api/user/"):
		name, _ := url.PathUnescape(strings.TrimPrefix(path, "/api/user/"))
		u, ok := f.users[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			writeJSONBody(w, map[string]any{"detail": "User not found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSONBody(w, f.userJSON(u))
		case http.MethodDelete:
			delete(f.users, name)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPut:
			f.modify(u, body)
			if u.Status == "active" {
				f.synced(u)
			}
			writeJSONBody(w, f.userJSON(u))
		}
	default:
		http.NotFound(w, r)
	}
}

// modify is Pasarguard's own modify: only what is sent changes; a data
// limit and an expiry of 0 are none.
func (f *fakePG) modify(u *fakePGUser, body []byte) {
	var in map[string]json.RawMessage
	_ = json.Unmarshal(body, &in)
	if v, ok := in["status"]; ok {
		_ = json.Unmarshal(v, &u.Status)
	}
	if v, ok := in["group_ids"]; ok {
		_ = json.Unmarshal(v, &u.Groups)
	}
	if v, ok := in["data_limit"]; ok {
		_ = json.Unmarshal(v, &u.DataLimit)
	}
	if v, ok := in["expire"]; ok {
		var n int64
		if json.Unmarshal(v, &n) == nil && n == 0 {
			u.Expire = nil
		}
	}
}

func (f *fakePG) serveUsers(w http.ResponseWriter, q url.Values) {
	named := map[string]bool{}
	for _, n := range q["username"] {
		named[n] = true
	}
	if q.Get("search") == "" && len(named) == 0 {
		f.everyUser++
	}
	var names []string
	for n := range f.users {
		if s := q.Get("search"); s != "" && !strings.Contains(n, s) {
			continue
		}
		if len(named) > 0 && !named[n] {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	total := len(names)
	if offset > len(names) {
		offset = len(names)
	}
	names = names[offset:]
	if limit > 0 && limit < len(names) {
		names = names[:limit]
	}
	var out []any
	for _, n := range names {
		j := f.userJSON(f.users[n])
		delete(j, "subscription_url") // the list leaves it out
		out = append(out, j)
	}
	writeJSONBody(w, map[string]any{"users": out, "total": total})
}

func (f *fakePG) userJSON(u *fakePGUser) map[string]any {
	j := map[string]any{"id": u.ID, "username": u.Username, "status": u.Status, "used_traffic": u.Used,
		"group_ids": u.Groups, "note": u.Note, "on_hold_expire_duration": u.OnHold,
		"subscription_url": f.srv.URL + "/sub/" + u.Username,
		"proxy_settings":   map[string]any{"vless": map[string]any{"id": u.Password}, "shadowsocks": map[string]any{"password": u.Password}}}
	if u.DataLimit > 0 {
		j["data_limit"] = u.DataLimit
	} else {
		j["data_limit"] = nil
	}
	if u.Expire != nil {
		j["expire"] = u.Expire.Format("2006-01-02T15:04:05")
	} else {
		j["expire"] = nil
	}
	return j
}

func (f *fakePG) core() map[string]any {
	list := make([]any, len(f.inbounds))
	for i, in := range f.inbounds {
		list[i] = in
	}
	return map[string]any{"id": 1, "name": "core", "config": map[string]any{"inbounds": list},
		"exclude_inbound_tags": []any{}, "fallbacks_inbound_tags": []any{}}
}

// serveSub is a user's links: one for every inbound its groups reach.
func (f *fakePG) serveSub(w http.ResponseWriter, r *http.Request) {
	f.linkReads++
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sub/"), "/links")
	u, ok := f.users[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	var lines []string
	for _, gid := range u.Groups {
		if g := f.groups[gid]; g != nil {
			for _, tag := range g.InboundTags {
				lines = append(lines, fmt.Sprintf("vless://%s@%s:443#%s", u.Password, f.hostOf(tag), url.PathEscape(u.Username+" "+tag)))
			}
		}
	}
	io.WriteString(w, strings.Join(lines, "\n"))
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// hostOf is the address the host of an inbound gives its links.
func (f *fakePG) hostOf(tag string) string {
	for _, h := range f.hosts {
		if h["inbound_tag"] == tag {
			if a, ok := h["address"].([]any); ok && len(a) > 0 {
				return fmt.Sprint(a[0])
			}
		}
	}
	return "127.0.0.1"
}
