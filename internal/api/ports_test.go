package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// signInAt signs who in through h and returns the session, or the status the
// sign-in answered with.
func signInAt(t *testing.T, h http.Handler, who string) (session, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"`+who+`","password":"servers-test-pass-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return session{}, rec.Code
	}
	var got struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return session{token: got.Token, cookies: rec.Result().Cookies()}, rec.Code
}

// With an owner port set, the owner's routes are not on the operators' port
// at all -- 404 signed in or not, the same as a path that does not exist --
// and each port signs in only its own people.
func TestTheOwnersPortIsTheirsAlone(t *testing.T) {
	mux, _, db := serversPanel(t)
	op, own := ForOperatorPort(mux), ForOwnerPort(mux)

	// The operators' port.
	if _, code := signInAt(t, op, "owner"); code != http.StatusUnauthorized {
		t.Errorf("the owner signs in on the operators' port: %d", code)
	}
	ad, code := signInAt(t, op, "ad")
	if code != http.StatusOK {
		t.Fatalf("an admin on their port: %d", code)
	}
	for _, path := range []string{"/api/nodes", "/api/backups", "/api/outbounds"} {
		for name, s := range map[string]session{"nobody": {}, "ad": ad} {
			if code, _ := send(t, op, s, http.MethodGet, path, ""); code != http.StatusNotFound {
				t.Errorf("%s reaches %s on the operators' port: %d", name, path, code)
			}
		}
	}
	// What is theirs works: their customers, and their resellers.
	for _, path := range []string{"/api/clients", "/api/admins"} {
		if code, body := send(t, op, ad, http.MethodGet, path, ""); code != http.StatusOK {
			t.Errorf("ad on %s: %d %s", path, code, body)
		}
	}

	// The owner's port.
	for _, who := range []string{"ad", "ra", "rc"} {
		if _, code := signInAt(t, own, who); code != http.StatusUnauthorized {
			t.Errorf("%s signs in on the owner's port: %d", who, code)
		}
	}
	owner, code := signInAt(t, own, "owner")
	if code != http.StatusOK {
		t.Fatalf("the owner on their port: %d", code)
	}
	if code, body := send(t, own, owner, http.MethodGet, "/api/backups", ""); code != http.StatusOK {
		t.Errorf("the owner's backups on their port: %d %s", code, body)
	}

	// A session is its port's: the admin's is nothing on the owner's port,
	// and the owner's nothing on the operators'.
	if code, _ := send(t, own, ad, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("ad's session on the owner's port: %d", code)
	}
	if code, _ := send(t, op, owner, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("the owner's session on the operators' port: %d", code)
	}
	for _, c := range owner.cookies {
		for _, d := range ad.cookies {
			if c.Name == d.Name && c.Name != "" {
				t.Errorf("both ports set the cookie %q", c.Name)
			}
		}
	}
	// An operator's token is refused on the owner's port.
	tok := issueFor(t, own, owner, idOf(t, db, "ad"), `{"name":"shop"}`)
	if code, _ := send(t, own, session{token: tok}, http.MethodGet, "/api/clients", ""); code != http.StatusUnauthorized {
		t.Errorf("ad's token on the owner's port: %d", code)
	}
	if code, _ := send(t, op, session{token: tok}, http.MethodGet, "/api/clients", ""); code != http.StatusOK {
		t.Errorf("ad's token on their port: %d", code)
	}
}

// Guessing on the operators' port locks nobody out of the owner's.
func TestEachPortThrottlesItsOwnSignIns(t *testing.T) {
	mux, _, _ := serversPanel(t)
	op, own := ForOperatorPort(mux), ForOwnerPort(mux)
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"username":"owner","password":"wrong-guess"}`))
		req.Header.Set("Content-Type", "application/json")
		op.ServeHTTP(httptest.NewRecorder(), req)
	}
	if _, code := signInAt(t, op, "ad"); code != http.StatusTooManyRequests {
		t.Errorf("the guessing address on the operators' port is not held back: %d", code)
	}
	if _, code := signInAt(t, own, "owner"); code != http.StatusOK {
		t.Errorf("the owner, from the same address, on their own port: %d", code)
	}
}

// With no owner port, one port serves everybody as before.
func TestOnePortIsAsBefore(t *testing.T) {
	mux, sess, _ := serversPanel(t)
	if code, _ := send(t, mux, sess["owner"], http.MethodGet, "/api/settings", ""); code != http.StatusOK {
		t.Errorf("the owner's settings: %d", code)
	}
	if code, _ := send(t, mux, sess["ad"], http.MethodGet, "/api/clients", ""); code != http.StatusOK {
		t.Errorf("ad's customers: %d", code)
	}
}
