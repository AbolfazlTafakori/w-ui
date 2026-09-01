// Command wui runs the W-UI control panel.
//
// The binary carries everything: the API, the compiled frontend, the schema and
// the migrations. Deploying it is copying one file.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/api"
	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/ovpndriver"
	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/sysinfo"
	"github.com/abolfazl/w-ui/internal/web"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wui: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log, err := logger.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return err
	}
	log.Info("starting", "version", version, "listen", cfg.Listen)

	catalog, err := i18n.Load()
	if err != nil {
		return err
	}
	reportLocaleDrift(catalog, log)

	db, err := database.Open(cfg, log)
	if err != nil {
		return err
	}

	password, err := database.Bootstrap(db, cfg.DefaultLocale, log)
	if err != nil {
		return err
	}
	if password != "" {
		printFirstRunCredentials(password)
	}

	jwtSecret, err := database.EnsureSecret(db, database.KeyJWTSecret, 32)
	if err != nil {
		return err
	}

	// OpenVPN keeps its per-interface files under the data directory, so the
	// whole of the panel's state stays in one place and one backup.
	ovpndriver.DataRoot = cfg.DataDir

	registerBackends(log)

	pools, err := loadPools(db, log)
	if err != nil {
		return err
	}

	enforcer := enforce.NewNFTables(log)
	defer enforcer.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	drivers, err := openDrivers(ctx, db, log)
	if err != nil {
		return err
	}

	rec := reconciler.New(reconciler.Options{
		DB:       db,
		Enforcer: enforcer,
		Backends: drivers,
		Interval: cfg.CollectInterval,
		Log:      log,
	})
	rec.Start(ctx)

	if err := enforcer.Health(ctx); err != nil {
		// Quota enforcement is the panel's core promise. Saying this once, at
		// warn level, is better than letting an operator sell a 50 GB plan that
		// silently has no ceiling.
		log.Warn("quota enforcement inactive: limits are recorded but not applied",
			"reason", err)
	} else {
		log.Info("quota enforcement active", "engine", "nftables")
	}
	if !web.Built() {
		log.Warn("frontend placeholder embedded; run npm run build in web/ and rebuild")
	}

	if err := reportReadiness(ctx, db, pools, log); err != nil {
		return err
	}

	sys := sysinfo.New(cfg.DataDir, cfg.CollectInterval, log)
	sys.Start(ctx)

	srv, err := buildServer(cfg, db, pools, catalog, enforcer, jwtSecret, sys, rec, log)
	if err != nil {
		return err
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "address", cfg.Listen, "url", "http://"+cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// buildServer wires the services, the API and the embedded frontend into one
// HTTP server.
func buildServer(
	cfg config.Config,
	db *gorm.DB,
	pools *ipam.Pools,
	catalog *i18n.Catalog,
	enforcer enforce.Enforcer,
	jwtSecret []byte,
	sys *sysinfo.Collector,
	rec *reconciler.Reconciler,
	log *slog.Logger,
) (*http.Server, error) {
	apiSrv := api.New(api.Options{
		DB:         db,
		Clients:    service.NewClients(db, pools, log),
		Interfaces: service.NewInterfaces(db, pools, log),
		Catalog:    catalog,
		Enforcer:   enforcer,
		JWTSecret:  jwtSecret,
		Logger:     log,
		Version:    version,
		Listen:     cfg.Listen,
		DBDriver:   string(cfg.DBDriver),
		SysInfo:    sys,
		Reconciler: rec,
	})

	frontend, err := web.Handler()
	if err != nil {
		return nil, fmt.Errorf("frontend: %w", err)
	}

	root := http.NewServeMux()
	root.Handle("/api/", apiSrv.Routes())
	root.Handle("/", frontend)

	return &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.LogRequests(log, root),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, nil
}

// registerBackends links the available protocol drivers.
//
// Phase 1 has no kernel drivers, so in-memory stand-ins are registered for any
// protocol left unclaimed. Phases 3 and 4 add wireguard and openvpn drivers
// that register themselves, and this loop then leaves them alone.
func registerBackends(log *slog.Logger) {
	// The real drivers claim their protocols first; the loop below then only
	// fills in whatever is still unclaimed.
	wgdriver.Register()
	ovpndriver.Register()

	for _, p := range []model.Protocol{model.ProtocolWireGuard, model.ProtocolOpenVPN} {
		if backend.Supports(p) {
			continue
		}
		proto := p
		backend.Register(proto, func() backend.Backend { return backend.NewMemory(proto) })
		log.Warn("no kernel driver for protocol; using in-memory stand-in",
			"protocol", proto)
	}
	log.Info("protocol drivers registered", "protocols", backend.Registered())
}

// loadPools rebuilds an address allocator per interface and replays every
// stored allocation into it.
//
// This is the boot half of the rule that the database is the source of truth:
// nothing is read back from the kernel or from a config file to decide which
// addresses are taken.
func loadPools(db *gorm.DB, log *slog.Logger) (*ipam.Pools, error) {
	var interfaces []model.Interface
	if err := db.Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("load interfaces: %w", err)
	}

	pools := ipam.NewPools()
	for i := range interfaces {
		iface := &interfaces[i]
		alloc, err := pools.Add(iface.ID, iface.Subnet)
		if err != nil {
			return nil, err
		}

		var addrs []string
		if err := db.Model(&model.Account{}).
			Where("interface_id = ?", iface.ID).
			Pluck("ip", &addrs).Error; err != nil {
			return nil, fmt.Errorf("load addresses for interface %q: %w", iface.Name, err)
		}
		for _, a := range addrs {
			if err := pools.Replay(iface.ID, a); err != nil {
				return nil, err
			}
		}

		log.Info("address pool ready",
			"interface", iface.Name,
			"protocol", iface.Protocol,
			"subnet", iface.Subnet,
			"allocated", alloc.InUse(),
			"capacity", alloc.Capacity())
	}
	return pools, nil
}

// reportReadiness logs what the panel is holding, so a fresh install says
// clearly that it has nothing configured yet rather than looking healthy and
// idle.
func reportReadiness(ctx context.Context, db *gorm.DB, pools *ipam.Pools, log *slog.Logger) error {
	node, err := database.LocalNode(db)
	if err != nil {
		return err
	}

	var clients, accounts int64
	if err := db.WithContext(ctx).Model(&model.Client{}).Count(&clients).Error; err != nil {
		return fmt.Errorf("count clients: %w", err)
	}
	if err := db.WithContext(ctx).Model(&model.Account{}).Count(&accounts).Error; err != nil {
		return fmt.Errorf("count accounts: %w", err)
	}

	log.Info("inventory",
		"node", node.Name,
		"interfaces", pools.Len(),
		"clients", clients,
		"accounts", accounts)

	if pools.Len() == 0 {
		log.Warn("no interfaces configured; create one before adding customers")
	}
	return nil
}

// reportLocaleDrift warns when a translation has fallen behind English. English
// is the source locale, so untranslated keys render in English rather than
// breaking, but drift should still be visible.
func reportLocaleDrift(c *i18n.Catalog, log *slog.Logger) {
	for _, l := range c.Locales() {
		if l == i18n.DefaultLocale {
			continue
		}
		if missing := c.Missing(l); len(missing) > 0 {
			log.Warn("locale is missing translations",
				"locale", l, "count", len(missing), "keys", missing)
		}
	}
	log.Info("locales loaded", "available", c.Locales(), "source", i18n.DefaultLocale)
}

func printFirstRunCredentials(password string) {
	const line = "────────────────────────────────────────────────────────"
	fmt.Fprintf(os.Stderr, "\n%s\n", line)
	fmt.Fprintf(os.Stderr, "  First run: an admin account has been created.\n\n")
	fmt.Fprintf(os.Stderr, "    username  admin\n")
	fmt.Fprintf(os.Stderr, "    password  %s\n\n", password)
	fmt.Fprintf(os.Stderr, "  This password is shown once and is not recoverable.\n")
	fmt.Fprintf(os.Stderr, "  Change it after signing in.\n")
	fmt.Fprintf(os.Stderr, "%s\n\n", line)
}

// assert that the enforcer stand-in satisfies the contract the phase 2
// nftables implementation will have to meet.
var _ enforce.Enforcer = (*enforce.Noop)(nil)

// assert that the in-memory backend satisfies the driver contract.
var _ backend.Backend = (*backend.Memory)(nil)

// openDrivers binds one protocol driver per configured interface.
//
// A driver that will not open is logged and skipped rather than aborting the
// boot: one broken interface should not take the panel — and every other
// interface's enforcement — down with it.
func openDrivers(ctx context.Context, db *gorm.DB, log *slog.Logger) (map[uint]backend.Backend, error) {
	var interfaces []model.Interface
	if err := db.Where("enabled = ?", true).Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("load interfaces: %w", err)
	}

	out := make(map[uint]backend.Backend, len(interfaces))
	for i := range interfaces {
		iface := &interfaces[i]
		drv, err := backend.New(iface.Protocol)
		if err != nil {
			log.Error("no driver for interface", "interface", iface.Name, "error", err)
			continue
		}
		if withLogger, ok := drv.(interface{ SetLogger(*slog.Logger) }); ok {
			withLogger.SetLogger(log)
		}
		if err := drv.Open(ctx, iface); err != nil {
			log.Error("could not open interface", "interface", iface.Name, "error", err)
			continue
		}
		out[iface.ID] = drv
		log.Info("driver open", "interface", iface.Name, "protocol", iface.Protocol)
	}
	return out, nil
}
