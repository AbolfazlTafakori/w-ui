package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/panelconf"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/scope"
	"github.com/abolfazl/w-ui/internal/service"
)

// A panel with three tunnels and five operators, each with customers of
// their own:
//
//	wg-a  (1)  given to reseller "ra" and to "rb"
//	wg-b  (2)  given to "rb" alone, switched off
//	oc-c  (3)  nobody's but the owner's
//
//	owner      customers on 1 and 3
//	admin "ad" (every page, wg-a and wg-b) with reseller "rc" under them
//	admin "an" with no page at all
//	"ra"  two customers on 1, one of them online
//	"rb"  one customer on 1 and one on 2
//	"rc"  one customer on 1
func serversPanel(t *testing.T) (http.Handler, map[string]session, *gorm.DB) {
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
		DB: db, Logger: log, Catalog: catalog,
		Enforcer: enforce.NewNoop(), Settings: panelconf.NewSettings(db, "en"),
		Interfaces: service.NewInterfaces(db, ipam.NewPools(), log),
		Clients:    service.NewClients(db, ipam.NewPools(), log),
		Reconciler: reconciler.New(reconciler.Options{DB: db, Log: log, Enforcer: enforce.NewNoop(), Pool: backend.NewPool(log)}),
		JWTSecret:  []byte("servers-test-secret-0123456789abcd"), Version: "test",
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error)
	for _, f := range []model.Interface{
		{Name: "wg-a", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.1.0.0/24",
			EndpointHost: "secret-host.example.com", NodeID: 1, Enabled: true,
			PrivateKey: "PRIVATE-KEY-MUST-NOT-LEAK", PublicKey: "PUBLIC-KEY-MUST-NOT-LEAK"},
		{Name: "wg-b", Protocol: model.ProtocolWireGuard, ListenPort: 51821, Subnet: "10.2.0.0/24",
			EndpointHost: "secret-host.example.com", NodeID: 1, Enabled: true},
		{Name: "oc-c", Protocol: model.ProtocolOpenConnect, ListenPort: 443, Subnet: "10.3.0.0/24",
			EndpointHost: "secret-host.example.com", NodeID: 1, Enabled: true},
	} {
		must(db.Create(&f).Error)
	}
	// Switched off after the insert: the schema's default is on.
	must(db.Model(&model.Interface{}).Where("id = ?", 2).Update("enabled", false).Error)

	hash, err := bcrypt.GenerateFromPassword([]byte("servers-test-pass-1"), bcrypt.MinCost)
	must(err)
	ids := map[string]uint{}
	mk := func(a model.Admin, tunnels ...uint) {
		a.PasswordHash = string(hash)
		must(db.Create(&a).Error)
		ids[a.Username] = a.ID
		for _, f := range tunnels {
			must(db.Create(&model.AdminInterface{AdminID: a.ID, InterfaceID: f}).Error)
		}
	}
	mk(model.Admin{Username: "owner", Role: model.RoleOwner})
	mk(model.Admin{Username: "ad", Role: model.RoleAdmin}, 1, 2)
	for _, m := range model.GrantableMenus() {
		must(db.Create(&model.AdminMenuPermission{AdminID: ids["ad"], Menu: m}).Error)
	}
	mk(model.Admin{Username: "an", Role: model.RoleAdmin})
	mk(model.Admin{Username: "ra", Role: model.RoleReseller, Enabled: true}, 1)
	// ra is granted the sharing page; rb is not.
	must(db.Create(&model.AdminMenuPermission{AdminID: ids["ra"], Menu: model.MenuSharing}).Error)
	mk(model.Admin{Username: "rb", Role: model.RoleReseller, Enabled: true}, 1, 2)
	mk(model.Admin{Username: "rc", Role: model.RoleReseller, Enabled: true, ParentAdminID: ids["ad"]}, 1)

	// The panel sets how recent a handshake counts as online when it starts.
	was := service.OnlineWithin.Load()
	service.OnlineWithin.Store(180)
	t.Cleanup(func() { service.OnlineWithin.Store(was) })
	online := time.Now().UTC()
	n := 0
	customer := func(owner string, adminID uint, tunnel uint, live bool) {
		n++
		c := model.Client{Name: "c" + string(rune('a'+n)), Status: model.StatusActive, OwnerID: ids[owner], AdminID: adminID}
		if owner == "owner" {
			c.OwnerID = 0
		}
		must(db.Create(&c).Error)
		a := model.Account{ClientID: c.ID, InterfaceID: tunnel, NodeID: 1, DeviceName: "d",
			IP: "10.0.0." + string(rune('0'+n)), Enabled: true}
		if live {
			a.LastHandshake = &online
		}
		must(db.Create(&a).Error)
	}
	customer("owner", 0, 1, true)
	customer("owner", 0, 3, false)
	customer("ra", 0, 1, true)
	customer("ra", 0, 1, false)
	customer("rb", 0, 1, false)
	customer("rb", 0, 2, false)
	customer("rc", ids["ad"], 1, true)

	mux := http.NewServeMux()
	s.register(mux)
	sessions := map[string]session{}
	for _, who := range []string{"owner", "ad", "an", "ra", "rb", "rc"} {
		body, _ := json.Marshal(map[string]string{"username": who, "password": "servers-test-pass-1"})
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
		sessions[who] = session{token: got.Token, cookies: rec.Result().Cookies()}
	}
	return mux, sessions, db
}

func askServers(t *testing.T, mux http.Handler, sess session) (int, []map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/operator/servers", nil)
	req.Header.Set("Authorization", "Bearer "+sess.token)
	for _, c := range sess.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out []map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%v: %s", err, rec.Body)
		}
	}
	return rec.Code, out, rec.Body.String()
}

// byName is the answer as name -> "status clients/online".
func byName(rows []map[string]any) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		out[r["name"].(string)] = r["status"].(string) + " " +
			jsonNum(r["clients"]) + "/" + jsonNum(r["online"])
	}
	return out
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Each operator is told about the tunnels the owner gave them and no
// others, with their own customers counted -- never the tunnel's total.
func TestAnOperatorSeesTheirOwnServersAndCustomersOnly(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	cases := map[string]map[string]string{
		// Two on wg-a, one online; wg-b and oc-c were not given to them.
		"ra": {"wg-a": "down 2/1"},
		// wg-b is switched off and still theirs to be told about.
		"rb": {"wg-a": "down 1/0", "wg-b": "off 1/0"},
		// Under admin ad, and narrowed to their own customer all the same.
		"rc": {"wg-a": "down 1/1"},
		// An admin is told the tunnels the owner gave them, and counts their
		// whole tree: their reseller rc's customer, and none of ra's, rb's or
		// the owner's.
		"ad": {"wg-a": "down 1/1", "wg-b": "off 0/0"},
		// The owner: every tunnel, every customer.
		"owner": {"wg-a": "down 5/3", "wg-b": "off 1/0", "oc-c": "down 1/0"},
	}
	for who, want := range cases {
		code, rows, body := askServers(t, mux, sess[who])
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", who, code, body)
		}
		got := byName(rows)
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", who, got, want)
			continue
		}
		for name, w := range want {
			if got[name] != w {
				t.Errorf("%s: %s is %q, want %q", who, name, got[name], w)
			}
		}
	}
}

// The answer carries a tunnel's name and state and nothing behind them: no
// key, address, port, subnet, node or setting reaches a reseller, checked by
// the JSON keys themselves rather than by the fields a struct happens to
// have today.
func TestTheServerStatusCarriesNothingElse(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	for _, who := range []string{"ra", "rb", "rc", "ad"} {
		_, rows, body := askServers(t, mux, sess[who])
		for _, r := range rows {
			var keys []string
			for k := range r {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if strings.Join(keys, ",") != "clients,id,name,online,protocol,status" {
				t.Errorf("%s: a row carries %v", who, keys)
			}
		}
		for _, secret := range []string{"PRIVATE-KEY", "PUBLIC-KEY", "secret-host", "10.1.0", "51820", "443", "nodeName", "endpoint", "subnet"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s: the answer carries %q: %s", who, secret, body)
			}
		}
	}
}

// An admin the owner left without the customers page is not given the
// servers that page shows either.
func TestAnAdminWithoutTheCustomersPageIsRefused(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	if code, _, body := askServers(t, mux, sess["an"]); code != http.StatusForbidden {
		t.Fatalf("an admin with no page: %d %s", code, body)
	}
	if code, _, _ := askServers(t, mux, session{}); code != http.StatusUnauthorized {
		t.Fatalf("nobody signed in: %d", code)
	}
}
