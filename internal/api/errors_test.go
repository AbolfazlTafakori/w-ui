package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/service"
)

// The answers a caller gets for a request the panel cannot act on: each
// exact, each with the status that belongs to it, and each the one the
// reference page documents.

func body(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %q", rec.Body.String())
	}
	return m
}

func TestABodyThatIsNotJSONIsRefusedPlainly(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader("{not json"))
	var dst struct{}
	if decode(rec, r, &dst) {
		t.Fatal("decoded")
	}
	if rec.Code != http.StatusBadRequest || body(t, rec)["error"] != "the request body could not be read as JSON" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAnEmptyBodyIsRefusedPlainly(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(""))
	var dst struct{}
	if decode(rec, r, &dst) {
		t.Fatal("decoded")
	}
	if rec.Code != http.StatusBadRequest || body(t, rec)["error"] != "the request had no body" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAnOversizedBodyIsRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	// A well-formed document that is simply too big: one string of two megabytes.
	r := httptest.NewRequest(http.MethodPost, "/api/clients", io.MultiReader(strings.NewReader(`{"note":"`), io.LimitReader(zeros{}, 2<<20), strings.NewReader(`"}`)))
	var dst map[string]any
	if decode(rec, r, &dst) {
		t.Fatal("decoded")
	}
	if rec.Code != http.StatusRequestEntityTooLarge || body(t, rec)["error"] != "that request is too large" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// fail() is the one place a service error becomes an HTTP answer.
func TestServiceErrorsBecomeTheRightStatus(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name   string
		err    error
		status int
		msg    string
		field  string
	}{
		{"invalid with field", &service.FieldError{Field: "name", Err: fmt.Errorf("%w: name is required", service.ErrInvalid)}, 400, "Name is required", "name"},
		{"invalid without field", fmt.Errorf("%w: panel port 70000 is out of range", service.ErrInvalid), 400, "Panel port 70000 is out of range", ""},
		{"not found", fmt.Errorf("%w: interface 99", service.ErrNotFound), 404, "Interface 99", ""},
		{"anything else", errors.New("disk on fire"), 500, "internal error", ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		fail(rec, quiet, c.err)
		got := body(t, rec)
		if rec.Code != c.status || got["error"] != c.msg {
			t.Errorf("%s: %d %q, want %d %q", c.name, rec.Code, got["error"], c.status, c.msg)
		}
		if c.field != "" && got["field"] != c.field {
			t.Errorf("%s: field %v, want %q", c.name, got["field"], c.field)
		}
	}
}

func TestARequestWithNoTokenIsToldToSignIn(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	s.requireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("let through") })(rec, r)
	if rec.Code != http.StatusUnauthorized || body(t, rec)["error"] != "your session has ended; sign in again" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAMachineTokenNobodyIssuedIsRefused(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	r.Header.Set("Authorization", "Bearer wui_notreal")
	s.requireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("let through") })(rec, r)
	if rec.Code != http.StatusUnauthorized || body(t, rec)["error"] != "that access token is not valid" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAGarbledSessionTokenIsExpired(t *testing.T) {
	s := &Server{jwtSecret: []byte("k")}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	r.Header.Set("Authorization", "Bearer not.a.jwt")
	s.requireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("let through") })(rec, r)
	if rec.Code != http.StatusUnauthorized || body(t, rec)["error"] != "session expired, sign in again" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}
