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
	"github.com/abolfazl/w-ui/internal/nodes"
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
	nodes     *service.Nodes
	prober    *nodes.Prober
	throttle  *throttle
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
	Prober     *nodes.Prober
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
		nodes:     service.NewNodes(o.DB, o.Logger),
		prober:    o.Prober,
		throttle:  newThrottle(),
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

	// Every endpoint comes from the table in routes.go, which is also what the
	// documentation page reads. Registering from one place is what stops the
	// two drifting apart.
	s.register(mux)
	mux.HandleFunc("GET /api/docs", s.requireAuth(s.handleAPIDocs))

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
