// Package api exposes the panel over HTTP.
//
// The API is the only way the frontend reaches the panel, and the frontend is
// served from the same binary, so there is no cross-origin surface to open up
// and no separate deployment to keep in step.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/nodes"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/routing"
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
	basePath  string
	tlsCert   string
	tlsKey    string
	proxies   string
	dbDriver  string
	dbSource  string
	sys       *sysinfo.Collector
	rec       *reconciler.Reconciler
	outbounds *service.Outbounds
	routing   *service.Routing
	hosts     *service.Hosts
	router    *routing.Applier
	subs      *service.Subscriptions
	subMiss   *subMisses
	totpMu    sync.Mutex
	totpUsed  map[uint]usedCode
	previews  subPreviews
	obSubs    *service.OutboundSubs
	balancers *service.Balancers
	providers *service.Providers
	audit     *service.Audit
	pool      *backend.Pool
	nodeSync  *service.NodeSync
	// localNodeID is which node this panel is, for the state another panel
	// pushes here.
	localNodeID uint

	restart  func()
	engine   *service.Engine
	template *service.Template
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
	Outbounds  *service.Outbounds
	Routing    *service.Routing
	Router     *routing.Applier
	Subs       *service.Subscriptions
	Pool       *backend.Pool
	// LocalNodeID is this panel's own node row, used when another panel is
	// driving this one as a node.
	LocalNodeID uint

	// Restart ends the process so the service manager brings it back. Nil
	// means the settings page cannot offer a restart.
	Restart func()

	Engine   *service.Engine
	Template *service.Template

	// What the process is running with, so the settings page can show the
	// effective listen address, path and certificates when nothing has been
	// saved over them.
	BasePath       string
	TLSCert        string
	TLSKey         string
	TrustedProxies string
}

// New builds the API server.
func New(o Options) *Server {
	srv := &Server{
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
		outbounds: o.Outbounds,
		routing:   o.Routing,
		hosts:     service.NewHosts(o.DB, o.Logger),
		router:    o.Router,
		subs:      o.Subs,
		subMiss:   newSubMisses(),
		obSubs:    service.NewOutboundSubs(o.DB, o.Outbounds, o.Logger),
		balancers: service.NewBalancers(o.DB, o.Logger),
		providers: service.NewProviders(o.DB, o.Outbounds, o.Logger),
		pool:      o.Pool,
		nodeSync:  newNodeSync(o),
		// Falls back to the first node, which is this one on every install that
		// has never added a second.
		localNodeID: maxUint(o.LocalNodeID, 1),
		restart:     o.Restart,
		engine:      o.Engine,
		template:    o.Template,
		basePath:    o.BasePath,
		tlsCert:     o.TLSCert,
		tlsKey:      o.TLSKey,
		proxies:     o.TrustedProxies,
	}

	// Built last because it asks the server which engines are running, and that
	// question needs the enforcer, shaper and router the server was just given.
	srv.audit = service.NewAudit(o.DB, o.Subs, o.Listen, srv.engineHealth)
	return srv
}

// engineHealth reports why each engine is not doing its job, keyed by name.
//
// An engine that is working contributes no entry, so an empty map means the
// kernel is doing everything the panel asked of it.
func (s *Server) engineHealth(ctx context.Context) map[string]string {
	out := map[string]string{}
	if s.enforcer != nil {
		if err := s.enforcer.Health(ctx); err != nil {
			out["enforcement"] = err.Error()
		}
	}
	if s.router != nil {
		if err := s.router.Health(ctx); err != nil {
			out["routing"] = err.Error()
		}
	}
	if s.shaper != nil {
		if err := s.shaper.Health(ctx); err != nil {
			out["shaping"] = err.Error()
		}
	}
	return out
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
		if !service.AccessLog.Load() {
			return
		}

		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		attrs := []any{
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start),
		}
		// Only when something went wrong. A refused sign-in or a scan is the
		// line an operator goes looking for afterwards, and without an address
		// it says only that somebody, somewhere, tried. Left off the ordinary
		// ones so a panel's debug log is not a record of where its operator
		// works from, kept for as long as the journal keeps anything.
		if rec.status >= 400 {
			ip := clientIP(r)
			if service.MaskAddress.Load() {
				ip = maskIP(ip)
			}
			attrs = append(attrs, "ip", ip)
		}
		log.Log(r.Context(), level, "request", attrs...)
	})
}

// maskIP hides the last part of an address, the way Xray's maskAddress
// does, for an operator who keeps logs but not where people come from.
func maskIP(ip string) string {
	if i := strings.LastIndex(ip, "."); i > 0 {
		return ip[:i] + ".*"
	}
	if i := strings.LastIndex(ip, ":"); i > 0 {
		return ip[:i] + ":*"
	}
	return ip
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// maxUint keeps a node id usable when the caller left it unset.
func maxUint(v, floor uint) uint {
	if v < floor {
		return floor
	}
	return v
}

// newNodeSync builds the node side with its way to the data plane: a hold
// the managing panel decides has to end a session here, which is the
// reconciler's to do.
func newNodeSync(o Options) *service.NodeSync {
	ns := service.NewNodeSync(o.DB, o.Logger)
	if o.Reconciler != nil {
		ns.OnHold = o.Reconciler.Hold
	}
	if o.Interfaces != nil && o.Interfaces.Teardown != nil {
		ns.OnRemoveInterface = o.Interfaces.Teardown
	}
	return ns
}
