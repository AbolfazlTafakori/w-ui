package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/scope"
	"github.com/abolfazl/w-ui/internal/service"
)

// machineServer is a whole panel's API over a real schema, and a machine
// token it issued -- what a managing panel holds for this one as its node.
func machineServer(t *testing.T) (http.Handler, string) {
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Options{
		DB: db, Logger: log, Catalog: catalog, Enforcer: enforce.NewNoop(),
		Settings: service.NewSettings(db, "en"), JWTSecret: []byte("machine-test-secret-0123456789ab"),
		Version: "test",
	})
	// Secrets a token must never be handed, stored as the settings page does.
	for k, v := range map[string]string{
		"notify.botToken": "123456:secret-bot-token", "mail.password": "secret-mail-pass",
		"panel.listen": "203.0.113.7",
	} {
		if err := db.Create(&model.Setting{Key: k, Value: v}).Error; err != nil {
			t.Fatal(err)
		}
	}
	issued, err := s.nodes.IssueToken(context.Background(), "managing panel")
	if err != nil {
		t.Fatal(err)
	}
	return s.Routes(), issued.Token
}

func call(h http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A managing panel reaches its node through the token the node issued it.
// From v2.0.0 to v2.6.0 every one of these was refused -- "this part of the
// panel is the owner's" -- and no panel could manage a node at all: not probe
// it, not sync its tunnels and customers, not collect its usage.
func TestAManagingPanelReachesItsNode(t *testing.T) {
	h, token := machineServer(t)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/system", ""},
		{"POST", "/api/node/usage", ""},
		{"POST", "/api/node/usage", `{"keep":[]}`},
		{"POST", "/api/node/sessions", ""},
		{"GET", "/api/nodes", ""},
	} {
		rec := call(h, token, c.method, c.path, c.body)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s with the node's token: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	// The settings answer a token what they answer a reseller: the display
	// preferences, never the secrets or where the panel listens.
	rec := call(h, token, "GET", "/api/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings with a token: %d", rec.Code)
	}
	for _, secret := range []string{"secret-bot-token", "secret-mail-pass", "203.0.113.7"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("GET /api/settings hands a token %q", secret)
		}
	}
	// A sync with nothing in it is refused for what it holds -- reaching the
	// handler is the point.
	if rec := call(h, token, "POST", "/api/node/sync", `{}`); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Errorf("POST /api/node/sync with the node's token: %d %s", rec.Code, rec.Body)
	}
}

// What is the owner's own stays closed to a machine token: the operators,
// the owner's account and sessions, further tokens, the backups, the
// settings -- everything a leaked token could use to keep the door open.
func TestAMachineTokenIsStillRefusedTheOwnersOwn(t *testing.T) {
	h, token := machineServer(t)
	checked := 0
	for _, r := range newRouteServer().routes() {
		if r.Group != "Operators" && !r.Operator {
			continue
		}
		path := strings.NewReplacer("{id}", "1", "{name}", "x").Replace(r.Path)
		body := ""
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			body = "{}"
		}
		rec := call(h, token, r.Method, path, body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a machine token: %d, want 403", r.Method, r.Path, rec.Code)
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only %d routes checked; the route table is not what this test expects", checked)
	}

	// And a token that is not one is not let in at all.
	if rec := call(h, "wui_notreal", "GET", "/api/system", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a made-up token: %d, want 401", rec.Code)
	}
}

// Each gate holds on its own, not only behind the others: requireManager lets
// a machine token through, requireAdminManager and requireOperator do not.
// (Every Operators route is also Operator, so through the mux the first of
// those two answers and the second is never asked.)
func TestEachGateDecidesAMachineTokenItself(t *testing.T) {
	s := &Server{}
	machine := context.WithValue(context.Background(), ctxMachine, true)
	for name, c := range map[string]struct {
		gate func(http.HandlerFunc) http.HandlerFunc
		want int
	}{
		"requireManager":      {s.requireManager, http.StatusOK},
		"requireAdminManager": {s.requireAdminManager, http.StatusForbidden},
		"requireOperator":     {s.requireOperator, http.StatusForbidden},
	} {
		rec := httptest.NewRecorder()
		c.gate(func(w http.ResponseWriter, r *http.Request) {})(rec,
			httptest.NewRequest(http.MethodGet, "/", nil).WithContext(machine))
		if rec.Code != c.want {
			t.Errorf("%s with a machine token: %d, want %d", name, rec.Code, c.want)
		}
	}
}
