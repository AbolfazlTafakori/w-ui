package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/devguide"
	"github.com/abolfazl/w-ui/internal/endpoints"
	"github.com/abolfazl/w-ui/internal/service"
)

const (
	revealToken = "tok-ali-0123456789"
	revealPSK   = "SharedKey7744QpZm"
	revealUser  = "s1-ali"
	revealPass  = "Pw88xKq2LmNb"
)

// revealServer is a panel with one customer on one L2TP tunnel, and the
// customer's page as a browser gets it.
var registerL2TP sync.Once

func revealServer(t *testing.T) (*Server, string) {
	t.Helper()
	registerL2TP.Do(l2tpdriver.Register)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error)
	iface := model.Interface{Name: "l2tp0", Protocol: model.ProtocolL2TP, ListenPort: 1701, Enabled: true,
		Subnet: "10.77.0.0/24", EndpointHost: "vpn.example.com", NodeID: 1, MTU: 1400,
		L2TP: model.JSON(model.L2TPParams{PSK: revealPSK})}
	must(db.Create(&iface).Error)
	client := model.Client{Name: "Ali", Status: model.StatusActive, SubToken: revealToken}
	must(db.Create(&client).Error)
	must(db.Create(&model.Account{ClientID: client.ID, InterfaceID: iface.ID, NodeID: 1, DeviceName: "device-1",
		IP: "10.77.0.2", Enabled: true, Username: revealUser, Secret: revealPass}).Error)

	s := withDB(&Server{log: log, reveals: newSubReveals(),
		subs: service.NewSubscriptions(db, backend.NewPool(log), endpoints.NewHosts(db, log), log)}, db)

	req := httptest.NewRequest(http.MethodGet, "/sub/"+revealToken, nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	if !s.maybeServeSubPage(rec, req, revealToken) {
		t.Fatal("no page")
	}
	return s, rec.Body.String()
}

// reveal asks the way the page does, with each of its marks changed by edit.
func reveal(s *Server, body map[string]string, edit func(*http.Request)) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/sub/"+revealToken+"?view=reveal", strings.NewReader(string(raw)))
	req.Host = "panel.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wui-Reveal", "1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "https://panel.example.com")
	req.RemoteAddr = "198.51.100.7:4444"
	if edit != nil {
		edit(req)
	}
	rec := httptest.NewRecorder()
	s.maybeServeSubPage(rec, req, revealToken)
	return rec
}

func markOf(t *testing.T, page string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, "var REVEAL = '")
	if !ok {
		t.Fatal("the page carries no mark")
	}
	mark, _, _ := strings.Cut(rest, "'")
	return mark
}

// The page as anything that fetches it gets it: the sign-in is not in it,
// anywhere -- not in a field, a button, a command or the line's text.
func TestThePageCarriesNoSignIn(t *testing.T) {
	_, page := revealServer(t)
	for _, secret := range []string{revealPass, revealPSK, revealUser} {
		if strings.Contains(page, secret) {
			t.Errorf("%s is in the page", secret)
		}
	}
	for _, want := range []string{devguide.Mask, `data-secret="password"`, `data-secret="psk"`, `data-secret="cmd"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %s", want)
		}
	}
}

// The page's own copy button gets each value, and the command with it.
func TestThePageCopiesEachValue(t *testing.T) {
	s, page := revealServer(t)
	mark := markOf(t, page)
	ref := `1.0`
	for what, want := range map[string]string{"password": revealPass, "psk": revealPSK, "username": revealUser} {
		rec := reveal(s, map[string]string{"mark": mark, "ref": ref, "what": what}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", what, rec.Code, rec.Body.String())
		}
		var got struct{ Value string }
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Value != want {
			t.Fatalf("%s: got %q", what, got.Value)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cacheable", what)
		}
	}
	rec := reveal(s, map[string]string{"mark": mark, "ref": ref, "what": "cmd", "device": "mikrotik", "protocol": "l2tp"}, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), revealPass) || !strings.Contains(rec.Body.String(), revealPSK) {
		t.Fatalf("command: %d %s", rec.Code, rec.Body.String())
	}
}

// Anything that is not the page asking is refused: another site, a script
// with no browser's headers, a GET, a wrong or old mark, another link's
// mark, and a device or value that is not there.
func TestOnlyThePageGetsAValue(t *testing.T) {
	s, page := revealServer(t)
	mark := markOf(t, page)
	ask := map[string]string{"mark": mark, "ref": "1.0", "what": "password"}
	cases := map[string]struct {
		body map[string]string
		edit func(*http.Request)
		code int
	}{
		"another site": {ask, func(r *http.Request) {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Origin", "https://evil.example")
		}, http.StatusForbidden},
		"another origin, no fetch metadata": {ask, func(r *http.Request) {
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Set("Origin", "https://evil.example")
		}, http.StatusForbidden},
		"a script without a browser's headers": {ask, func(r *http.Request) {
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Del("Origin")
		}, http.StatusForbidden},
		"no reveal header": {ask, func(r *http.Request) { r.Header.Del("X-Wui-Reveal") }, http.StatusForbidden},
		"a form post":      {ask, func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusForbidden},
		"a GET":            {ask, func(r *http.Request) { r.Method = http.MethodGet }, http.StatusMethodNotAllowed},
		"no mark":          {map[string]string{"ref": "1.0", "what": "password"}, nil, http.StatusConflict},
		"a forged mark":    {map[string]string{"mark": "1700000000.AAAA", "ref": "1.0", "what": "password"}, nil, http.StatusConflict},
		"another link's mark": {map[string]string{"mark": s.reveals.issue("someone-else", time.Now()), "ref": "1.0", "what": "password"},
			nil, http.StatusConflict},
		"an old mark": {map[string]string{"mark": s.reveals.issue(revealToken, time.Now().Add(-revealTTL-time.Minute)), "ref": "1.0", "what": "password"},
			nil, http.StatusConflict},
		"another device":   {map[string]string{"mark": mark, "ref": "9.0", "what": "password"}, nil, http.StatusNotFound},
		"something else":   {map[string]string{"mark": mark, "ref": "1.0", "what": "secret"}, nil, http.StatusNotFound},
		"a command absent": {map[string]string{"mark": mark, "ref": "1.0", "what": "cmd", "device": "windows", "protocol": "l2tp"}, nil, http.StatusNotFound},
	}
	for name, c := range cases {
		rec := reveal(s, c.body, c.edit)
		if rec.Code != c.code {
			t.Errorf("%s: %d, want %d (%s)", name, rec.Code, c.code, strings.TrimSpace(rec.Body.String()))
		}
		if strings.Contains(rec.Body.String(), revealPass) {
			t.Errorf("%s: the password was handed over", name)
		}
	}
}

// Asked over and over, the values stop coming: per link, and per address.
func TestRevealsAreCounted(t *testing.T) {
	s, page := revealServer(t)
	mark := markOf(t, page)
	ok, limited := 0, 0
	for i := 0; i < revealPerLink+5; i++ {
		rec := reveal(s, map[string]string{"mark": mark, "ref": "1.0", "what": "password"}, nil)
		switch rec.Code {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	if ok != revealPerLink || limited != 5 {
		t.Fatalf("%d answered, %d refused; want %d and 5", ok, limited, revealPerLink)
	}
}

// What anyone holding the link fetches -- the raw answer an app gets, and a
// one-file download -- has no sign-in either.
func TestTheLinkHandsOutNoSignIn(t *testing.T) {
	s, _ := revealServer(t)
	for name, path := range map[string]string{
		"raw":      "/sub/" + revealToken + "?view=raw",
		"zip":      "/sub/" + revealToken + "?format=zip",
		"one file": "/sub/" + revealToken + "?device=1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		if s.maybeServeSubDevice(rec, req, revealToken) {
			// served
		} else {
			bundle, err := s.subs.Serve(req.Context(), revealToken, req.URL.Query().Get("format"))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			rec.Body.Write(bundle.Body)
		}
		body := rec.Body.String()
		if body == "" {
			t.Fatalf("%s: nothing served", name)
		}
		for _, secret := range []string{revealPass, revealPSK, revealUser} {
			if strings.Contains(body, secret) {
				t.Errorf("%s: %s is in it", name, secret)
			}
		}
		if name != "zip" && !strings.Contains(body, "Open this link in a browser") {
			t.Errorf("%s: does not say where the sign-in is", name)
		}
	}
}

// Through the router the panel really serves with: the copy button's POST
// arrives, and any other POST to a link -- a cross-site form's -- still does
// not.
func TestTheRouterLetsOnlyTheCopyButtonPost(t *testing.T) {
	s, page := revealServer(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(database.PutSetting(dbOf(s), "sub.enabled", "true"))
	must(database.PutSetting(dbOf(s), "sub.path", "/sub/"))
	reachedPanel := false
	router := s.SubscriptionRouter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedPanel = true
		w.WriteHeader(http.StatusTeapot)
	}))

	raw, _ := json.Marshal(map[string]string{"mark": markOf(t, page), "ref": "1.0", "what": "password"})
	req := httptest.NewRequest(http.MethodPost, "/sub/"+revealToken+"?view=reveal", strings.NewReader(string(raw)))
	req.Host = "panel.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wui-Reveal", "1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if reachedPanel || rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), revealPass) {
		t.Fatalf("the copy button's POST: panel=%v %d %s", reachedPanel, rec.Code, rec.Body.String())
	}

	reachedPanel = false
	form := httptest.NewRequest(http.MethodPost, "/sub/"+revealToken, strings.NewReader("a=b"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, form)
	if !reachedPanel {
		t.Fatalf("another POST reached the link: %d", rec.Code)
	}
}
