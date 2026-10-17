package nodes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/abolfazl/w-ui/internal/service"
)

// A 3x-ui panel, as far as this panel speaks to it, kept in memory: its
// inbounds and their clients, its sign-in, and the API of the generation its
// version says -- 2.x signed in by password, the API under /panel/api and the
// links from its subscription service; 3.0 with a bearer token; 3.10 with the
// clients API and a CSRF token on every signed-in change. What it refuses it
// refuses as the real one does, so the rounds are tested against the answers
// they will get.

const fakeXUIPath = "/secret/"

type fakeXUIInbound struct {
	ID       int
	Remark   string
	Protocol string
	Port     int
	Clients  []map[string]any
	Extra    map[string]any // the rest of its settings: method, decryption
	Stream   map[string]any
	Stats    map[string][2]uint64 // email -> up, down
}

type fakeXUI struct {
	t       *testing.T
	mu      sync.Mutex
	version string
	token   string
	user    string
	pass    string

	inbounds []*fakeXUIInbound
	nextID   int
	xray     map[string]any // the xray config it runs
	// xrayFails says xray will not run with a config, as a real one may not.
	xrayFails func(cfg map[string]any) bool
	// inboundFails says xray will not run with these inbounds.
	inboundFails func(ins []*fakeXUIInbound) bool
	restarts     int
	logins       int
	// linkReads counts the links it was asked for, by API or subscription.
	linkReads int
	// sessions are the cookies it gave, signed in or not, and each one's
	// CSRF token.
	sessions map[string]*fakeXUISession
	srv      *httptest.Server
}

type fakeXUISession struct {
	in   bool
	csrf string
}

func newFakeXUI(t *testing.T, version string) *fakeXUI {
	t.Helper()
	f := &fakeXUI{t: t, version: version, token: "tok-" + version, user: "admin", pass: "pw",
		nextID: 1, sessions: map[string]*fakeXUISession{},
		xray: map[string]any{
			"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
			"routing":   map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []any{"api"}, "outboundTag": "api"}}},
		}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeXUI) address() string { return f.srv.URL + fakeXUIPath }

func (f *fakeXUI) atLeast(major, minor int) bool {
	return !service.VersionBelow(f.version, major, minor)
}

// addInbound puts an inbound there as its own operator would.
func (f *fakeXUI) addInbound(remark, protocol string, port int, extra, stream map[string]any) *fakeXUIInbound {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := &fakeXUIInbound{ID: f.nextID, Remark: remark, Protocol: protocol, Port: port, Extra: extra, Stream: stream, Stats: map[string][2]uint64{}}
	if in.Extra == nil {
		in.Extra = map[string]any{}
	}
	if in.Stream == nil {
		in.Stream = map[string]any{"network": "tcp", "security": "none"}
	}
	f.nextID++
	f.inbounds = append(f.inbounds, in)
	return in
}

// addOwnClient is a client its own operator made.
func (f *fakeXUI) addOwnClient(inboundID int, c map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.inbound(inboundID)
	in.Clients = append(in.Clients, c)
}

// client is a client there by inbound and email, as it stands.
func (f *fakeXUI) client(inboundID int, email string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.inbound(inboundID).Clients {
		if c["email"] == email {
			return c
		}
	}
	return nil
}

// emails are the clients on an inbound.
func (f *fakeXUI) emails(inboundID int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.inbound(inboundID).Clients {
		out = append(out, fmt.Sprint(c["email"]))
	}
	return out
}

// count is traffic a client moved there.
func (f *fakeXUI) count(inboundID int, email string, up, down uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.inbound(inboundID)
	s := in.Stats[email]
	in.Stats[email] = [2]uint64{s[0] + up, s[1] + down}
}

func (f *fakeXUI) inbound(id int) *fakeXUIInbound {
	for _, in := range f.inbounds {
		if in.ID == id {
			return in
		}
	}
	f.t.Fatalf("fake 3x-ui: no inbound %d", id)
	return nil
}

func (f *fakeXUI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := strings.CutPrefix(r.URL.Path, fakeXUIPath)
	if !ok {
		// Anywhere but its secret path it answers nothing, as 3x-ui does.
		http.NotFound(w, r)
		return
	}
	if sub, ok := strings.CutPrefix(path, "sub/"); ok {
		f.serveSub(w, sub)
		return
	}
	sess := f.session(w, r)
	switch path {
	case "csrf-token":
		if !f.atLeast(3, 10) {
			http.NotFound(w, r)
			return
		}
		ok200(w, sess.csrf)
		return
	case "login":
		if f.atLeast(3, 10) && r.Header.Get("X-CSRF-Token") != sess.csrf {
			w.WriteHeader(http.StatusForbidden) // an empty 403, as its middleware gives
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("username") != f.user || r.PostForm.Get("password") != f.pass {
			writeJSONBody(w, map[string]any{"success": false, "msg": "wrong username or password"})
			return
		}
		sess.in = true
		sess.csrf = fmt.Sprintf("csrf-%d", len(f.sessions)+100) // a new session's own token
		f.logins++
		ok200(w, nil)
		return
	case "panel/":
		if !sess.in {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<html><link href="/assets/css/custom.min.css?%s" rel="stylesheet"></html>`, f.version)
		return
	}

	bearer := f.atLeast(3, 0) && r.Header.Get("Authorization") == "Bearer "+f.token
	if !bearer && !sess.in {
		// Unsigned, its API answers 404 like everything outside it.
		http.NotFound(w, r)
		return
	}
	if !bearer && f.atLeast(3, 10) && r.Method != http.MethodGet && r.Header.Get("X-CSRF-Token") != sess.csrf {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// The old server routes, before the API had them (before 2.8).
	if !f.atLeast(2, 8) {
		switch path {
		case "server/status":
			f.serveStatus(w)
			return
		case "server/restartXrayService":
			f.restarts++
			ok200(w, nil)
			return
		}
	}
	// Its xray settings: under /panel before 3.3, signed in.
	if !f.atLeast(3, 3) && (path == "panel/xray/" || path == "panel/xray/update") {
		if !sess.in {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.serveXray(w, body, path == "panel/xray/update", true)
		return
	}
	if path == "panel/setting/all" {
		ok200(w, map[string]any{"subEnable": true, "subURI": f.srv.URL + fakeXUIPath + "sub/"})
		return
	}
	api, ok := strings.CutPrefix(path, "panel/api/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.serveAPI(w, r, api, body)
}

func (f *fakeXUI) session(w http.ResponseWriter, r *http.Request) *fakeXUISession {
	if c, err := r.Cookie("3x-ui"); err == nil {
		if s, ok := f.sessions[c.Value]; ok {
			return s
		}
	}
	id := fmt.Sprintf("s%d", len(f.sessions)+1)
	s := &fakeXUISession{csrf: "csrf-" + id}
	f.sessions[id] = s
	http.SetCookie(w, &http.Cookie{Name: "3x-ui", Value: id, Path: fakeXUIPath})
	return s
}

func (f *fakeXUI) serveStatus(w http.ResponseWriter) {
	xray := map[string]any{"state": "running"}
	if f.xrayFails != nil && f.xrayFails(f.xray) || f.inboundFails != nil && f.inboundFails(f.inbounds) {
		xray = map[string]any{"state": "error", "errorMsg": "failed to start: bad outbound"}
	}
	st := map[string]any{"cpu": 1.5, "mem": map[string]any{"current": 1, "total": 4}, "xray": xray, "uptime": 60}
	if f.atLeast(3, 0) {
		st["panelVersion"] = f.version
	}
	ok200(w, st)
}

func (f *fakeXUI) serveAPI(w http.ResponseWriter, r *http.Request, api string, body []byte) {
	switch {
	case api == "server/status" && f.atLeast(2, 8):
		f.serveStatus(w)
	case api == "server/restartXrayService" && f.atLeast(2, 8):
		f.restarts++
		ok200(w, nil)
	case (api == "xray/" || api == "xray/update") && f.atLeast(3, 3):
		f.serveXray(w, body, api == "xray/update", false)
	case api == "inbounds/add":
		var in struct {
			Remark   string `json:"remark"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Settings string `json:"settings"`
			Stream   string `json:"streamSettings"`
		}
		_ = json.Unmarshal(body, &in)
		for _, have := range f.inbounds {
			if have.Port == in.Port {
				fail200(w, "Port already exists")
				return
			}
		}
		var set, st map[string]any
		_ = json.Unmarshal([]byte(in.Settings), &set)
		_ = json.Unmarshal([]byte(in.Stream), &st)
		made := &fakeXUIInbound{ID: f.nextID, Remark: in.Remark, Protocol: in.Protocol, Port: in.Port, Extra: set, Stream: st, Stats: map[string][2]uint64{}}
		f.nextID++
		f.inbounds = append(f.inbounds, made)
		ok200(w, map[string]any{"id": made.ID, "tag": fmt.Sprintf("inbound-%d", made.Port)})
	case strings.HasPrefix(api, "inbounds/del/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(api, "inbounds/del/"))
		kept := f.inbounds[:0]
		for _, in := range f.inbounds {
			if in.ID != id {
				kept = append(kept, in)
			}
		}
		f.inbounds = kept
		ok200(w, nil)
	case api == "inbounds/list":
		f.serveList(w)
	case api == "inbounds/addClient" && !f.atLeast(3, 1):
		id, clients := f.settingsBody(r, body)
		in := f.inbound(id)
		for _, c := range clients {
			if f.emailTaken(fmt.Sprint(c["email"])) {
				fail200(w, "Duplicate email: "+fmt.Sprint(c["email"]))
				return
			}
			in.Clients = append(in.Clients, c)
		}
		ok200(w, nil)
	case strings.HasPrefix(api, "inbounds/updateClient/") && !f.atLeast(3, 1):
		key := strings.TrimPrefix(api, "inbounds/updateClient/")
		id, clients := f.settingsBody(r, body)
		in := f.inbound(id)
		for i, c := range in.Clients {
			if credential(in.Protocol, c) == key {
				in.Clients[i] = clients[0]
				ok200(w, nil)
				return
			}
		}
		fail200(w, "client not found")
	case strings.Contains(api, "/delClient") && !f.atLeast(3, 1):
		parts := strings.Split(api, "/") // inbounds/{id}/delClient[ByEmail]/{key}
		id, _ := strconv.Atoi(parts[1])
		in := f.inbound(id)
		byEmail := parts[2] == "delClientByEmail"
		if byEmail != f.atLeast(2, 8) {
			http.NotFound(w, r)
			return
		}
		for i, c := range in.Clients {
			if byEmail && c["email"] == parts[3] || !byEmail && credential(in.Protocol, c) == parts[3] {
				if len(in.Clients) == 1 {
					// 3x-ui 2 keeps a client on every inbound.
					fail200(w, "no client remained in Inbound")
					return
				}
				in.Clients = append(in.Clients[:i], in.Clients[i+1:]...)
				ok200(w, nil)
				return
			}
		}
		fail200(w, "client not found")
	case api == "clients/list" && f.atLeast(3, 1):
		ok200(w, []any{})
	case api == "clients/add" && f.atLeast(3, 1):
		var in struct {
			Client     map[string]any `json:"client"`
			InboundIDs []int          `json:"inboundIds"`
		}
		_ = json.Unmarshal(body, &in)
		if f.emailTaken(fmt.Sprint(in.Client["email"])) {
			fail200(w, "Duplicate email")
			return
		}
		for _, id := range in.InboundIDs {
			f.inbound(id).Clients = append(f.inbound(id).Clients, copyMap(in.Client))
		}
		ok200(w, nil)
	case strings.HasPrefix(api, "clients/update/") && f.atLeast(3, 1):
		email := strings.TrimPrefix(api, "clients/update/")
		var c map[string]any
		_ = json.Unmarshal(body, &c)
		found := false
		for _, in := range f.inbounds {
			for i, have := range in.Clients {
				if have["email"] == email {
					in.Clients[i] = copyMap(c)
					found = true
				}
			}
		}
		if !found {
			fail200(w, "client not found")
			return
		}
		ok200(w, nil)
	case strings.HasPrefix(api, "clients/del/") && f.atLeast(3, 1):
		email := strings.TrimPrefix(api, "clients/del/")
		for _, in := range f.inbounds {
			kept := in.Clients[:0]
			for _, c := range in.Clients {
				if c["email"] != email {
					kept = append(kept, c)
				}
			}
			in.Clients = kept
		}
		ok200(w, nil)
	case strings.HasPrefix(api, "clients/links/") && f.atLeast(3, 1):
		f.linkReads++
		email := strings.TrimPrefix(api, "clients/links/")
		var out []string
		for _, in := range f.inbounds {
			for _, c := range in.Clients {
				if c["email"] == email {
					out = append(out, fakeXUILink(in, c))
				}
			}
		}
		ok200(w, out)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeXUI) serveList(w http.ResponseWriter) {
	rows := []map[string]any{}
	for _, in := range f.inbounds {
		set := copyMap(in.Extra)
		set["clients"] = in.Clients
		var stats []map[string]any
		for _, c := range in.Clients {
			e := fmt.Sprint(c["email"])
			s := in.Stats[e]
			stats = append(stats, map[string]any{"email": e, "enable": c["enable"], "up": s[0], "down": s[1]})
		}
		row := map[string]any{"id": in.ID, "remark": in.Remark, "enable": true, "port": in.Port, "protocol": in.Protocol,
			"tag": fmt.Sprintf("inbound-%d", in.Port), "clientStats": stats}
		// Objects from 3x-ui 3, JSON in a string before it.
		if f.atLeast(3, 0) {
			row["settings"], row["streamSettings"] = set, in.Stream
		} else {
			s, _ := json.Marshal(set)
			st, _ := json.Marshal(in.Stream)
			row["settings"], row["streamSettings"] = string(s), string(st)
		}
		rows = append(rows, row)
	}
	ok200(w, rows)
}

// serveXray reads or replaces its xray config; before 3.3 the answer is the
// settings written into a string.
func (f *fakeXUI) serveXray(w http.ResponseWriter, body []byte, update, asString bool) {
	if update {
		form, _ := url.ParseQuery(string(body))
		var cfg map[string]any
		if err := json.Unmarshal([]byte(form.Get("xraySetting")), &cfg); err != nil {
			fail200(w, "bad xray config")
			return
		}
		f.xray = cfg
		ok200(w, nil)
		return
	}
	raw, _ := json.Marshal(f.xray)
	if asString {
		wrapped, _ := json.Marshal(map[string]any{"xraySetting": string(raw)})
		ok200(w, string(wrapped))
		return
	}
	ok200(w, map[string]any{"xraySetting": json.RawMessage(raw)})
}

// xrayConfig is the xray config it runs now.
func (f *fakeXUI) xrayConfig() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return normalJSON(f.xray).(map[string]any)
}

// serveSub is its subscription service: every client with that id's links.
func (f *fakeXUI) serveSub(w http.ResponseWriter, subID string) {
	f.linkReads++
	var out []string
	for _, in := range f.inbounds {
		for _, c := range in.Clients {
			if c["subId"] == subID {
				out = append(out, fakeXUILink(in, c))
			}
		}
	}
	if len(out) == 0 {
		http.NotFound(w, nil)
		return
	}
	io.WriteString(w, base64.StdEncoding.EncodeToString([]byte(strings.Join(out, "\n"))))
}

// settingsBody reads the older API's client body: an inbound id and its
// settings in a string, sent as JSON or as a form.
func (f *fakeXUI) settingsBody(r *http.Request, body []byte) (int, []map[string]any) {
	var in struct {
		ID       int    `json:"id"`
		Settings string `json:"settings"`
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(body, &in)
	} else {
		v, _ := url.ParseQuery(string(body))
		in.ID, _ = strconv.Atoi(v.Get("id"))
		in.Settings = v.Get("settings")
	}
	var set struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(in.Settings), &set); err != nil {
		f.t.Errorf("fake 3x-ui: settings not JSON: %q", in.Settings)
	}
	return in.ID, set.Clients
}

func (f *fakeXUI) emailTaken(email string) bool {
	for _, in := range f.inbounds {
		for _, c := range in.Clients {
			if c["email"] == email {
				return true
			}
		}
	}
	return false
}

// credential is what the older API finds a client by.
func credential(protocol string, c map[string]any) string {
	switch protocol {
	case "trojan":
		return fmt.Sprint(c["password"])
	case "shadowsocks":
		return fmt.Sprint(c["email"])
	}
	return fmt.Sprint(c["id"])
}

func fakeXUILink(in *fakeXUIInbound, c map[string]any) string {
	name := url.PathEscape(in.Remark + "-" + fmt.Sprint(c["email"]))
	// What else of the inbound a link carries: its Reality name.
	if sni, ok := in.Stream["sni"].(string); ok {
		name += "-" + sni
	}
	switch in.Protocol {
	case "vmess":
		j, _ := json.Marshal(map[string]any{"v": "2", "ps": in.Remark, "add": "127.0.0.1", "port": in.Port, "id": c["id"]})
		return "vmess://" + base64.StdEncoding.EncodeToString(j)
	case "trojan":
		return fmt.Sprintf("trojan://%s@127.0.0.1:%d#%s", c["password"], in.Port, name)
	}
	return fmt.Sprintf("%s://%s@127.0.0.1:%d#%s", in.Protocol, c["id"], in.Port, name)
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ok200(w http.ResponseWriter, obj any) {
	writeJSONBody(w, map[string]any{"success": true, "msg": "", "obj": obj})
}

func fail200(w http.ResponseWriter, msg string) {
	writeJSONBody(w, map[string]any{"success": false, "msg": msg, "obj": nil})
}

func writeJSONBody(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
