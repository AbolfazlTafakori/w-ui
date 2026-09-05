package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/totp"
)

// defaultSessionTTL is how long a sign-in lasts when nothing has been chosen.
// The effective value comes from the settings page.
const defaultSessionTTL = 12 * time.Hour

// sessionTTL is the configured session length.
func (s *Server) sessionTTL(ctx context.Context) time.Duration {
	if s.settings == nil {
		return defaultSessionTTL
	}
	return time.Duration(s.settings.SessionTTLHours(ctx)) * time.Hour
}

type ctxKey int

const ctxAdmin ctxKey = iota

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

type loginResponse struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expiresAt"`
	Admin     *model.Admin `json:"admin"`

	// NeedCode tells the sign-in page to ask for a second factor. It is only
	// ever sent after the password was correct, so it reveals nothing to
	// someone guessing.
	NeedCode bool `json:"needCode,omitempty"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}

	now := time.Now()
	ipKey := "ip:" + clientIP(r)
	userKey := "user:" + strings.ToLower(strings.TrimSpace(req.Username))

	// Checked before the password is looked at, so a locked-out caller costs
	// nothing to refuse.
	for _, key := range []string{ipKey, userKey} {
		if wait := s.throttle.retryAfter(key, now); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, lockoutMessage(wait))
			return
		}
	}

	var admin model.Admin
	err := s.db.WithContext(r.Context()).
		Where("username = ?", strings.TrimSpace(req.Username)).
		First(&admin).Error

	// A wrong username and a wrong password give the same answer, and the hash
	// comparison runs either way, so response timing does not reveal which
	// usernames exist.
	if errors.Is(err, gorm.ErrRecordNotFound) {
		bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidi"), []byte(req.Password))
		s.throttle.fail(ipKey, now)
		s.throttle.fail(userKey, now)
		writeError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}
	if err != nil {
		fail(w, s.log, fmt.Errorf("load admin: %w", err))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		wait := s.throttle.fail(ipKey, now)
		s.throttle.fail(userKey, now)
		s.log.Warn("failed sign-in", "username", admin.Username,
			"ip", clientIP(r), "lockout", wait)
		writeError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}

	// The second factor is checked only once the password is right. Asking for
	// a code before that would tell an attacker which accounts have one, and
	// would let them confirm a username without knowing the password.
	if admin.TOTPSecret != "" {
		if strings.TrimSpace(req.Code) == "" {
			// Only the flag. Sending the empty token and zero date of the full
			// shape would leave a client to distinguish "no session" from a
			// session that expired in year one.
			writeJSON(w, http.StatusOK, map[string]bool{"needCode": true})
			return
		}
		if !totp.Validate(admin.TOTPSecret, req.Code, time.Now()) {
			// A wrong code counts as a failed attempt too. Otherwise someone
			// holding the password could try every one of the million codes
			// without ever being slowed down.
			s.throttle.fail(ipKey, now)
			s.throttle.fail(userKey, now)
			s.log.Warn("failed second factor", "username", admin.Username, "ip", clientIP(r))
			writeError(w, http.StatusUnauthorized, "that code is not right")
			return
		}
	}

	expires := time.Now().Add(s.sessionTTL(r.Context()))
	token, err := s.issueToken(w, r, &admin, expires)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.throttle.succeed(ipKey)
	s.throttle.succeed(userKey)

	ip := clientIP(r)
	if err := s.db.WithContext(r.Context()).Model(&admin).
		Updates(map[string]any{"last_login_at": time.Now().UTC(), "last_login_ip": ip}).Error; err != nil {
		s.log.Error("record sign-in", "error", err)
	}

	s.log.Info("admin signed in", "username", admin.Username, "ip", ip)
	if s.notifier != nil {
		s.notifier.Send(notify.Event{
			Kind:  notify.KindLogin,
			Title: "Panel sign-in",
			Body:  fmt.Sprintf("%s from %s", admin.Username, ip),
		})
	}
	writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expires, Admin: &admin})
}

// issueToken mints a session and the cookie half that has to come with it.
//
// The cookie is set here rather than by the caller so that a token carrying a
// binding can never be handed out without the browser being given the other
// half — which would be an operator signed in and immediately refused.
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request, admin *model.Admin, expires time.Time) (string, error) {
	value, fingerprint, err := newBinding()
	if err != nil {
		return "", err
	}

	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprint(admin.ID),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expires),
			Issuer:    "w-ui",
		},
		Bind:  fingerprint,
		Epoch: max(admin.SessionEpoch, 1),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("api: sign token: %w", err)
	}

	setBindCookie(w, r, value, expires)
	return token, nil
}

// requireAuth rejects requests without a valid bearer token.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if raw == "" || raw == r.Header.Get("Authorization") {
			writeError(w, http.StatusUnauthorized, "your session has ended; sign in again")
			return
		}

		// A machine token, from another panel watching this one. Checked before
		// the JWT because it is not one and would fail that parse with a
		// message about sessions that would send an operator looking in the
		// wrong place.
		if strings.HasPrefix(raw, "wui_") {
			if s.nodes != nil && s.nodes.VerifyToken(r.Context(), raw) {
				next(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, "that access token is not valid")
			return
		}

		claims := &sessionClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			// Pinning the algorithm is what stops a token signed with "none",
			// or with the public half of an asymmetric key, from being accepted.
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
			}
			return s.jwtSecret, nil
		}, jwt.WithIssuer("w-ui"), jwt.WithExpirationRequired())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session expired, sign in again")
			return
		}

		// The token alone is not a session. Without the cookie it names, this is
		// a token that left the browser it was issued to.
		if !bindingHolds(r, claims) {
			writeError(w, http.StatusUnauthorized, "your session has ended; sign in again")
			return
		}

		var admin model.Admin
		if err := s.db.WithContext(r.Context()).First(&admin, claims.Subject).Error; err != nil {
			writeError(w, http.StatusUnauthorized, "your session has ended; sign in again")
			return
		}

		// Signed out everywhere since this was issued — by a password change, or
		// deliberately.
		if !epochHolds(&admin, claims) {
			clearBindCookie(w, r)
			writeError(w, http.StatusUnauthorized, "you were signed out everywhere; sign in again")
			return
		}

		ctx := context.WithValue(r.Context(), ctxAdmin, &admin)
		next(w, r.WithContext(ctx))
	}
}

// adminFrom returns the signed-in admin attached by requireAuth.
func adminFrom(ctx context.Context) *model.Admin {
	admin, _ := ctx.Value(ctxAdmin).(*model.Admin)
	return admin
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	// The secret itself is never serialised. Only whether one is set, which is
	// what the settings page needs to know to show the right control.
	writeJSON(w, http.StatusOK, struct {
		*model.Admin
		TwoFactor bool `json:"twoFactor"`
	}{Admin: admin, TwoFactor: admin.TOTPSecret != ""})
}

// Enrolling a second factor.
//
// The secret is generated, shown once, and only stored after the operator has
// proved their app produces the right code. Storing it before that would let
// someone lock themselves out by scanning a code their app never accepted.

type totpStartResponse struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

func (s *Server) handleTOTPStart(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	secret, err := totp.NewSecret()
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, totpStartResponse{
		Secret: secret,
		URI:    totp.URI("W-UI", admin.Username, secret),
	})
}

type totpConfirmRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	var req totpConfirmRequest
	if !decode(w, r, &req) {
		return
	}
	if !totp.Validate(req.Secret, req.Code, time.Now()) {
		writeError(w, http.StatusBadRequest,
			"that code is not right. Check your phone's clock is correct and try the next one.")
		return
	}

	err := s.db.WithContext(r.Context()).Model(&model.Admin{}).
		Where("id = ?", admin.ID).Update("totp_secret", req.Secret).Error
	if err != nil {
		fail(w, s.log, fmt.Errorf("store second factor: %w", err))
		return
	}

	s.log.Info("second factor enabled", "username", admin.Username)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

type totpDisableRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	var req totpDisableRequest
	if !decode(w, r, &req) {
		return
	}

	// The password is asked for again. Otherwise a borrowed open session -
	// which is exactly what the second factor exists to survive - could remove
	// it in one click.
	var stored model.Admin
	if err := s.db.WithContext(r.Context()).First(&stored, admin.ID).Error; err != nil {
		fail(w, s.log, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "that password is not right")
		return
	}

	err := s.db.WithContext(r.Context()).Model(&model.Admin{}).
		Where("id = ?", admin.ID).Update("totp_secret", "").Error
	if err != nil {
		fail(w, s.log, fmt.Errorf("remove second factor: %w", err))
		return
	}

	s.log.Warn("second factor disabled", "username", admin.Username, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}
