package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// sessionTTL is how long a sign-in lasts.
const sessionTTL = 12 * time.Hour

type ctxKey int

const ctxAdmin ctxKey = iota

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expiresAt"`
	Admin     *model.Admin `json:"admin"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
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
		writeError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}
	if err != nil {
		fail(w, s.log, fmt.Errorf("load admin: %w", err))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		s.log.Warn("failed sign-in", "username", admin.Username, "ip", clientIP(r))
		writeError(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}

	expires := time.Now().Add(sessionTTL)
	token, err := s.issueToken(&admin, expires)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	now := time.Now().UTC()
	ip := clientIP(r)
	if err := s.db.WithContext(r.Context()).Model(&admin).
		Updates(map[string]any{"last_login_at": now, "last_login_ip": ip}).Error; err != nil {
		s.log.Error("record sign-in", "error", err)
	}

	s.log.Info("admin signed in", "username", admin.Username, "ip", ip)
	writeJSON(w, http.StatusOK, loginResponse{Token: token, ExpiresAt: expires, Admin: &admin})
}

func (s *Server) issueToken(admin *model.Admin, expires time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   fmt.Sprint(admin.ID),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(expires),
		Issuer:    "w-ui",
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("api: sign token: %w", err)
	}
	return token, nil
}

// requireAuth rejects requests without a valid bearer token.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if raw == "" || raw == r.Header.Get("Authorization") {
			writeError(w, http.StatusUnauthorized, "sign in to continue")
			return
		}

		claims := &jwt.RegisteredClaims{}
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

		var admin model.Admin
		if err := s.db.WithContext(r.Context()).First(&admin, claims.Subject).Error; err != nil {
			writeError(w, http.StatusUnauthorized, "sign in to continue")
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
	writeJSON(w, http.StatusOK, adminFrom(r.Context()))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
