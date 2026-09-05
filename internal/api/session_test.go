package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// withBinding builds a token's claims and the cookie that belongs with them.
func withBinding(t *testing.T) (*sessionClaims, *http.Cookie) {
	t.Helper()
	value, fingerprint, err := newBinding()
	if err != nil {
		t.Fatalf("newBinding: %v", err)
	}
	return &sessionClaims{Bind: fingerprint}, &http.Cookie{Name: bindCookie, Value: value}
}

func bound(t *testing.T, cookies ...*http.Cookie) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

// The whole point: the token on its own is not a session.
//
// This is the case that matters — a script that gets to run on the panel's
// origin can read the token out of the page and can never read an HttpOnly
// cookie. Without the cookie the token has to be refused, or the protection is
// decoration.
func TestATokenWithoutItsCookieIsRefused(t *testing.T) {
	claims, _ := withBinding(t)

	if bindingHolds(bound(t), claims) {
		t.Error("a stolen token was accepted with no cookie at all")
	}
}

// And it is the right cookie, not any cookie.
func TestSomebodyElsesCookieDoesNotUnlockAToken(t *testing.T) {
	claims, _ := withBinding(t)
	_, other := withBinding(t)

	if bindingHolds(bound(t, other), claims) {
		t.Error("a token was accepted with a cookie from a different session")
	}
	if bindingHolds(bound(t, &http.Cookie{Name: bindCookie, Value: ""}), claims) {
		t.Error("an empty cookie was treated as a match")
	}
}

// The pair issued together works, which is the ordinary case and must not have
// been broken by any of the above.
func TestTheTokenAndItsOwnCookieWorkTogether(t *testing.T) {
	claims, cookie := withBinding(t)

	if !bindingHolds(bound(t, cookie), claims) {
		t.Error("a session was refused its own cookie")
	}
}

// A token issued before this existed carries no binding and keeps working
// until it expires. Deploying a hardening change should not sign every operator
// out of a running panel; the old tokens age out within hours on their own.
func TestATokenFromBeforeThisIsStillAccepted(t *testing.T) {
	if !bindingHolds(bound(t), &sessionClaims{}) {
		t.Error("an upgrade signed out everybody who was already working")
	}
}

// The cookie must be one a script cannot read, or none of this holds.
func TestTheCookieIsUnreachableFromAScript(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.TLS = &tls.ConnectionState{}

	setBindCookie(w, r, "value", time.Now().Add(time.Hour))

	res := w.Result()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("setBindCookie wrote %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("the cookie is readable by any script that runs on this page")
	}
	if !c.Secure {
		t.Error("the cookie would be sent over plain HTTP")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("the cookie is sent on cross-site requests")
	}
}

// On a panel served over plain HTTP the cookie must not be marked secure, or
// the browser keeps it to itself and every request after sign-in is refused —
// an operator locked out of their own panel by a hardening flag.
func TestAPlainHTTPPanelStillWorks(t *testing.T) {
	restore(t)

	w := httptest.NewRecorder()
	setBindCookie(w, httptest.NewRequest(http.MethodPost, "/api/auth/login", nil),
		"value", time.Now().Add(time.Hour))

	if c := w.Result().Cookies()[0]; c.Secure {
		t.Error("a panel without TLS was given a cookie its browser will never send back")
	}
}

// ── ending a session that somebody else may be holding ──────────────────────

// Changing a password is what an operator does when they think a session is not
// theirs. A token that outlives the change means the intruder keeps working
// while the operator believes they have shut the door.
func TestRaisingTheGenerationEndsAnOlderToken(t *testing.T) {
	admin := &model.Admin{SessionEpoch: 2}

	if epochHolds(admin, &sessionClaims{Epoch: 1}) {
		t.Error("a token from before the password change is still accepted")
	}
	if !epochHolds(admin, &sessionClaims{Epoch: 2}) {
		t.Error("the current session was ended along with the old ones")
	}
}

// The same grace as the binding, and for the same reason.
func TestATokenFromBeforeTheGenerationExistedIsAccepted(t *testing.T) {
	if !epochHolds(&model.Admin{SessionEpoch: 3}, &sessionClaims{}) {
		t.Error("an upgrade signed out everybody who was already working")
	}
}
