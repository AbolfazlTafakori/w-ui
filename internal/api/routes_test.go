package api

import (
	"net/http"
	"strings"
	"testing"
)

// The table is the only description of this API. These check it stays usable
// as one, because a wrong entry here is documentation that lies rather than an
// endpoint that breaks — the kind of mistake nobody notices for months.

func newRouteServer() *Server { return &Server{} }

func TestEveryRouteIsDescribedAndReachable(t *testing.T) {
	for _, r := range newRouteServer().routes() {
		if r.handler == nil {
			t.Errorf("%s %s has no handler", r.Method, r.Path)
		}
		if strings.TrimSpace(r.Summary) == "" {
			t.Errorf("%s %s has no summary; it would appear in the docs as a bare path",
				r.Method, r.Path)
		}
		if r.Group == "" {
			t.Errorf("%s %s has no group and would not appear under any heading",
				r.Method, r.Path)
		}
		if !strings.HasPrefix(r.Path, "/api/") {
			t.Errorf("%s %s is outside /api/", r.Method, r.Path)
		}
	}
}

func TestNoRouteIsRegisteredTwice(t *testing.T) {
	// net/http panics on a duplicate pattern, which would take the panel down
	// at startup rather than at a test.
	seen := map[string]bool{}
	for _, r := range newRouteServer().routes() {
		key := r.Method + " " + r.Path
		if seen[key] {
			t.Errorf("%s is registered twice", key)
		}
		seen[key] = true
	}
}

func TestOnlySigningInIsOpen(t *testing.T) {
	// Anything reachable without a token is reachable by anyone on the
	// internet. The list of exceptions should be short and deliberate.
	open := map[string]bool{
		"POST /api/auth/login":   true,
		"GET /api/meta":          true,
		"GET /api/i18n/{locale}": true,
	}
	for _, r := range newRouteServer().routes() {
		key := r.Method + " " + r.Path
		if !r.Auth && !open[key] {
			t.Errorf("%s needs no token; add it to the list above only if that is deliberate", key)
		}
		if r.Auth && open[key] {
			t.Errorf("%s is listed as open but requires a token", key)
		}
	}
}

func TestMethodsAreOnesTheMuxUnderstands(t *testing.T) {
	allowed := map[string]bool{
		http.MethodGet: true, http.MethodPost: true,
		http.MethodPatch: true, http.MethodPut: true, http.MethodDelete: true,
	}
	for _, r := range newRouteServer().routes() {
		if !allowed[r.Method] {
			t.Errorf("%s %s uses an unexpected method", r.Method, r.Path)
		}
	}
}

func TestRoutesThatTakeABodyShowOne(t *testing.T) {
	// A POST or PATCH documented with no example leaves the caller guessing at
	// field names, which is the one thing the page exists to prevent.
	exempt := map[string]bool{
		"POST /api/clients/{id}/reset":               true, // no body
		"POST /api/clients/reset-all":                true,
		"POST /api/backups":                          true,
		"POST /api/settings/notify/test":             true,
		"POST /api/auth/totp/start":                  true,
		"POST /api/nodes/{id}/probe":                 true, // asks a node; nothing to send
		"POST /api/interfaces/{id}/restart":          true,
		"POST /api/outbounds/{id}/check":             true, // measures; nothing to send
		"POST /api/outbounds/check":                  true,
		"POST /api/hosts/{id}/check":                 true,
		"POST /api/clients/{id}/subscription/rotate": true, // acts on the id in the path
	}
	for _, r := range newRouteServer().routes() {
		if r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodPut {
			continue
		}
		key := r.Method + " " + r.Path
		if r.Body == "" && !exempt[key] {
			t.Errorf("%s takes a body but documents none", key)
		}
	}
}

func TestPathParametersUseTheMuxSyntax(t *testing.T) {
	for _, r := range newRouteServer().routes() {
		if strings.Contains(r.Path, ":") {
			t.Errorf("%s %s uses a colon parameter; this mux wants {name}", r.Method, r.Path)
		}
	}
}
