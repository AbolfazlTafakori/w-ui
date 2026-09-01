// Package api exposes the panel over HTTP.
//
// The API is the only way the frontend reaches the panel, and the frontend is
// served from the same binary, so there is no cross-origin surface to open up
// and no separate deployment to keep in step.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/shaper"
	"github.com/abolfazl/w-ui/internal/sysinfo"
)

// Server holds the API's dependencies.
type Server struct {
	db        *gorm.DB
	clients   *service.Clients
	ifaces    *service.Interfaces
	catalog   *i18n.Catalog
	enforcer  enforce.Enforcer
	settings  *service.Settings
	notifier  *notify.Notifier
	backups   *backup.Service
	shaper    shaper.Shaper
	jwtSecret []byte
	log       *slog.Logger
	version   string
	listen    string
	dbDriver  string
	dbSource  string
	sys       *sysinfo.Collector
	rec       *reconciler.Reconciler
}

// Options configures a Server.
type Options struct {
	DB         *gorm.DB
	Clients    *service.Clients
	Interfaces *service.Interfaces
	Catalog    *i18n.Catalog
	Enforcer   enforce.Enforcer
	Settings   *service.Settings
	Notifier   *notify.Notifier
	Backups    *backup.Service
	Shaper     shaper.Shaper
	JWTSecret  []byte
	Logger     *slog.Logger
	Version    string
	Listen     string
	DBDriver   string
	DBSource   string
	SysInfo    *sysinfo.Collector
	Reconciler *reconciler.Reconciler
}

// New builds the API server.
func New(o Options) *Server {
	return &Server{
		db:        o.DB,
		clients:   o.Clients,
		ifaces:    o.Interfaces,
		catalog:   o.Catalog,
		enforcer:  o.Enforcer,
		settings:  o.Settings,
		notifier:  o.Notifier,
		backups:   o.Backups,
		shaper:    o.Shaper,
		jwtSecret: o.JWTSecret,
		log:       o.Logger,
		version:   o.Version,
		listen:    o.Listen,
		dbDriver:  o.DBDriver,
		dbSource:  o.DBSource,
		sys:       o.SysInfo,
		rec:       o.Reconciler,
	}
}

// Routes returns the API mux. The caller mounts it under /api/ and serves the
// frontend from everything else.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Open endpoints: signing in, and the strings the sign-in page itself needs.
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/i18n/{locale}", s.handleMessages)

	auth := s.requireAuth
	mux.HandleFunc("GET /api/auth/me", auth(s.handleMe))
	mux.HandleFunc("PATCH /api/auth/me", auth(s.handleUpdateMe))
	mux.HandleFunc("POST /api/auth/password", auth(s.handleChangePassword))
	mux.HandleFunc("POST /api/auth/totp/start", auth(s.handleTOTPStart))
	mux.HandleFunc("POST /api/auth/totp/confirm", auth(s.handleTOTPConfirm))
	mux.HandleFunc("POST /api/auth/totp/disable", auth(s.handleTOTPDisable))
	mux.HandleFunc("GET /api/overview", auth(s.handleOverview))
	mux.HandleFunc("GET /api/system", auth(s.handleSystemInfo))
	mux.HandleFunc("GET /api/logs", auth(s.handleLogs))
	mux.HandleFunc("GET /api/settings", auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", auth(s.handleSaveSettings))
	mux.HandleFunc("POST /api/settings/notify/test", auth(s.handleTestNotification))
	mux.HandleFunc("GET /api/sharing", auth(s.handleSharing))
	mux.HandleFunc("GET /api/backups", auth(s.handleListBackups))
	mux.HandleFunc("POST /api/backups", auth(s.handleCreateBackup))
	mux.HandleFunc("GET /api/backups/{name}", auth(s.handleDownloadBackup))
	mux.HandleFunc("DELETE /api/backups/{name}", auth(s.handleDeleteBackup))
	mux.HandleFunc("GET /api/overview/full", auth(s.handleFullOverview))

	mux.HandleFunc("GET /api/interfaces", auth(s.handleListInterfaces))
	mux.HandleFunc("POST /api/interfaces", auth(s.handleCreateInterface))
	mux.HandleFunc("PATCH /api/interfaces/{id}", auth(s.handleUpdateInterface))
	mux.HandleFunc("DELETE /api/interfaces/{id}", auth(s.handleDeleteInterface))

	mux.HandleFunc("GET /api/clients", auth(s.handleListClients))
	mux.HandleFunc("POST /api/clients", auth(s.handleCreateClient))
	mux.HandleFunc("GET /api/clients/{id}", auth(s.handleGetClient))
	mux.HandleFunc("PATCH /api/clients/{id}", auth(s.handleUpdateClient))
	mux.HandleFunc("DELETE /api/clients/{id}", auth(s.handleDeleteClient))
	mux.HandleFunc("POST /api/clients/{id}/devices", auth(s.handleAddDevice))
	mux.HandleFunc("POST /api/clients/{id}/reset", auth(s.handleResetTraffic))
	mux.HandleFunc("POST /api/clients/bulk", auth(s.handleBulk))
	mux.HandleFunc("POST /api/clients/adjust", auth(s.handleAdjust))
	mux.HandleFunc("POST /api/clients/reset-all", auth(s.handleResetAll))
	mux.HandleFunc("POST /api/clients/purge", auth(s.handlePurge))
	mux.HandleFunc("POST /api/clients/batch", auth(s.handleCreateBatch))
	mux.HandleFunc("GET /api/clients/export", auth(s.handleExport))
	mux.HandleFunc("POST /api/clients/import", auth(s.handleImport))

	mux.HandleFunc("GET /api/groups", auth(s.handleListGroups))
	mux.HandleFunc("GET /api/groups/names", auth(s.handleGroupNames))
	mux.HandleFunc("POST /api/groups/rename", auth(s.handleRenameGroup))
	mux.HandleFunc("POST /api/groups/assign", auth(s.handleAssignGroup))
	mux.HandleFunc("POST /api/groups/action", auth(s.handleGroupAction))

	mux.HandleFunc("DELETE /api/devices/{id}", auth(s.handleRemoveDevice))
	mux.HandleFunc("GET /api/devices/{id}/profile", auth(s.handleProfile))

	return mux
}

// LogRequests records method, path, status and duration for each request.
func LogRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		log.Log(r.Context(), level, "request",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
