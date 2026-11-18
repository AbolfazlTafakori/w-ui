package api

import (
	"context"
	"net/http"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The owner's own port.
//
// By default the panel listens on one port and everybody signs in there.
// The owner can give themselves a port of their own (WUI_OWNER_LISTEN, or the
// owner's settings page): the panel's usual port is then the operators', and
// the owner's pages are not there at all.
//
//   - On the operators' port every route only the owner may use answers 404,
//     signed in or not, as if it did not exist; the owner cannot sign in
//     there, and a machine token is not taken there.
//   - On the owner's port only the owner signs in; an admin's or reseller's
//     password, session or token is refused.
//   - Each port has its own session cookie and its own sign-in throttle, so a
//     session from one is nothing on the other and guessing on the
//     operators' port never locks the owner out of theirs.
//
// With no owner port set, nothing here changes anything.

type portRole int

const (
	// portBoth is the single port everybody uses: no owner port is set.
	portBoth portRole = iota
	portOwner
	portOperator
)

type ctxPortKey struct{}

// ForOwnerPort marks every request through h as arriving on the owner's port.
func ForOwnerPort(h http.Handler) http.Handler { return withPort(portOwner, h) }

// ForOperatorPort marks every request through h as arriving on the
// operators' port: the panel's usual port once the owner has one of their own.
func ForOperatorPort(h http.Handler) http.Handler { return withPort(portOperator, h) }

func withPort(role portRole, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxPortKey{}, role)))
	})
}

func portOf(r *http.Request) portRole {
	role, _ := r.Context().Value(ctxPortKey{}).(portRole)
	return role
}

// bindCookieName is the session cookie on the port the request came in on.
func bindCookieName(r *http.Request) string {
	switch portOf(r) {
	case portOwner:
		return bindCookie + "_owner"
	case portOperator:
		return bindCookie + "_op"
	}
	return bindCookie
}

// throttleScope keeps each port's sign-in throttle apart.
func throttleScope(r *http.Request) string {
	switch portOf(r) {
	case portOwner:
		return "owner|"
	case portOperator:
		return "op|"
	}
	return ""
}

// signsInHere reports whether this operator may sign in, or use a session or
// token, on the port the request came in on.
func signsInHere(r *http.Request, a *model.Admin) bool {
	switch portOf(r) {
	case portOwner:
		return a.Role == model.RoleOwner
	case portOperator:
		return a.Role != model.RoleOwner
	}
	return true
}

// ownerPortOnly hides an owner's route on the operators' port: 404, before
// anything else is asked, exactly what a path that does not exist answers.
func ownerPortOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if portOf(r) == portOperator {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}
