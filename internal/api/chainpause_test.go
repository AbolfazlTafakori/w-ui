package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// An admin paused stops their whole tree: their own open session, their
// reseller's open session and token, and a fresh sign-in by either, all
// refused; switched back on, all of it works again exactly as before.
func TestAPausedAdminStopsTheirTreeEverywhere(t *testing.T) {
	mux, sess, db := serversPanel(t)
	tok := issueFor(t, mux, sess["owner"], idOf(t, db, "rc"), `{"name":"shop"}`)
	reach := func(s session) int {
		code, _ := send(t, mux, s, http.MethodGet, "/api/clients", "")
		return code
	}
	for name, s := range map[string]session{"ad": sess["ad"], "rc": sess["rc"], "rc token": {token: tok}} {
		if code := reach(s); code != http.StatusOK {
			t.Fatalf("%s before the pause: %d", name, code)
		}
	}

	if err := db.Model(&model.Admin{}).Where("username = ?", "ad").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]session{"ad": sess["ad"], "rc": sess["rc"], "rc token": {token: tok}} {
		if code := reach(s); code != http.StatusUnauthorized {
			t.Errorf("%s while ad is paused: %d", name, code)
		}
	}
	for who, want := range map[string]string{"ad": model.PauseSwitchedOff, "rc": model.PauseByAdmin} {
		code, body := send(t, mux, session{}, http.MethodPost, "/api/auth/login",
			`{"username":"`+who+`","password":"servers-test-pass-1"}`)
		if code != http.StatusForbidden || !contains(body, model.PauseMessage(want)) {
			t.Errorf("%s signs in while paused: %d %s", who, code, body)
		}
	}
	// The other trees go on.
	if code := reach(sess["ra"]); code != http.StatusOK {
		t.Errorf("ra, under nobody paused: %d", code)
	}

	if err := db.Model(&model.Admin{}).Where("username = ?", "ad").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]session{"ad": sess["ad"], "rc": sess["rc"], "rc token": {token: tok}} {
		if code := reach(s); code != http.StatusOK {
			t.Errorf("%s after ad is back: %d", name, code)
		}
	}
}

// A reseller paused on their own is out, and their admin is not.
func TestAPausedResellerIsOutAndTheirAdminIsNot(t *testing.T) {
	mux, sess, db := serversPanel(t)
	if err := db.Model(&model.Admin{}).Where("username = ?", "rc").Updates(map[string]any{"quota_bytes": 1, "used_bytes": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if code, _ := send(t, mux, sess["rc"], http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("rc with traffic spent: %d", code)
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodGet, "/api/clients", ""); code != http.StatusOK {
		t.Errorf("ad, over a paused reseller: %d", code)
	}
}

// Who is online is right while a tree is paused: rc's customer handshook a
// moment ago, but rc's admin is paused, so they are off -- not online on
// the tunnel, on the dashboard, in the "online" filter or on their own row.
// Back on, they count again with nothing written in between.
func TestAPausedTreeIsNotCountedOnline(t *testing.T) {
	mux, sess, db := serversPanel(t)
	type tally struct {
		tunnel  string
		online  float64
		filter  int
		rowLive float64
	}
	read := func() tally {
		t.Helper()
		var out tally
		_, rows, _ := askServers(t, mux, sess["owner"])
		out.tunnel = byName(rows)["wg-a"]
		code, body := send(t, mux, sess["owner"], http.MethodGet, "/api/overview", "")
		if code != http.StatusOK {
			t.Fatalf("overview: %d %s", code, body)
		}
		var o map[string]any
		if err := json.Unmarshal([]byte(body), &o); err != nil {
			t.Fatal(err)
		}
		out.online, _ = o["online"].(float64)
		code, body = send(t, mux, sess["owner"], http.MethodGet, "/api/clients?buckets=online&perPage=100", "")
		if code != http.StatusOK {
			t.Fatalf("list: %d %s", code, body)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		out.filter = len(page.Items)
		code, body = send(t, mux, sess["owner"], http.MethodGet, "/api/clients/7", "")
		if code != http.StatusOK {
			t.Fatalf("get: %d %s", code, body)
		}
		var one map[string]any
		if err := json.Unmarshal([]byte(body), &one); err != nil {
			t.Fatal(err)
		}
		out.rowLive, _ = one["onlineNow"].(float64)
		return out
	}

	before := read()
	if before.tunnel != "down 5/3" || before.filter != 3 {
		t.Fatalf("before: %+v", before)
	}
	if err := db.Model(&model.Admin{}).Where("username = ?", "ad").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	paused := read()
	if paused.tunnel != "down 5/2" || paused.filter != 2 || paused.online != before.online-1 || paused.rowLive != 0 {
		t.Errorf("while ad is paused: %+v (before %+v)", paused, before)
	}
	if err := db.Model(&model.Admin{}).Where("username = ?", "ad").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if after := read(); after != before {
		t.Errorf("back on: %+v, want %+v", after, before)
	}
}
