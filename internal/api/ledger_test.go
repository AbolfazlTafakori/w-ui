package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
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
