package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

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

// issue makes a token as a signed-in operator and returns the secret.
func issue(t *testing.T, mux http.Handler, sess session, body string) string {
	t.Helper()
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

// A reseller's token reaches what the reseller does and nothing more: their
// own customer, not another's (404, as for the reseller signed in), and none
// of what only someone at the keyboard does -- their password, the
// operators, the settings, another token.
func TestATokenCannotReachPastItsOperator(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	tok := session{token: issue(t, mux, sess["ra"], `{"name":"shop"}`)}

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
	tok := session{token: issue(t, mux, sess["rb"], `{"name":"shop"}`)}
	rb := idOf(t, db, "rb")

	_, body := send(t, mux, tok, http.MethodGet, "/api/operator/servers", "")
	if !strings.Contains(body, "wg-b") {
		t.Fatalf("rb's servers before: %s", body)
	}
	// The owner takes wg-b away from them.
	if err := db.Where("admin_id = ? AND interface_id = ?", rb, 2).Delete(&model.AdminInterface{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, body := send(t, mux, tok, http.MethodGet, "/api/operator/servers", ""); strings.Contains(body, "wg-b") {
		t.Errorf("the token still reaches a server taken away: %s", body)
	}
	// And switches them off.
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
	tok := issue(t, mux, sess["ra"], `{"name":"shop"}`)
	var row model.OperatorToken
	if err := db.Where("admin_id = ?", idOf(t, db, "ra")).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Hash == tok || strings.Contains(row.Hash, tok[4:]) {
		t.Fatal("the secret is stored as it is")
	}
	past := time.Now().UTC().Add(-time.Minute)
	db.Model(&model.OperatorToken{}).Where("id = ?", row.ID).Update("expires_at", past)
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
	tok2 := issue(t, mux, sess["rc"], `{"name":"shop"}`)
	if code, _ := send(t, mux, sess["owner"], http.MethodDelete, "/api/admins/"+jsonNum(idOf(t, db, "rc"))+"?clients=keep", ""); code != http.StatusOK {
		t.Fatalf("removing rc: %d", code)
	}
	var left int64
	db.Model(&model.OperatorToken{}).Count(&left)
	if left != 0 {
		t.Errorf("a removed operator's token is kept: %d left", left)
	}
	if code, _ := send(t, mux, session{token: tok2}, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("a removed operator's token: %d", code)
	}
}

// Five at most, a year at most, and the owner issues machine tokens, not
// these.
func TestTokenLimits(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	for i := 0; i < 5; i++ {
		issue(t, mux, sess["ra"], `{"name":"t"}`)
	}
	if code, body := send(t, mux, sess["ra"], http.MethodPost, "/api/operator/tokens", `{"name":"sixth"}`); code != http.StatusBadRequest {
		t.Errorf("a sixth token: %d %s", code, body)
	}
	if code, _ := send(t, mux, sess["rb"], http.MethodPost, "/api/operator/tokens", `{"name":"long","days":400}`); code != http.StatusBadRequest {
		t.Errorf("a token for 400 days: %d", code)
	}
	if code, _ := send(t, mux, sess["rb"], http.MethodPost, "/api/operator/tokens", `{"name":""}`); code != http.StatusBadRequest {
		t.Errorf("a token with no name: %d", code)
	}
	if code, _ := send(t, mux, sess["owner"], http.MethodPost, "/api/operator/tokens", `{"name":"mine"}`); code != http.StatusBadRequest {
		t.Errorf("the owner issuing an operator token: %d", code)
	}
	_, body := send(t, mux, sess["rb"], http.MethodPost, "/api/operator/tokens", `{"name":"forever","days":0}`)
	if !strings.Contains(body, `"expiresAt":null`) {
		t.Errorf("days 0 should be no end: %s", body)
	}
	_, body = send(t, mux, sess["rb"], http.MethodPost, "/api/operator/tokens", `{"name":"default"}`)
	var got struct {
		Meta model.OperatorToken `json:"meta"`
	}
	json.Unmarshal([]byte(body), &got)
	if got.Meta.ExpiresAt == nil || got.Meta.ExpiresAt.Sub(time.Now()) < 89*24*time.Hour {
		t.Errorf("no days given should be 90: %s", body)
	}
}

// Who sees and revokes whose: the owner everyone's, an admin their own and
// their resellers', a reseller their own. One not theirs to see is not
// found, so whether it exists is not told.
func TestWhoSeesWhoseTokens(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	for _, who := range []string{"ra", "rc", "ad"} {
		issue(t, mux, sess[who], `{"name":"of-`+who+`"}`)
	}
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
		if strings.Contains(body, "wuo_") && strings.Count(body, "wuo_") != len(got.Items) {
			t.Errorf("%s's list carries more than prefixes: %s", who, body)
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
		"rb":    "",
	} {
		if got := names(who); got != want {
			t.Errorf("%s sees %q, want %q", who, got, want)
		}
	}
	// ra's token is id 1, rc's 2, ad's 3.
	if code, _ := send(t, mux, sess["rb"], http.MethodDelete, "/api/operator/tokens/1", ""); code != http.StatusNotFound {
		t.Errorf("rb revoking ra's token: %d", code)
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
