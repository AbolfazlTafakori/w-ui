package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// send makes one request as a session, or with a bearer token alone.
func send(t *testing.T, mux http.Handler, sess session, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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

// issueFor makes a token as a signed-in operator for another (0: themself)
// and returns the secret.
func issueFor(t *testing.T, mux http.Handler, sess session, forID uint, body string) string {
	t.Helper()
	if forID != 0 {
		body = strings.TrimSuffix(body, "}") + `,"forAdminId":` + jsonNum(forID) + "}"
	}
	code, out := send(t, mux, sess, http.MethodPost, "/api/operator/tokens", body)
	if code != http.StatusCreated {
		t.Fatalf("issue: %d %s", code, out)
	}
	var got struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || !strings.HasPrefix(got.Token, "wuo_") {
		t.Fatalf("issue: %v %s", err, out)
	}
	return got.Token
}

func idOf(t *testing.T, db *gorm.DB, username string) uint {
	t.Helper()
	var id uint
	db.Model(&model.Admin{}).Where("username = ?", username).Pluck("id", &id)
	return id
}

// tokensFor gives admin ad a token capacity, as the owner does.
func tokensFor(t *testing.T, mux http.Handler, sess map[string]session, db *gorm.DB, n int) {
	t.Helper()
	if code, body := send(t, mux, sess["owner"], http.MethodPatch, "/api/admins/"+jsonNum(idOf(t, db, "ad")), `{"tokenLimit":`+jsonNum(n)+`}`); code != http.StatusOK {
		t.Fatalf("giving ad %d tokens: %d %s", n, code, body)
	}
}

func tokenAccount(t *testing.T, db *gorm.DB, who uint) *model.CapacityAccount {
	t.Helper()
	acc, err := capacity.Account(db, who, model.CapacityTokens)
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

// A reseller issues no token, not even calling the route straight; an
// admin issues for themself and their own resellers and nobody else's; the
// owner for anyone; only the owner sets an admin's capacity.
func TestWhoIssuesTokens(t *testing.T) {
	mux, sess, db := serversPanel(t)
	tokensFor(t, mux, sess, db, 5)
	for _, body := range []string{`{"name":"mine"}`, `{"name":"mine","forAdminId":` + jsonNum(idOf(t, db, "ra")) + `}`} {
		if code, out := send(t, mux, sess["ra"], http.MethodPost, "/api/operator/tokens", body); code != http.StatusForbidden {
			t.Errorf("a reseller issuing %s: %d %s", body, code, out)
		}
	}
	issueFor(t, mux, sess["ad"], 0, `{"name":"own"}`)
	issueFor(t, mux, sess["ad"], idOf(t, db, "rc"), `{"name":"for-rc"}`)
	if code, _ := send(t, mux, sess["ad"], http.MethodPost, "/api/operator/tokens", `{"name":"x","forAdminId":`+jsonNum(idOf(t, db, "ra"))+`}`); code != http.StatusNotFound {
		t.Errorf("ad issuing for ra, not theirs: %d", code)
	}
	issueFor(t, mux, sess["owner"], idOf(t, db, "ra"), `{"name":"from-owner"}`)
	if got := tokenAccount(t, db, idOf(t, db, "ad")).Used; got != 2 {
		t.Errorf("ad spent %d tokens, want 2: the owner's issue is not theirs", got)
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodPatch, "/api/admins/"+jsonNum(idOf(t, db, "rc")), `{"tokenLimit":9}`); code == http.StatusOK {
		t.Error("an admin set a token capacity")
	}
	if code, _ := send(t, mux, sess["owner"], http.MethodPatch, "/api/admins/"+jsonNum(idOf(t, db, "ra")), `{"tokenLimit":3}`); code == http.StatusOK {
		t.Error("a reseller was given a token capacity of their own")
	}
}

// Each token an admin issues spends one of theirs; at none left they issue
// no more; a revoked token is spent for good, so revoking and reissuing is
// no way to more.
func TestATokenIsSpentAndRevokingGivesNothingBack(t *testing.T) {
	mux, sess, db := serversPanel(t)
	tokensFor(t, mux, sess, db, 2)
	issueFor(t, mux, sess["ad"], 0, `{"name":"a"}`)
	issueFor(t, mux, sess["ad"], idOf(t, db, "rc"), `{"name":"b"}`)
	if code, body := send(t, mux, sess["ad"], http.MethodPost, "/api/operator/tokens", `{"name":"c"}`); code != http.StatusBadRequest || !strings.Contains(body, "no tokens left") {
		t.Fatalf("a third of two: %d %s", code, body)
	}
	var first model.OperatorToken
	db.Where("name = ?", "a").First(&first)
	if code, _ := send(t, mux, sess["ad"], http.MethodDelete, "/api/operator/tokens/"+jsonNum(first.ID), ""); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodPost, "/api/operator/tokens", `{"name":"c"}`); code != http.StatusBadRequest {
		t.Fatalf("a revoked token's unit came back: %d", code)
	}
	if acc := tokenAccount(t, db, idOf(t, db, "ad")); acc.Used != 2 || acc.Granted != 2 {
		t.Fatalf("ad's tokens: %+v", acc)
	}
	if bad, _ := capacity.Balanced(db); len(bad) > 0 {
		t.Fatalf("unbalanced: %v", bad)
	}
}

// Tokens issued at once cannot pass the capacity: twenty against five let
// five in.
func TestTokensIssuedAtOnceCannotPassTheCapacity(t *testing.T) {
	mux, sess, db := serversPanel(t)
	tokensFor(t, mux, sess, db, 5)
	var won atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := send(t, mux, sess["ad"], http.MethodPost, "/api/operator/tokens", `{"name":"race"}`)
			if code == http.StatusCreated {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	var n int64
	db.Model(&model.OperatorToken{}).Count(&n)
	if won.Load() != 5 || n != 5 || tokenAccount(t, db, idOf(t, db, "ad")).Used != 5 {
		t.Fatalf("issued %d, stored %d, spent %d -- against five", won.Load(), n, tokenAccount(t, db, idOf(t, db, "ad")).Used)
	}
}

// Where an operator has no token, the section is not theirs: a reseller
// issued none and an admin with no capacity are refused the list; one issued
// a token sees it.
func TestNoTokensNoTokenSection(t *testing.T) {
	mux, sess, db := serversPanel(t)
	for _, who := range []string{"rb", "ad", "an"} {
		if code, body := send(t, mux, sess[who], http.MethodGet, "/api/operator/tokens", ""); code != http.StatusForbidden {
			t.Errorf("%s with no tokens: %d %s", who, code, body)
		}
	}
	issueFor(t, mux, sess["owner"], idOf(t, db, "rb"), `{"name":"x"}`)
	if code, _ := send(t, mux, sess["rb"], http.MethodGet, "/api/operator/tokens", ""); code != http.StatusOK {
		t.Errorf("rb with a token: %d", code)
	}
}

// A reseller's token reaches what the reseller does and nothing more: their
// own customer, not another's (404, as signed in), and none of what only
// someone at the keyboard does.
func TestATokenCannotReachPastItsOperator(t *testing.T) {
	mux, sess, db := serversPanel(t)
	tok := session{token: issueFor(t, mux, sess["owner"], idOf(t, db, "ra"), `{"name":"shop"}`)}

	// Customers 3 and 4 are ra's, 5 is rb's, 1 the owner's.
	for path, want := range map[string]int{
		"/api/clients/3":        http.StatusOK,
		"/api/clients/5":        http.StatusNotFound,
		"/api/clients/1":        http.StatusNotFound,
		"/api/operator/servers": http.StatusOK,
	} {
		if code, body := send(t, mux, tok, http.MethodGet, path, ""); code != want {
			t.Errorf("GET %s: %d, want %d: %s", path, code, want, body)
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/operator/tokens", `{"name":"another"}`},
		{"GET", "/api/operator/tokens", ""},
		{"DELETE", "/api/operator/tokens/1", ""},
		{"GET", "/api/auth/me", ""},
		{"POST", "/api/auth/password", `{"currentPassword":"servers-test-pass-1","newPassword":"something-else-1"}`},
		{"POST", "/api/auth/totp/start", ""},
		{"GET", "/api/admins", ""},
		{"POST", "/api/admins", `{"username":"rzz","password":"servers-test-pass-1","role":"reseller"}`},
		{"PATCH", "/api/admins/" + jsonNum(idOf(t, db, "ra")), `{"clientLimit":999,"tokenLimit":99}`},
		{"GET", "/api/settings", ""},
		{"PUT", "/api/settings", `{}`},
		{"GET", "/api/system", ""},
		{"POST", "/api/interfaces", `{}`},
		{"GET", "/api/tokens", ""},
		{"POST", "/api/tokens", `{"name":"x"}`},
	} {
		if code, body := send(t, mux, tok, c.method, c.path, c.body); code != http.StatusForbidden {
			t.Errorf("%s %s with a reseller's token: %d, want 403: %s", c.method, c.path, code, body)
		}
	}
}

// The token holds no rights of its own: an operator switched off is turned
// away with it at once, and one given fewer servers reaches fewer.
func TestATokenIsItsOperatorAsTheyAreNow(t *testing.T) {
	mux, sess, db := serversPanel(t)
	rb := idOf(t, db, "rb")
	tok := session{token: issueFor(t, mux, sess["owner"], rb, `{"name":"shop"}`)}

	_, body := send(t, mux, tok, http.MethodGet, "/api/operator/servers", "")
	if !strings.Contains(body, "wg-b") {
		t.Fatalf("rb's servers before: %s", body)
	}
	if err := db.Where("admin_id = ? AND interface_id = ?", rb, 2).Delete(&model.AdminInterface{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, body := send(t, mux, tok, http.MethodGet, "/api/operator/servers", ""); strings.Contains(body, "wg-b") {
		t.Errorf("the token still reaches a server taken away: %s", body)
	}
	if code, _ := send(t, mux, sess["owner"], http.MethodPatch, "/api/admins/"+jsonNum(rb), `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("switching rb off: %d", code)
	}
	if code, body := send(t, mux, tok, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("a switched-off operator's token: %d, want 401: %s", code, body)
	}
}

// A revoked, an expired and a made-up token are all turned away, and a
// removed operator's tokens go with them.
func TestATokenThatIsNotLiveIsRefused(t *testing.T) {
	mux, sess, db := serversPanel(t)
	ra := idOf(t, db, "ra")
	tok := issueFor(t, mux, sess["owner"], ra, `{"name":"shop"}`)
	var row model.OperatorToken
	if err := db.Where("admin_id = ?", ra).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Hash == tok || strings.Contains(row.Hash, tok[4:]) {
		t.Fatal("the secret is stored as it is")
	}
	db.Model(&model.OperatorToken{}).Where("id = ?", row.ID).Update("expires_at", time.Now().UTC().Add(-time.Minute))
	if code, _ := send(t, mux, session{token: tok}, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("an expired token: %d", code)
	}
	db.Model(&model.OperatorToken{}).Where("id = ?", row.ID).Update("expires_at", nil)
	if code, _ := send(t, mux, session{token: tok}, http.MethodGet, "/api/clients", ""); code != http.StatusOK {
		t.Fatalf("the same token with no end: %d", code)
	}
	if code, _ := send(t, mux, sess["ra"], http.MethodDelete, "/api/operator/tokens/"+jsonNum(row.ID), ""); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := send(t, mux, session{token: tok}, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("a revoked token: %d", code)
	}
	if code, _ := send(t, mux, session{token: "wuo_" + strings.Repeat("A", 43)}, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("a made-up token: %d", code)
	}
	rc := idOf(t, db, "rc")
	tok2 := issueFor(t, mux, sess["owner"], rc, `{"name":"shop"}`)
	if code, _ := send(t, mux, sess["owner"], http.MethodDelete, "/api/admins/"+jsonNum(rc)+"?clients=keep", ""); code != http.StatusOK {
		t.Fatalf("removing rc: %d", code)
	}
	var left int64
	db.Model(&model.OperatorToken{}).Where("admin_id = ?", rc).Count(&left)
	if left != 0 {
		t.Errorf("a removed operator's token is kept: %d left", left)
	}
	if code, _ := send(t, mux, session{token: tok2}, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("a removed operator's token: %d", code)
	}
}

// Who sees and revokes whose: the owner everyone's, an admin their own and
// their resellers', a reseller their own. One not theirs to see is not
// found, so whether it exists is not told.
func TestWhoSeesWhoseTokens(t *testing.T) {
	mux, sess, db := serversPanel(t)
	tokensFor(t, mux, sess, db, 5)
	issueFor(t, mux, sess["owner"], idOf(t, db, "ra"), `{"name":"of-ra"}`) // id 1
	issueFor(t, mux, sess["ad"], idOf(t, db, "rc"), `{"name":"of-rc"}`)    // id 2
	issueFor(t, mux, sess["ad"], 0, `{"name":"of-ad"}`)                    // id 3
	names := func(who string) string {
		_, body := send(t, mux, sess[who], http.MethodGet, "/api/operator/tokens", "")
		var got struct {
			Items []model.OperatorToken `json:"items"`
		}
		json.Unmarshal([]byte(body), &got)
		var out []string
		for _, tk := range got.Items {
			out = append(out, tk.Name)
		}
		if strings.Contains(body, `"hash"`) {
			t.Errorf("%s's list carries a hash: %s", who, body)
		}
		return strings.Join(sortedCopy(out), ",")
	}
	for who, want := range map[string]string{
		"owner": "of-ad,of-ra,of-rc",
		"ad":    "of-ad,of-rc",
		"ra":    "of-ra",
		"rc":    "of-rc",
	} {
		if got := names(who); got != want {
			t.Errorf("%s sees %q, want %q", who, got, want)
		}
	}
	if code, _ := send(t, mux, sess["rc"], http.MethodDelete, "/api/operator/tokens/1", ""); code != http.StatusNotFound {
		t.Errorf("rc revoking ra's token: %d", code)
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodDelete, "/api/operator/tokens/1", ""); code != http.StatusNotFound {
		t.Errorf("ad revoking ra's token, not their reseller: %d", code)
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodDelete, "/api/operator/tokens/2", ""); code != http.StatusOK {
		t.Errorf("ad revoking their reseller rc's token: %d", code)
	}
	if code, _ := send(t, mux, sess["owner"], http.MethodDelete, "/api/operator/tokens/1", ""); code != http.StatusOK {
		t.Errorf("the owner revoking ra's token: %d", code)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
