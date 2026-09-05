package api

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

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
	DBSource  string `json:"dbSource"`
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

	// Host load, so a panel watching this one as a node can show it without a
	// second request. It is the same reading the overview draws.
	CPUPercent float64 `json:"cpuPercent"`
	MemPercent float64 `json:"memPercent"`

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

	if s.sys != nil {
		snap := s.sys.Snapshot()
		info.CPUPercent = snap.CPU.Percent
		info.MemPercent = snap.Memory.Percent
	}

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
		DBSource:  s.dbSource,
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
		writeError(w, http.StatusUnauthorized, "your session has ended; sign in again")
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
	// The generation moves with the password. Somebody changes it because they
	// think a session is not theirs, and a signed token that outlives the change
	// means the intruder keeps working for the rest of the day while the
	// operator believes they have just shut the door.
	//
	// Written in one statement with the hash, so a failure cannot leave the
	// password changed and the old sessions alive.
	if err := s.db.WithContext(r.Context()).Model(admin).Updates(map[string]any{
		"password_hash": string(hash),
		"session_epoch": gorm.Expr("session_epoch + 1"),
	}).Error; err != nil {
		fail(w, s.log, fmt.Errorf("store password: %w", err))
		return
	}

	// Including this one. Signing the operator out of the browser they just
	// used is the honest behaviour: every other session ended, and leaving
	// theirs alive would mean the change was not quite what it said.
	clearBindCookie(w, r)

	s.log.Info("admin password changed; all sessions ended",
		"username", admin.Username, "ip", clientIP(r))
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
		writeError(w, http.StatusUnauthorized, "your session has ended; sign in again")
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

// panelSettingsResponse carries the current values together with the shipped
// defaults, so the page can mark a value that has never been changed instead of
// leaving the operator to guess which of these they chose.
type panelSettingsResponse struct {
	Settings service.PanelSettings `json:"settings"`
	Defaults service.PanelSettings `json:"defaults"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	current, err := s.settings.Get(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, panelSettingsResponse{
		Settings: maskToken(current),
		Defaults: s.settings.Defaults(),
	})
}

// maskToken keeps the bot token out of every settings response.
//
// It is a bearer credential for an account that can message the operator, and
// this endpoint is fetched on every visit to the page. Returning a placeholder
// still lets the page show that one is configured.
func maskToken(in service.PanelSettings) service.PanelSettings {
	if in.NotifyBotToken != "" {
		in.NotifyBotToken = service.TokenPlaceholder
	}
	return in
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var in service.PanelSettings
	if !decode(w, r, &in) {
		return
	}

	saved, err := s.settings.Save(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	// The notifier is told immediately rather than on the next restart, so an
	// operator who has just fixed their token can test it straight away.
	if s.notifier != nil {
		s.notifier.SetConfig(s.settings.Notify(r.Context()))
	}

	writeJSON(w, http.StatusOK, panelSettingsResponse{
		Settings: maskToken(saved),
		Defaults: s.settings.Defaults(),
	})
}

// handleTestNotification sends one message so an operator learns whether their
// token works here, rather than from the absence of an alert months later.
func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if s.notifier == nil {
		fail(w, s.log, fmt.Errorf("notifications are not available"))
		return
	}

	cfg := s.settings.Notify(r.Context())
	if err := s.notifier.Test(r.Context(), cfg); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	list, err := s.backups.List()
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		fail(w, s.log, fmt.Errorf("backups are not available"))
		return
	}
	a, err := s.backups.Create(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		http.NotFound(w, r)
		return
	}

	f, a, err := s.backups.Open(r.PathValue("name"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+a.Name+`"`)
	if _, err := io.Copy(w, f); err != nil {
		s.log.Warn("backup download interrupted", "file", a.Name, "error", err)
	}
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		http.NotFound(w, r)
		return
	}
	if err := s.backups.Delete(r.PathValue("name")); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSharing lists credentials seen from several places at once.
func (s *Server) handleSharing(w http.ResponseWriter, r *http.Request) {
	if s.rec == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	report, err := s.rec.Sharing(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
