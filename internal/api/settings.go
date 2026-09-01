package api

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/sysinfo"
)

// startedAt is when the process came up, used to report uptime.
var startedAt = time.Now()

// systemInfo is what the settings page shows: everything an operator needs to
// answer "what is this server actually running" without opening a shell.
type systemInfo struct {
	Version   string `json:"version"`
	Listen    string `json:"listen"`
	DBDriver  string `json:"dbDriver"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
	UptimeSec int64  `json:"uptimeSec"`

	Protocols []model.Protocol `json:"protocols"`
	Locales   []string         `json:"locales"`

	EnforcementActive  bool   `json:"enforcementActive"`
	EnforcementMessage string `json:"enforcementMessage,omitempty"`

	Interfaces int64 `json:"interfaces"`
	Clients    int64 `json:"clients"`
	Accounts   int64 `json:"accounts"`

	Reconciler reconciler.Stats `json:"reconciler"`
}

// overviewResponse is everything the overview page draws: host telemetry, the
// panel's own state, and the inventory it manages, in one round trip so the
// page cannot show three readings taken at three different moments.
type overviewResponse struct {
	System  sysinfo.Snapshot  `json:"system"`
	Panel   systemInfo        `json:"panel"`
	Clients *service.Overview `json:"clients"`
	Ifaces  []interfaceView   `json:"interfaces"`
}

func (s *Server) handleFullOverview(w http.ResponseWriter, r *http.Request) {
	clients, err := s.clients.Overview(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}

	views, err := s.interfaceViews(r)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	writeJSON(w, http.StatusOK, overviewResponse{
		System:  s.sys.Snapshot(),
		Panel:   s.buildSystemInfo(r),
		Clients: clients,
		Ifaces:  views,
	})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info := s.buildSystemInfo(r)

	db := s.db.WithContext(r.Context())
	if err := db.Model(&model.Interface{}).Count(&info.Interfaces).Error; err != nil {
		fail(w, s.log, err)
		return
	}
	if err := db.Model(&model.Client{}).Count(&info.Clients).Error; err != nil {
		fail(w, s.log, err)
		return
	}
	if err := db.Model(&model.Account{}).Count(&info.Accounts).Error; err != nil {
		fail(w, s.log, err)
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// buildSystemInfo fills the panel-side facts, without the database counts that
// only the settings page needs.
func (s *Server) buildSystemInfo(r *http.Request) systemInfo {
	info := systemInfo{
		Version:   s.version,
		Listen:    s.listen,
		DBDriver:  s.dbDriver,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		UptimeSec: int64(time.Since(startedAt).Seconds()),
		Protocols: backend.Registered(),
		Locales:   s.catalog.Locales(),
	}
	if err := s.enforcer.Health(r.Context()); err != nil {
		info.EnforcementMessage = err.Error()
	} else {
		info.EnforcementActive = true
	}
	if s.rec != nil {
		info.Reconciler = s.rec.Stats()
	}
	return info
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// handleChangePassword updates the signed-in operator's password.
//
// The current password is required even though the caller already holds a
// valid session: a borrowed unlocked browser should not be enough to lock the
// real owner out of their own panel.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "sign in to continue")
		return
	}

	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "the new password must be at least 8 characters")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.CurrentPassword)) != nil {
		writeError(w, http.StatusForbidden, "the current password is incorrect")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		fail(w, s.log, fmt.Errorf("hash password: %w", err))
		return
	}
	if err := s.db.WithContext(r.Context()).Model(admin).
		Update("password_hash", string(hash)).Error; err != nil {
		fail(w, s.log, fmt.Errorf("store password: %w", err))
		return
	}

	s.log.Info("admin password changed", "username", admin.Username, "ip", clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

type updateMeRequest struct {
	Locale string `json:"locale"`
}

// handleUpdateMe stores the operator's interface language so it follows them to
// another browser rather than living only in that one's local storage.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin == nil {
		writeError(w, http.StatusUnauthorized, "sign in to continue")
		return
	}

	var req updateMeRequest
	if !decode(w, r, &req) {
		return
	}

	locale := strings.ToLower(strings.TrimSpace(req.Locale))
	if !slicesContains(s.catalog.Locales(), locale) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported language %q; available: %s",
				locale, strings.Join(s.catalog.Locales(), ", ")))
		return
	}

	if err := s.db.WithContext(r.Context()).Model(admin).
		Update("locale", locale).Error; err != nil {
		fail(w, s.log, fmt.Errorf("store locale: %w", err))
		return
	}
	admin.Locale = locale
	writeJSON(w, http.StatusOK, admin)
}

func slicesContains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
