package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// What the sharing tests add to the servers panel: devices seen from several
// places now, and a record with entries of several operators' customers.
//
//	account 1  the owner's customer, from three networks now
//	account 3  ra's customer, from 5.1.1.10, 5.1.1.20, 6.2.2.2 and 7.3.3.3
//	record     ra's customer (3), a customer of ra's since deleted,
//	           rb's customer (5), the owner's customer (1)
func seedSharing(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	for _, e := range []model.AccountEndpoint{
		{AccountID: 1, Addr: "9.9.9.1", FirstSeen: now, LastSeen: now},
		{AccountID: 1, Addr: "9.8.8.1", FirstSeen: now, LastSeen: now},
		{AccountID: 1, Addr: "9.7.7.1", FirstSeen: now, LastSeen: now},
		{AccountID: 3, Addr: "5.1.1.10", FirstSeen: now, LastSeen: now},
		{AccountID: 3, Addr: "5.1.1.20", FirstSeen: now, LastSeen: now},
		{AccountID: 3, Addr: "6.2.2.2", FirstSeen: now, LastSeen: now},
		{AccountID: 3, Addr: "7.3.3.3", FirstSeen: now, LastSeen: now},
	} {
		if err := db.Create(&e).Error; err != nil {
			t.Fatal(err)
		}
	}
	var ra, rb uint
	db.Model(&model.Admin{}).Where("username = ?", "ra").Pluck("id", &ra)
	db.Model(&model.Admin{}).Where("username = ?", "rb").Pluck("id", &rb)
	for i, ev := range []model.SharingEvent{
		{ClientID: 3, OwnerID: ra, ClientName: "ra-live", Addrs: "5.1.1.10,6.2.2.2,2a01:4f8:c010:6f04::1"},
		{ClientID: 99, OwnerID: ra, ClientName: "ra-gone", Addrs: "8.8.4.4"},
		{ClientID: 5, OwnerID: rb, ClientName: "rb-cust", Addrs: "1.1.1.1"},
		{ClientID: 1, OwnerID: 0, ClientName: "owner-cust", Addrs: "4.4.4.4"},
	} {
		ev.Kind, ev.DeviceName, ev.Source = model.SharingShared, "d", model.SharingLive
		ev.StartedAt, ev.LastSeen = now.Add(-time.Duration(i+1)*time.Hour), now.Add(-time.Duration(i)*time.Minute)
		if err := db.Create(&ev).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func askJSON(t *testing.T, mux http.Handler, sess session, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if sess.token != "" {
		req.Header.Set("Authorization", "Bearer "+sess.token)
	}
	for _, c := range sess.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// A reseller granted sharing sees their own customer's device, by network:
// two addresses in one /24 are one network counted twice, and no address is
// sent in full. The owner's customer, seen from three places too, is not
// in it.
func TestAResellerSeesTheirOwnSharingByNetwork(t *testing.T) {
	mux, sess, db := serversPanel(t)
	seedSharing(t, db)
	code, body := askJSON(t, mux, sess["ra"], "/api/operator/sharing")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var rows []operatorSharingView
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ClientID != 3 {
		t.Fatalf("ra sees %+v", rows)
	}
	var nets []string
	for _, n := range rows[0].Networks {
		nets = append(nets, n.Net+"*"+jsonNum(n.Count))
	}
	sort.Strings(nets)
	if strings.Join(nets, " ") != "5.1.1.x*2 6.2.2.x*1 7.3.3.x*1" {
		t.Errorf("networks: %v", nets)
	}
	for _, full := range []string{"5.1.1.10", "5.1.1.20", "6.2.2.2", "7.3.3.3", "9.9.9.1", "\"addrs\""} {
		if strings.Contains(body, full) {
			t.Errorf("the answer carries %q: %s", full, body)
		}
	}
}

// The record of a reseller's own customers, a deleted one's included and
// marked so; nobody else's, and no address in full.
func TestAResellersSharingRecordIsTheirOwn(t *testing.T) {
	mux, sess, db := serversPanel(t)
	seedSharing(t, db)
	code, body := askJSON(t, mux, sess["ra"], "/api/operator/sharing/history")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r["clientName"].(string)] = r["deleted"].(bool)
		var keys []string
		for k := range r {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != "clientId,clientName,deleted,deviceName,id,kind,lastSeen,limit,networks,startedAt" {
			t.Errorf("an entry carries %v", keys)
		}
	}
	if len(got) != 2 || got["ra-live"] != false || got["ra-gone"] != true {
		t.Fatalf("ra's record: %v", got)
	}
	for _, other := range []string{"rb-cust", "owner-cust", "8.8.4.4", "2a01:4f8:c010:6f04::1", "place", "source"} {
		if strings.Contains(body, other) {
			t.Errorf("the record carries %q: %s", other, body)
		}
	}
	if !strings.Contains(body, "2a01:4f8:c010::/48") {
		t.Errorf("an IPv6 address is not given as its /48: %s", body)
	}
}

// Sharing is the owner's to grant: a reseller without it, and an admin
// without it, are refused both routes; an admin with it sees their tree's
// record alone, here none of it.
func TestSharingIsGrantedNotGiven(t *testing.T) {
	mux, sess, db := serversPanel(t)
	seedSharing(t, db)
	for _, who := range []string{"rb", "an"} {
		for _, path := range []string{"/api/operator/sharing", "/api/operator/sharing/history"} {
			if code, body := askJSON(t, mux, sess[who], path); code != http.StatusForbidden {
				t.Errorf("%s %s: %d %s", who, path, code, body)
			}
		}
	}
	code, body := askJSON(t, mux, sess["ad"], "/api/operator/sharing/history")
	if code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("ad, whose tree has no entries: %d %s", code, body)
	}
	if code, _ := askJSON(t, mux, session{}, "/api/operator/sharing"); code != http.StatusUnauthorized {
		t.Errorf("nobody signed in: %d", code)
	}
}

// The owner's own report is untouched: every address in full, as before.
func TestTheOwnersSharingIsAsItWas(t *testing.T) {
	mux, sess, db := serversPanel(t)
	seedSharing(t, db)
	code, body := askJSON(t, mux, sess["owner"], "/api/sharing/history")
	if code != http.StatusOK || !strings.Contains(body, "2a01:4f8:c010:6f04::1") || !strings.Contains(body, "owner-cust") {
		t.Fatalf("the owner's record: %d %s", code, body)
	}
	if strings.Contains(body, "ownerId") || strings.Contains(body, "owner_id") {
		t.Errorf("the owner's record gained a field: %s", body)
	}
}

func TestMaskAddr(t *testing.T) {
	for in, want := range map[string]string{
		"5.1.1.10":              "5.1.1.x",
		"5.1.1.10:51820":        "5.1.1.x",
		"[2a01:4f8::1]:443":     "2a01:4f8::/48",
		"2a01:4f8:c010:6f04::1": "2a01:4f8:c010::/48",
		"::ffff:10.0.0.7":       "10.0.0.x",
		"not an address":        "",
		"":                      "",
	} {
		if got := maskAddr(in); got != want {
			t.Errorf("maskAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// An admin manages their own resellers, but granting one a page is the
// owner's: an admin ticking sharing for their reseller is refused, on a new
// reseller and on one they already have.
func TestOnlyTheOwnerGrantsAResellerSharing(t *testing.T) {
	mux, sess, db := serversPanel(t)
	var rc uint
	db.Model(&model.Admin{}).Where("username = ?", "rc").Pluck("id", &rc)
	send := func(who, method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+sess[who].token)
		for _, c := range sess[who].cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := send("ad", http.MethodPost, "/api/admins",
		`{"username":"rnew","password":"servers-test-pass-1","role":"reseller","interfaceIds":[1],"menuIds":["sharing"]}`); code != http.StatusForbidden {
		t.Errorf("an admin granted a new reseller sharing: %d", code)
	}
	if code := send("ad", http.MethodPatch, "/api/admins/"+jsonNum(rc), `{"menuIds":["sharing"]}`); code != http.StatusForbidden {
		t.Errorf("an admin granted their reseller sharing: %d", code)
	}
	var n int64
	db.Model(&model.AdminMenuPermission{}).Where("admin_id = ?", rc).Count(&n)
	if n != 0 {
		t.Fatalf("rc holds %d pages", n)
	}
	if code := send("owner", http.MethodPatch, "/api/admins/"+jsonNum(rc), `{"menuIds":["sharing"]}`); code != http.StatusOK {
		t.Fatalf("the owner could not grant sharing: %d", code)
	}
}
