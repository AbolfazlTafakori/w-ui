package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// What each operator changed is kept for the admin over them, and for
// themselves when granted: an admin sees their own and their resellers', a
// reseller their own, the owner everyone's; nobody else's, and never what
// was sent -- no password, no token, no query.
func TestActivityIsTheTreesOwnAndCarriesNoSecrets(t *testing.T) {
	mux, sess, db := serversPanel(t)
	if err := db.Create(&model.AdminMenuPermission{AdminID: idOf(t, db, "ra"), Menu: model.MenuActivity}).Error; err != nil {
		t.Fatal(err)
	}

	// ra resets their own customer, and tries another's.
	send(t, mux, sess["ra"], http.MethodPost, "/api/clients/3/reset?secret=QUERY-SECRET", `{"password":"BODY-SECRET"}`)
	send(t, mux, sess["ra"], http.MethodPost, "/api/clients/5/reset", "")
	// rc, under ad, resets theirs with a token the owner issued them.
	tok := issueFor(t, mux, sess["owner"], idOf(t, db, "rc"), `{"name":"shop"}`)
	send(t, mux, session{token: tok}, http.MethodPost, "/api/clients/7/reset", "")
	// ad changes their own password; the owner resets a customer.
	send(t, mux, sess["ad"], http.MethodPost, "/api/auth/password", `{"currentPassword":"wrong-password-1","newPassword":"PASSWORD-SECRET-9"}`)
	send(t, mux, sess["owner"], http.MethodPost, "/api/clients/1/reset", "")

	type row struct {
		Operator string `json:"operator"`
		Action   string `json:"action"`
		Status   int    `json:"status"`
		ViaToken bool   `json:"viaToken"`
	}
	read := func(who string) ([]row, string) {
		t.Helper()
		code, body := send(t, mux, sess[who], http.MethodGet, "/api/operator/activity", "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", who, code, body)
		}
		var out []row
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out, body
	}
	whom := func(rows []row) string {
		seen := map[string]bool{}
		var out []string
		for _, r := range rows {
			if !seen[r.Operator] {
				seen[r.Operator] = true
				out = append(out, r.Operator)
			}
		}
		return strings.Join(sortedCopy(out), ",")
	}

	raRows, raBody := read("ra")
	if whom(raRows) != "ra" || len(raRows) != 2 {
		t.Errorf("ra sees %+v", raRows)
	}
	// The refused reset is kept too, with how it ended.
	statuses := map[int]bool{}
	for _, r := range raRows {
		statuses[r.Status] = true
	}
	if !statuses[http.StatusOK] || !statuses[http.StatusNotFound] {
		t.Errorf("ra's statuses: %+v", raRows)
	}
	adRows, _ := read("ad")
	if whom(adRows) != "ad,rc" {
		t.Errorf("ad sees %q, want ad and rc", whom(adRows))
	}
	viaToken := map[string]bool{}
	for _, r := range adRows {
		if r.Operator == "rc" {
			viaToken[r.Action] = r.ViaToken
		}
	}
	// The token was made signed in; the reset was made with it.
	if !viaToken["POST /api/clients/7/reset"] || viaToken["POST /api/operator/tokens"] {
		t.Errorf("rc's changes, by whether a token made them: %v", viaToken)
	}
	ownerRows, _ := read("owner")
	if whom(ownerRows) != "ad,ra,rc" {
		t.Errorf("the owner sees %q; their own changes are in the log, not here", whom(ownerRows))
	}

	var all []model.OperatorAction
	db.Find(&all)
	raw, _ := json.Marshal(all)
	for _, secret := range []string{"QUERY-SECRET", "BODY-SECRET", "PASSWORD-SECRET", "wrong-password", tok, "wuo_"} {
		if strings.Contains(string(raw), secret) || strings.Contains(raBody, secret) {
			t.Errorf("the record carries %q", secret)
		}
	}
}

// Granted, not given: rb (not granted) and an admin with no page are
// refused; nobody signed in is told to sign in.
func TestActivityIsGranted(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	for _, who := range []string{"rb", "ra", "an"} {
		if code, body := send(t, mux, sess[who], http.MethodGet, "/api/operator/activity", ""); code != http.StatusForbidden {
			t.Errorf("%s: %d %s", who, code, body)
		}
	}
	if code, _ := send(t, mux, session{}, http.MethodGet, "/api/operator/activity", ""); code != http.StatusUnauthorized {
		t.Errorf("nobody: %d", code)
	}
}
