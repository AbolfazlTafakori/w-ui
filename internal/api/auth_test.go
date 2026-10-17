package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/totp"
)

// Signing in, and the second factor: who gets a session, who does not, and
// what a stolen password, a code read over a shoulder or a borrowed session
// can still do.

func authServer(t *testing.T) (*Server, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{db: db, log: log, throttle: newThrottle(), jwtSecret: []byte("a test session key, 32 bytes!!!"),
		admins: service.NewAdmins(db, log)}, db
}

func admin(t *testing.T, db *gorm.DB, a model.Admin, password string) *model.Admin {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	a.PasswordHash = string(hash)
	if a.Role == "" {
		a.Role = model.RoleOwner
	}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	return &a
}

func login(s *Server, ip, user, pass, code string) (*httptest.ResponseRecorder, map[string]any) {
	body, _ := json.Marshal(loginRequest{Username: user, Password: pass, Code: code})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	r.RemoteAddr = ip + ":40000"
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func asAdmin(a *model.Admin, method, path string, body any) *http.Request {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	return r.WithContext(context.WithValue(r.Context(), ctxAdmin, a))
}

func TestTheRightPasswordGetsABoundSession(t *testing.T) {
	s, db := authServer(t)
	admin(t, db, model.Admin{Username: "owner"}, "a long password")
	w, out := login(s, "203.0.113.1", "owner", "a long password", "")
	if w.Code != http.StatusOK || out["token"] == "" || out["token"] == nil {
		t.Fatalf("%d %v", w.Code, out)
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("the session came without its cookie")
	}
	// The token and its cookie are a session; the token alone is not.
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+out["token"].(string))
	if a, _, _, _ := s.authenticate(r); a != nil {
		t.Fatal("a token without its cookie was a session")
	}
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	if a, _, reason, _ := s.authenticate(r); a == nil || a.Username != "owner" {
		t.Fatalf("its own session was refused: %s", reason)
	}
}

func TestAWrongPasswordAndAnUnknownNameLookTheSame(t *testing.T) {
	s, db := authServer(t)
	admin(t, db, model.Admin{Username: "owner"}, "a long password")
	w1, _ := login(s, "203.0.113.2", "owner", "wrong", "")
	w2, _ := login(s, "203.0.113.3", "nobody", "wrong", "")
	if w1.Code != http.StatusUnauthorized || w2.Code != http.StatusUnauthorized || w1.Body.String() != w2.Body.String() {
		t.Fatalf("they differ: %d %q / %d %q", w1.Code, w1.Body, w2.Code, w2.Body)
	}
}

// Guessing is slowed down: after a few wrong passwords the address is
// refused before the password is even looked at.
func TestGuessingIsSlowedDown(t *testing.T) {
	s, db := authServer(t)
	admin(t, db, model.Admin{Username: "owner"}, "a long password")
	var last *httptest.ResponseRecorder
	for i := 0; i < 12; i++ {
		last, _ = login(s, "203.0.113.4", "owner", "wrong", "")
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Fatalf("still guessing freely: %d", last.Code)
	}
	if w, _ := login(s, "203.0.113.4", "owner", "a long password", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the right password from a locked-out address: %d", w.Code)
	}
}

func TestASwitchedOffAccountIsRefusedOnlyAfterItsPassword(t *testing.T) {
	s, db := authServer(t)
	r := admin(t, db, model.Admin{Username: "reza", Role: model.RoleReseller}, "a long password")
	// Switched off after it is made: false is the column's default's opposite,
	// and a create would put the default back.
	if err := db.Model(&model.Admin{}).Where("id = ?", r.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if w, _ := login(s, "203.0.113.5", "reza", "wrong", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password told it was switched off: %d", w.Code)
	}
	if w, _ := login(s, "203.0.113.5", "reza", "a long password", ""); w.Code != http.StatusForbidden {
		t.Fatalf("switched off, let in: %d", w.Code)
	}
}

// With a second factor the password alone is not enough, a code is good
// once, and a wrong code counts as a failed attempt.
func TestTheSecondFactorIsAskedForAndUsedOnce(t *testing.T) {
	s, db := authServer(t)
	secret, _ := totp.NewSecret()
	admin(t, db, model.Admin{Username: "owner", TOTPSecret: secret}, "a long password")

	w, out := login(s, "203.0.113.6", "owner", "a long password", "")
	if w.Code != http.StatusOK || out["needCode"] != true || out["token"] != nil {
		t.Fatalf("no code asked for: %v", out)
	}
	if w, _ := login(s, "203.0.113.6", "owner", "wrong", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password was asked for a code: %d", w.Code)
	}
	code, _ := totp.Generate(secret, time.Now())
	if w, out := login(s, "203.0.113.7", "owner", "a long password", code); w.Code != http.StatusOK || out["token"] == nil {
		t.Fatalf("the right code: %d %v", w.Code, out)
	}
	if w, _ := login(s, "203.0.113.8", "owner", "a long password", code); w.Code != http.StatusUnauthorized {
		t.Fatalf("the same code a second time: %d", w.Code)
	}
	for i := 0; i < 12; i++ {
		w, _ = login(s, "203.0.113.9", "owner", "a long password", "000000")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("wrong codes are not slowed down: %d", w.Code)
	}
}

// A second factor is stored only once the app has shown the right code; it
// is removed only with the password or a fresh code, and wrong proofs are
// slowed down.
func TestTheSecondFactorIsEnrolledAndRemovedWithProof(t *testing.T) {
	s, db := authServer(t)
	a := admin(t, db, model.Admin{Username: "owner"}, "a long password")

	w := httptest.NewRecorder()
	s.handleTOTPStart(w, asAdmin(a, http.MethodPost, "/api/auth/totp/start", nil))
	var start totpStartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil || start.Secret == "" {
		t.Fatalf("start: %v %q", err, w.Body)
	}
	stored := func() string { var x model.Admin; db.First(&x, a.ID); return x.TOTPSecret }
	if stored() != "" {
		t.Fatal("stored before it was confirmed")
	}
	w = httptest.NewRecorder()
	s.handleTOTPConfirm(w, asAdmin(a, http.MethodPost, "/api/auth/totp/confirm", totpConfirmRequest{Secret: start.Secret, Code: "000000"}))
	if w.Code != http.StatusBadRequest || stored() != "" {
		t.Fatalf("a wrong code enrolled it: %d", w.Code)
	}
	code, _ := totp.Generate(start.Secret, time.Now())
	w = httptest.NewRecorder()
	s.handleTOTPConfirm(w, asAdmin(a, http.MethodPost, "/api/auth/totp/confirm", totpConfirmRequest{Secret: start.Secret, Code: code}))
	if w.Code != http.StatusOK || stored() != start.Secret {
		t.Fatalf("the right code did not enrol it: %d", w.Code)
	}

	off := func(p totpDisableRequest) int {
		w := httptest.NewRecorder()
		s.handleTOTPDisable(w, asAdmin(a, http.MethodPost, "/api/auth/totp/disable", p))
		return w.Code
	}
	if c := off(totpDisableRequest{}); c != http.StatusForbidden || stored() == "" {
		t.Fatalf("removed with no proof: %d", c)
	}
	if c := off(totpDisableRequest{Password: "wrong"}); c != http.StatusForbidden || stored() == "" {
		t.Fatalf("removed with a wrong password: %d", c)
	}
	if c := off(totpDisableRequest{Password: "a long password"}); c != http.StatusOK || stored() != "" {
		t.Fatalf("the password did not remove it: %d", c)
	}

	// And a borrowed session guessing codes is stopped.
	db.Model(&model.Admin{}).Where("id = ?", a.ID).Update("totp_secret", start.Secret)
	var c int
	for i := 0; i < 12; i++ {
		c = off(totpDisableRequest{Code: "000000"})
	}
	if c != http.StatusTooManyRequests || stored() == "" {
		t.Fatalf("codes guessed freely: %d", c)
	}
}

// A reseller's term on hold starts at their first sign-in, once.
func TestAResellersTermStartsAtFirstSignIn(t *testing.T) {
	s, db := authServer(t)
	a := admin(t, db, model.Admin{Username: "reza", Role: model.RoleReseller, DurationDays: 30}, "a long password")
	if w, _ := login(s, "203.0.113.10", "reza", "a long password", ""); w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	var x model.Admin
	db.First(&x, a.ID)
	if x.ExpiresAt == nil || x.DurationDays != 0 || x.ExpiresAt.Sub(time.Now()) < 29*24*time.Hour {
		t.Fatalf("term %v, days %d", x.ExpiresAt, x.DurationDays)
	}
	first := *x.ExpiresAt
	login(s, "203.0.113.10", "reza", "a long password", "")
	db.First(&x, a.ID)
	if !x.ExpiresAt.Equal(first) {
		t.Fatal("a second sign-in moved the term")
	}
}
