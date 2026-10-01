package api

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/scope"
	"github.com/abolfazl/w-ui/internal/service"
)

var updateRoutes = flag.Bool("update", false, "rewrite testdata/routes.golden")

const routesGolden = "testdata/routes.golden"

// The whole API, pinned: every route with its method, path, heading, the two
// flags that decide its gates, and who it actually lets in -- found by
// asking it, through the same gates the panel puts in front of it, as each
// kind of caller in turn. Adding a route, removing one, or changing who may
// reach one fails here until the golden file is regenerated, so a change to
// the API is always one somebody chose and a reviewer sees:
//
//	go test ./internal/api -run TestTheRouteTable -update
func TestTheRouteTable(t *testing.T) {
	s, callers := routeTableServer(t)

	// Sessions come from the panel's own sign-in.
	login := http.NewServeMux()
	s.register(login)
	sessions := signIn(t, login, callers)

	// Then every route, behind the real gates, in front of a handler that
	// only says it was reached: nothing is restarted, deleted or sent while
	// every caller tries every route.
	routes := s.routes()
	for i := range routes {
		routes[i].handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Reached", "1")
			w.WriteHeader(http.StatusOK)
		}
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux, routes)

	order := []string{"none", "token", "reseller", "admin", "owner"}
	var b bytes.Buffer
	b.WriteString("# Every route, and who it lets in. Regenerate with:\n")
	b.WriteString("#   go test ./internal/api -run TestTheRouteTable -update\n")
	b.WriteString("# yes = reached; 401 = not signed in; 403 = refused by a gate.\n")
	b.WriteString("# A reseller reaches what they reach over their own customers alone.\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "%-6s %-46s %-14s auth=%-5v operator=%-5v |", r.Method, r.Path, r.Group, r.Auth, r.Operator)
		for _, who := range order {
			fmt.Fprintf(&b, " %s=%s", who, reach(mux, r, sessions[who]))
		}
		b.WriteString("\n")
	}

	if *updateRoutes {
		if err := os.MkdirAll(filepath.Dir(routesGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(routesGolden, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(routesGolden)
	if err != nil {
		t.Fatalf("no golden file; run with -update to write it: %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b.Bytes()) {
		t.Errorf("the API changed; if that is intended, regenerate %s and review the diff:\n%s",
			routesGolden, lineDiff(string(want), b.String()))
	}
}

// reach is what one caller gets from one route: yes when the handler ran,
// otherwise the status a gate answered with.
func reach(mux http.Handler, r Route, sess session) string {
	path := strings.NewReplacer("{id}", "1", "{name}", "x", "{gid}", "1").Replace(r.Path)
	req := httptest.NewRequest(r.Method, path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if sess.token != "" {
		req.Header.Set("Authorization", "Bearer "+sess.token)
	}
	for _, c := range sess.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Header().Get("X-Reached") == "1" {
		return "yes"
	}
	return fmt.Sprint(rec.Code)
}

type session struct {
	token   string
	cookies []*http.Cookie
}

// routeTableServer is a whole panel's API over a real schema, with one
// operator of each role and a machine token.
func routeTableServer(t *testing.T) (*Server, map[string]string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wui.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	if err := scope.Register(db); err != nil {
		t.Fatal(err)
	}
	catalog, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Catalog: catalog,
		Enforcer: enforce.NewNoop(), Settings: service.NewSettings(db, "en"),
		JWTSecret: []byte("route-table-secret-0123456789abcd"), Version: "test",
	})
	hash, err := bcrypt.GenerateFromPassword([]byte("route-table-pass-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []model.Admin{
		{Username: "owner", Role: model.RoleOwner},
		{Username: "admin", Role: model.RoleAdmin},
		{Username: "reseller", Role: model.RoleReseller},
	} {
		a.PasswordHash = string(hash)
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	issued, err := s.nodes.IssueToken(context.Background(), "route table")
	if err != nil {
		t.Fatal(err)
	}
	return s, map[string]string{"token": issued.Token}
}

// signIn opens a session for each operator through the panel's own sign-in,
// and adds the machine token and the caller with nothing.
func signIn(t *testing.T, mux http.Handler, callers map[string]string) map[string]session {
	t.Helper()
	out := map[string]session{"none": {}, "token": {token: callers["token"]}}
	for _, who := range []string{"owner", "admin", "reseller"} {
		body, _ := json.Marshal(map[string]string{"username": who, "password": "route-table-pass-1"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var got struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Token == "" {
			t.Fatalf("%s could not sign in: %d %s", who, rec.Code, rec.Body)
		}
		out[who] = session{token: got.Token, cookies: rec.Result().Cookies()}
	}
	return out
}

// lineDiff lists the lines one text has and the other does not.
func lineDiff(want, got string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
			m[l] = true
		}
		return m
	}
	w, g := in(want), in(got)
	var out []string
	for _, l := range strings.Split(want, "\n") {
		if !g[strings.TrimSuffix(l, "\r")] {
			out = append(out, "  - "+l)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !w[l] {
			out = append(out, "  + "+l)
		}
	}
	return strings.Join(out, "\n")
}
