package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/service"
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
	return s.settings.SessionTTL(ctx)
}

type ctxKey int

const ctxAdmin ctxKey = iota

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

type loginResponse struct {
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expiresAt"`
	Admin     *adminView `json:"admin"`

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

	// Switched off by the owner means out. Asked only once the password is
	// right, so it tells someone guessing nothing; and before the second
	// factor, since a code for an account that cannot come in is a question
	// with no point. A reseller whose term ran out or whose allowance is
	// spent is let in, read-only, to see why and pay: that is a lapse, and
	// this is a decision.
	if admin.Role != model.RoleOwner && !admin.Enabled {
		s.log.Warn("sign-in refused: account switched off",
			"username", admin.Username, "ip", clientIP(r))
		writeError(w, http.StatusForbidden, "your account has been switched off")
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
		if !totp.Validate(admin.TOTPSecret, req.Code, time.Now()) || s.totpReplayed(admin.ID, req.Code, now) {
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

	// A term on hold starts now. Written with a condition on the row, so two
	// sign-ins racing each other cannot both start the clock.
	if admin.Role.Capped() && admin.ExpiresAt == nil && admin.DurationDays > 0 {
		ends := time.Now().UTC().Add(time.Duration(admin.DurationDays) * 24 * time.Hour)
		res := s.db.WithContext(r.Context()).Model(&model.Admin{}).
			Where("id = ? AND expires_at IS NULL AND duration_days > 0", admin.ID).
			Updates(map[string]any{"expires_at": ends, "duration_days": 0})
		if res.Error != nil {
			s.log.Error("start a reseller's term", "username", admin.Username, "error", res.Error)
		} else if res.RowsAffected > 0 {
			s.log.Info("reseller's term started on first sign-in",
				"username", admin.Username, "days", admin.DurationDays, "until", ends)
			admin.ExpiresAt, admin.DurationDays = &ends, 0
		}
	}

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
	writeJSON(w, http.StatusOK, loginResponse{
		Token: token, ExpiresAt: expires, Admin: s.adminView(r.Context(), &admin)})
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

// authenticate says who a request is from: an administrator with a live
// session, a machine token (admin nil, machine true), or nobody, with the
// message the caller should answer with and whether the binding cookie
// should be cleared.
func (s *Server) authenticate(r *http.Request) (admin *model.Admin, machine bool, reason string, clearCookie bool) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" || raw == r.Header.Get("Authorization") {
		return nil, false, "your session has ended; sign in again", false
	}

	// A machine token, from another panel watching this one. Checked before
	// the JWT because it is not one and would fail that parse with a
	// message about sessions that would send an operator looking in the
	// wrong place.
	if strings.HasPrefix(raw, "wui_") {
		if s.nodes != nil && s.nodes.VerifyToken(r.Context(), raw) {
			return nil, true, "", false
		}
		return nil, false, "that access token is not valid", false
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
		return nil, false, "session expired, sign in again", false
	}

	// The token alone is not a session. Without the cookie it names, this is
	// a token that left the browser it was issued to.
	if !bindingHolds(r, claims) {
		return nil, false, "your session has ended; sign in again", false
	}

	var a model.Admin
	if err := s.db.WithContext(r.Context()).First(&a, claims.Subject).Error; err != nil {
		return nil, false, "your session has ended; sign in again", false
	}

	// Signed out everywhere since this was issued — by a password change, or
	// deliberately.
	if !epochHolds(&a, claims) {
		return nil, false, "you were signed out everywhere; sign in again", true
	}
	return &a, false, "", false
}

// signedIn is authenticate for a handler that serves everyone and says a
// little more to an administrator.
func (s *Server) signedIn(r *http.Request) bool {
	admin, machine, _, _ := s.authenticate(r)
	return admin != nil || machine
}

// requireAuth rejects requests without a valid bearer token.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin, machine, reason, clearCookie := s.authenticate(r)
		if admin == nil && !machine {
			if clearCookie {
				clearBindCookie(w, r)
			}
			writeError(w, http.StatusUnauthorized, reason)
			return
		}
		if machine {
			next(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxAdmin, admin)
		// Attached here, once, for every authenticated request. Below this
		// line the database narrows itself to this operator on its own, so
		// a handler cannot serve another operator's customers by forgetting
		// to ask whose they are.
		sc, err := s.scopeFor(ctx, admin)
		if err != nil {
			fail(w, s.log, err)
			return
		}
		next(w, r.WithContext(service.WithScope(ctx, sc)))
	}
}

// scopeFor works out how much of the panel this operator sees, and what
// they are allowed to sell.
func (s *Server) scopeFor(ctx context.Context, admin *model.Admin) (service.Scope, error) {
	sc := service.ScopeFor(admin)
	if !sc.Restricted {
		return sc, nil
	}
	allowed, err := s.admins.AllowedInterfaces(ctx, admin)
	if err != nil {
		return sc, err
	}
	sc.Interfaces = allowed
	sc.ClientLimit = admin.ClientLimit
	switch {
	case !admin.Enabled:
		sc.Barred = "your account has been switched off"
	case admin.Expired(time.Now().UTC()):
		sc.Barred = "your account's term has ended"
	case admin.QuotaExceeded():
		sc.Barred = "your data allowance is used up"
	}
	return sc, nil
}

// requireManager refuses anyone but the panel's owner.
//
// What is behind it is the machine: the tunnels, the nodes, where traffic
// goes, the engine, the settings and the backups. A reseller has no
// business there, and neither has an administrator brought in to help with
// customers -- the point of that role is a second pair of hands over the
// customers without the server underneath.
func (s *Server) requireManager(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := adminFrom(r.Context())
		if admin == nil || !admin.Role.ManagesPanel() {
			writeError(w, http.StatusForbidden, "this part of the panel is the owner's")
			return
		}
		next(w, r)
	}
}

// requireAdminManager refuses anyone but the owner from the operator list.
func (s *Server) requireAdminManager(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admin := adminFrom(r.Context())
		if admin == nil || !admin.Role.ManagesAdmins() {
			writeError(w, http.StatusForbidden, "only the panel's owner manages operators")
			return
		}
		next(w, r)
	}
}

// requireOperator refuses a machine token: what is behind it is an
// administrator's to do. Runs inside requireAuth, so the context carries
// the admin when there is one.
func (s *Server) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if adminFrom(r.Context()) == nil {
			writeError(w, http.StatusForbidden, "this needs a signed-in administrator, not an API token")
			return
		}
		next(w, r)
	}
}

// adminFrom returns the signed-in admin attached by requireAuth.
func adminFrom(ctx context.Context) *model.Admin {
	admin, _ := ctx.Value(ctxAdmin).(*model.Admin)
	return admin
}

// adminView is the operator as the panel needs them to draw itself.
//
// The secret itself is never serialised. Only whether one is set, which is
// what the settings page needs to know to show the right control. The three
// questions the interface asks about a role are sent as answers rather than
// leaving the page to work them out, so the menu and the server can never
// disagree about who may reach what.
//
// Hiding a page is not what keeps anyone out -- every endpoint behind it
// checks for itself. It is so the panel a reseller signs in to has only the
// pages that are theirs on it, rather than a menu of things that refuse them.
type adminView struct {
	*model.Admin
	TwoFactor     bool `json:"twoFactor"`
	ManagesPanel  bool `json:"managesPanel"`
	ManagesAdmins bool `json:"managesAdmins"`
	SeesEveryone  bool `json:"seesEveryone"`
}

// adminView builds it. Sign-in and the /me call both answer with this: an
// operator whose sign-in answered with less would be shown a menu for
// somebody else until the first time they reloaded the page.
func (s *Server) adminView(ctx context.Context, admin *model.Admin) *adminView {
	view := &adminView{
		Admin:         admin,
		TwoFactor:     admin.TOTPSecret != "",
		ManagesPanel:  admin.Role.ManagesPanel(),
		ManagesAdmins: admin.Role.ManagesAdmins(),
		SeesEveryone:  admin.Role.SeesEveryone(),
	}
	// The label the owner files this operator's customers under is not
	// theirs to see, and it is on the row that answers this call.
	if !admin.Role.SeesEveryone() {
		clone := *admin
		clone.GroupName = ""
		view.Admin = &clone
	}
	if admin.Role.Capped() {
		var held int64
		if err := s.db.WithContext(ctx).Model(&model.Client{}).
			Where("owner_id = ?", admin.ID).Count(&held).Error; err == nil {
			view.Admin.Clients = held
		}
	}
	if ids, err := s.admins.AllowedInterfaces(ctx, admin); err == nil && ids != nil {
		list := make([]uint, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		view.Admin.InterfaceIDs = list
	}
	return view
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeJSON(w, http.StatusOK, s.adminView(r.Context(), admin))
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
	Code     string `json:"code"`
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
	// Either proof will do: the password, or a code from the app being
	// removed, which is what the classic panel asks for and what an operator holding the
	// phone has to hand.
	// Throttled as sign-in is: a six-digit code tried as fast as a borrowed
	// session can send requests falls in well under an hour.
	key := fmt.Sprintf("totpoff:%d", admin.ID)
	now := time.Now()
	if wait := s.throttle.retryAfter(key, now); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, lockoutMessage(wait))
		return
	}
	switch {
	case req.Password != "" && bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(req.Password)) == nil:
	case req.Code != "" && stored.TOTPSecret != "" && totp.Validate(stored.TOTPSecret, req.Code, now) &&
		!s.totpReplayed(stored.ID, req.Code, now):
	default:
		s.throttle.fail(key, now)
		s.log.Warn("wrong proof when turning off the second factor",
			"username", admin.Username, "ip", clientIP(r))
		writeError(w, http.StatusForbidden, "that password or code is not right")
		return
	}
	s.throttle.succeed(key)

	err := s.db.WithContext(r.Context()).Model(&model.Admin{}).
		Where("id = ?", admin.ID).Update("totp_secret", "").Error
	if err != nil {
		fail(w, s.log, fmt.Errorf("remove second factor: %w", err))
		return
	}

	s.log.Warn("second factor disabled", "username", admin.Username, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// totpReplayed reports whether this code was already accepted for this
// administrator in the last little while, and remembers it otherwise. A code
// is good for a window of a minute or so, and one read over a shoulder or
// off a screen must not open a second session inside that window.
func (s *Server) totpReplayed(adminID uint, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	s.totpMu.Lock()
	defer s.totpMu.Unlock()
	if s.totpUsed == nil {
		s.totpUsed = map[uint]usedCode{}
	}
	if u, ok := s.totpUsed[adminID]; ok && u.code == code && now.Sub(u.at) < 2*time.Minute {
		return true
	}
	s.totpUsed[adminID] = usedCode{code: code, at: now}
	return false
}

type usedCode struct {
	code string
	at   time.Time
}
