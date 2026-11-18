package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/abolfazl/w-ui/internal/service"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// Each operator reads their own capacity ledger and nobody else's: an admin
// their own and their resellers', a reseller their own, the owner every
// account's. Nothing in the request widens it.
func TestTheLedgerIsEachTreesOwn(t *testing.T) {
	mux, sess, db := serversPanel(t)
	// One movement on each operator's account.
	for _, who := range []string{"ad", "an", "ra", "rb", "rc"} {
		id := idOf(t, db, who)
		a, err := capacity.Open(db, id, model.CapacityUsers, model.CapacityOpen)
		if err != nil {
			t.Fatal(err)
		}
		if err := capacity.Charge(db, a, 10, capacity.Entry{Reason: model.CapacityCharge}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(who, query string) map[string]bool {
		t.Helper()
		code, body := send(t, mux, sess[who], http.MethodGet, "/api/operator/ledger"+query, "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", who, code, body)
		}
		var out struct {
			Items []struct {
				Operator string `json:"operator"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, l := range out.Items {
			seen[l.Operator] = true
		}
		return seen
	}
	cases := map[string][]string{
		"ad":    {"ad", "rc"},
		"rc":    {"rc"},
		"ra":    {"ra"},
		"an":    {"an"},
		"owner": {"ad", "an", "ra", "rb", "rc"},
	}
	for who, want := range cases {
		for _, query := range []string{"", "?kind=users", "?adminId=" + jsonNum(idOf(t, db, "rb")), "?limit=100000"} {
			got := read(who, query)
			if len(got) != len(want) {
				t.Errorf("%s%s sees %v, want %v", who, query, got, want)
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("%s%s does not see %s's line", who, query, w)
				}
			}
		}
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodGet, "/api/operator/ledger?kind=../../", ""); code != http.StatusBadRequest {
		t.Errorf("a kind that is not one: %d", code)
	}
	if code, _ := send(t, mux, session{}, http.MethodGet, "/api/operator/ledger", ""); code != http.StatusUnauthorized {
		t.Errorf("nobody signed in: %d", code)
	}
}

// The operators page shows each operator's capacity, read for the whole list
// at once; an admin sees their own resellers' and nobody else's.
func TestTheOperatorsListCarriesCapacity(t *testing.T) {
	mux, sess, db := serversPanel(t)
	rc := idOf(t, db, "rc")
	a, err := capacity.Open(db, rc, model.CapacityUsers, model.CapacityConsumable)
	if err != nil {
		t.Fatal(err)
	}
	if err := capacity.Charge(db, a, 40, capacity.Entry{Reason: model.CapacityCharge}); err != nil {
		t.Fatal(err)
	}
	read := func(who string) map[string]*model.OperatorCapacity {
		t.Helper()
		code, body := send(t, mux, sess[who], http.MethodGet, "/api/admins", "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", who, code, body)
		}
		var out struct {
			Items []struct {
				Username string                  `json:"username"`
				Capacity *model.OperatorCapacity `json:"capacity"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		m := map[string]*model.OperatorCapacity{}
		for _, r := range out.Items {
			m[r.Username] = r.Capacity
		}
		return m
	}
	for _, who := range []string{"owner", "ad"} {
		got := read(who)
		c := got["rc"]
		if c == nil || c.UserMode != model.CapacityConsumable || c.UsersGiven != 40 {
			t.Errorf("%s reads rc's capacity as %+v", who, c)
		}
		if who == "ad" && len(got) != 1 {
			t.Errorf("ad lists %v, want rc alone", got)
		}
		if who == "owner" && got["owner"] != nil {
			t.Errorf("the owner has a ceiling: %+v", got["owner"])
		}
	}
}

// An admin the owner lets see every customer is not narrowed, and is an
// admin all the same: the scope the API builds for them holds them to the
// tunnels they were given and carries their pause. Before, they had the
// owner's own scope: any tunnel, and nothing barred while paused.
func TestSeeingEveryoneIsStillAnAdminsScope(t *testing.T) {
	mux, sess, db := serversPanel(t)
	ad := idOf(t, db, "ad")
	if err := db.Model(&model.Admin{}).Where("id = ?", ad).Update("can_view_all_clients", true).Error; err != nil {
		t.Fatal(err)
	}
	// They still see every customer.
	code, body := send(t, mux, sess["ad"], http.MethodGet, "/api/clients?perPage=100", "")
	var page struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal([]byte(body), &page)
	if code != http.StatusOK || page.Total != 7 {
		t.Fatalf("ad sees %d customers (%d), want all 7", page.Total, code)
	}
	srv := &Server{admins: operators.NewAdmins(db, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	var a model.Admin
	db.First(&a, ad)
	sc, err := srv.scopeFor(context.Background(), &a)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Restricted || !sc.Capped || sc.AdminID != ad || sc.AdminStamp != ad {
		t.Errorf("the scope: %+v", sc)
	}
	if sc.Allows(3) || !sc.Allows(1) {
		t.Errorf("tunnels: %v, want wg-a and wg-b alone", sc.Interfaces)
	}
	db.Model(&model.Admin{}).Where("id = ?", ad).Update("enabled", false)
	db.First(&a, ad)
	if sc, _ = srv.scopeFor(context.Background(), &a); sc.Barred == "" {
		t.Error("paused, and not barred")
	}
}

// A preview changes nothing, and nothing says it did: no record in the
// operators' activity, while the extension itself is recorded.
func TestAPreviewLeavesNoTrace(t *testing.T) {
	mux, sess, db := serversPanel(t)
	count := func() int64 {
		var n int64
		db.Model(&model.OperatorAction{}).Count(&n)
		return n
	}
	before := count()
	body := `{"ids":[3],"kind":"time","days":3,"dryRun":true}`
	if code, out := send(t, mux, sess["ra"], http.MethodPost, "/api/clients/extend", body); code != http.StatusOK {
		t.Fatalf("preview: %d %s", code, out)
	}
	if got := count(); got != before {
		t.Errorf("a preview was recorded as a change: %d records, was %d", got, before)
	}
	var c model.Client
	db.First(&c, 3)
	if c.ExpiresAt != nil {
		t.Errorf("the preview gave customer 3 an end date: %v", c.ExpiresAt)
	}
	if code, _ := send(t, mux, sess["ra"], http.MethodPost, "/api/clients/extend", `{"ids":[3],"kind":"traffic","gb":1}`); code != http.StatusOK {
		t.Fatal("the extension itself was refused")
	}
	if count() != before+1 {
		t.Error("the extension itself was not recorded")
	}
}

// The answers that matter carry their fixed code: a reseller issuing a
// token, an admin with none to issue, a paused operator signing in, and
// a service refusal for want of users.
func TestRefusalsCarryTheirCode(t *testing.T) {
	mux, sess, db := serversPanel(t)
	code := func(body string) string {
		var out map[string]any
		_ = json.Unmarshal([]byte(body), &out)
		c, _ := out["code"].(string)
		return c
	}
	if _, body := send(t, mux, sess["rc"], http.MethodPost, "/api/operator/tokens", `{"name":"x"}`); code(body) != codeTokensResellerIssues {
		t.Errorf("a reseller issuing: %s", body)
	}
	if _, body := send(t, mux, sess["an"], http.MethodGet, "/api/operator/tokens", ""); code(body) != codeTokensNone {
		t.Errorf("an admin with no tokens: %s", body)
	}
	db.Model(&model.Admin{}).Where("username = ?", "rb").Update("enabled", false)
	if _, body := send(t, mux, session{}, http.MethodPost, "/api/auth/login", `{"username":"rb","password":"servers-test-pass-1"}`); code(body) != codeOperatorPaused {
		t.Errorf("a paused operator signing in: %s", body)
	}
	rec := httptest.NewRecorder()
	fail(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), fmt.Errorf("%w: %w: this needs 2 users and you have 1 left; ask for more to be added", service.ErrInvalid, service.ErrNoUsers))
	if code(rec.Body.String()) != codeCapacityInsufficient {
		t.Errorf("a refusal for want of users: %s", rec.Body.String())
	}
}

// What is going wrong is the owner's to see in full, and an operator's
// only where it touches the servers they sell on -- with no detail.
func TestProblemsAreShownToWhomTheyConcern(t *testing.T) {
	mux, sess, db := serversPanel(t)
	sold := model.Node{Name: "sold-node", Kind: model.KindRemote, Address: "x", Enabled: true, LastError: "dial tcp 198.51.100.4:2096: connection refused"}
	other := model.Node{Name: "other-node", Kind: model.KindRemote, Address: "y", Enabled: true, LastError: "tls: bad certificate"}
	db.Create(&sold)
	db.Create(&other)
	db.Model(&model.Node{}).Where("id IN ?", []uint{sold.ID, other.ID}).Update("reachable", false)
	// wg-a, which ra sells, moves to the sold node; nothing of ra's is on the other.
	db.Model(&model.Interface{}).Where("id = ?", 1).Update("node_id", sold.ID)

	read := func(who string) []map[string]any {
		t.Helper()
		code, body := send(t, mux, sess[who], http.MethodGet, "/api/problems", "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", who, code, body)
		}
		var out struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		return out.Items
	}
	nodes := func(items []map[string]any) map[string]string {
		m := map[string]string{}
		for _, p := range items {
			if p["code"] == problemNodeUnreachable {
				d, _ := p["detail"].(string)
				m[p["node"].(string)] = d
			}
		}
		return m
	}
	owner := nodes(read("owner"))
	if owner["sold-node"] == "" || owner["other-node"] == "" {
		t.Errorf("the owner sees %v, want both with what failed", owner)
	}
	ra := nodes(read("ra"))
	if _, ok := ra["sold-node"]; !ok || ra["sold-node"] != "" {
		t.Errorf("ra sees %v, want the node they sell on, without the detail", ra)
	}
	if _, ok := ra["other-node"]; ok {
		t.Errorf("ra sees a node they do not sell on: %v", ra)
	}
}

// No operator raises their own capacity, or reaches another's: not by
// editing their own row, not through their own profile, not by naming
// another admin, and not a reseller through anything.
func TestNobodyRaisesTheirOwnCapacity(t *testing.T) {
	mux, sess, db := serversPanel(t)
	ad, rc := idOf(t, db, "ad"), idOf(t, db, "rc")
	for _, id := range []uint{ad, rc} {
		acc, err := capacity.Open(db, id, model.CapacityUsers, model.CapacityOpen)
		if err != nil {
			t.Fatal(err)
		}
		if err := capacity.Charge(db, acc, 5, capacity.Entry{Reason: model.CapacityCharge}); err != nil {
			t.Fatal(err)
		}
	}
	granted := func(id uint) int64 {
		a, _ := capacity.Account(db, id, model.CapacityUsers)
		return a.Granted
	}
	tries := []struct {
		who, method, path, body string
	}{
		{"ad", http.MethodPatch, "/api/admins/" + jsonNum(ad), `{"clientLimit":99999}`},
		{"ad", http.MethodPatch, "/api/admins/" + jsonNum(ad), `{"tokenLimit":99}`},
		{"ad", http.MethodPatch, "/api/admins/" + jsonNum(idOf(t, db, "an")), `{"clientLimit":99999}`},
		{"ad", http.MethodPatch, "/api/auth/me", `{"clientLimit":99999,"tokenLimit":99}`},
		{"rc", http.MethodPatch, "/api/admins/" + jsonNum(rc), `{"clientLimit":99999}`},
		{"rc", http.MethodPatch, "/api/auth/me", `{"clientLimit":99999}`},
		{"rc", http.MethodPost, "/api/admins", `{"username":"rcsub","password":"long-enough-pass-1","role":"reseller","clientLimit":100}`},
	}
	for _, tr := range tries {
		code, body := send(t, mux, sess[tr.who], tr.method, tr.path, tr.body)
		if code < 400 {
			t.Errorf("%s %s %s %s was taken: %d %s", tr.who, tr.method, tr.path, tr.body, code, body)
		}
	}
	if g := granted(ad); g != 5 {
		t.Errorf("ad's users are %d, want the 5 given", g)
	}
	if g := granted(rc); g != 5 {
		t.Errorf("rc's users are %d, want the 5 given", g)
	}
	var tok model.CapacityAccount
	if db.Where("admin_id = ? AND kind = ?", ad, model.CapacityTokens).Limit(1).Find(&tok); tok.Granted != 0 {
		t.Errorf("ad gave themselves %d tokens", tok.Granted)
	}
}
