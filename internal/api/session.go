package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Making a stolen session token worth less, and making one endable.
//
// The token the page holds is readable by any script that manages to run on
// this origin. The policy this panel serves makes that hard — nothing inline,
// nothing from anywhere else — but "hard" is not "cannot", and a token on its
// own bought twelve hours of full administrative access from anywhere in the
// world.
//
// The obvious answer is to move the session into an HttpOnly cookie, which a
// script cannot read. The cost is that cookies are sent by the browser without
// being asked, so every state-changing request then needs cross-site request
// forgery protection of its own — a change reaching every endpoint and the
// whole front end, on a panel already in service.
//
// This gets the same protection for far less. The session stays a bearer token
// in the Authorization header, exactly as before, and at sign-in a second
// random value is put in an HttpOnly cookie with a fingerprint of it inside the
// signed token. Both halves are now required. A script can read the token and
// cannot read the cookie; a cross-site request carries the cookie and cannot
// set the header. Neither half is any use alone, and nothing else had to change
// to get there.

const (
	// bindCookie carries the half a script cannot read.
	bindCookie = "wui_bind"

	// bindBytes is the size of that half. Long enough that guessing it is not a
	// strategy, short enough to sit in a header without comment.
	bindBytes = 32
)

// sessionClaims is what this panel puts in a session token.
type sessionClaims struct {
	jwt.RegisteredClaims

	// Bind is the fingerprint of the cookie that must accompany this token.
	// Absent on tokens issued before this existed, which stay usable until they
	// expire rather than signing everybody out on an upgrade.
	Bind string `json:"bnd,omitempty"`

	// Epoch is the sign-out generation the token was minted under. A token from
	// an older generation is refused, which is what makes changing a password
	// end the sessions somebody else may be holding.
	Epoch int `json:"epc,omitempty"`
}

// newBinding returns the value for the cookie and the fingerprint for the token.
//
// Only the fingerprint is signed into the token: the token is the half that can
// be stolen, and it should not carry the other half inside it.
func newBinding() (value, fingerprint string, err error) {
	raw := make([]byte, bindBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("api: generate session binding: %w", err)
	}
	value = base64.RawURLEncoding.EncodeToString(raw)
	return value, fingerprintOf(value), nil
}

func fingerprintOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// setBindCookie puts the unreadable half in the browser.
func setBindCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:  bindCookie,
		Value: value,
		// The whole host. The panel may be mounted under a secret prefix, and a
		// cookie scoped to that prefix would put the prefix in every request's
		// Cookie header for anything else on the name — which is the one thing
		// the prefix exists to keep quiet.
		Path:     "/",
		HttpOnly: true,
		// Same site only. This is not what stops forgery here — the
		// Authorization header does that — but a cookie that never leaves its
		// own site is a cookie with fewer ways to go wrong.
		SameSite: http.SameSiteStrictMode,
		// Only over TLS, and only when we can see that there is TLS. Marking it
		// secure on a panel reached over plain HTTP would mean the browser never
		// sends it back, and every request after sign-in would be refused.
		Secure:  clientScheme(r) == "https",
		Expires: expires,
	})
}

// clearBindCookie ends the browser's half of a session.
func clearBindCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     bindCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   clientScheme(r) == "https",
		MaxAge:   -1,
	})
}

// bindingHolds reports whether the request carries the cookie this token names.
//
// A token with no binding is one issued before this existed. It is accepted
// until it expires — a few hours at most — because signing every operator out
// of a running panel to deploy a hardening change is its own kind of incident.
func bindingHolds(r *http.Request, claims *sessionClaims) bool {
	if claims.Bind == "" {
		return true
	}
	c, err := r.Cookie(bindCookie)
	if err != nil || c.Value == "" {
		return false
	}
	// Constant time, because the comparison is against a value an attacker
	// holding the token would like to search for.
	return subtle.ConstantTimeCompare([]byte(fingerprintOf(c.Value)), []byte(claims.Bind)) == 1
}

// epochHolds reports whether the token is from the current generation.
//
// Zero is a token from before the field existed and is accepted for the same
// reason. Once an operator changes their password or signs out everywhere the
// generation moves on, and those tokens go with it.
func epochHolds(admin *model.Admin, claims *sessionClaims) bool {
	if claims.Epoch == 0 {
		return true
	}
	return claims.Epoch == admin.SessionEpoch
}
