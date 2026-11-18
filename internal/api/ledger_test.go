package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
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
