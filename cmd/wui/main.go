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
	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/nodes"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/shaper"
	"github.com/abolfazl/w-ui/internal/sysinfo"
	"github.com/abolfazl/w-ui/internal/web"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// A subcommand means the operator is asking the binary a question from a
	// terminal, not starting the panel.
	if handled, err := dispatch(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "wui: %v\n", err)
			os.Exit(1)
		}
		return
	}

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

	shp := shaper.New(log)
	defer shp.Close()

	router := routing.NewApplier(log)
	hops := routing.NewHopManager(log)

	settings := service.NewSettings(db, cfg.DefaultLocale)
	outbounds := service.NewOutbounds(db, log)
	routes := service.NewRouting(db, log)
	notifier := notify.New(log)
	notifier.SetConfig(settings.Notify(context.Background()))

	backups := backup.New(backup.Options{
		DataDir: cfg.DataDir,
		Keep:    7,
		Log:     log,
		// SQLite can write a consistent copy of itself while it is in use.
		// Copying the file byte by byte instead can catch it mid-write, and a
		// torn database is worth nothing at the moment it is needed.
		Snapshot: func(ctx context.Context, dest string) error {
			if cfg.DBDriver != config.DriverSQLite {
				return fmt.Errorf("snapshots are only available for sqlite")
			}
			return db.WithContext(ctx).Exec("VACUUM INTO ?", dest).Error
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	drivers, err := openDrivers(ctx, db, log)
	if err != nil {
		return err
	}

	// The two built-in outbounds are created before the reconciler starts, so
	// its first tick already has somewhere to send traffic.
	if err := outbounds.EnsureBuiltins(ctx); err != nil {
		return err
	}
	// Names in the policy are resolved on a timer; the first pass runs now so
	// the opening tick is not applied with empty sets.
	routes.StartResolver(ctx)

	rec := reconciler.New(reconciler.Options{
		DB:       db,
		Enforcer: enforcer,
		Backends: drivers,
		Shaper:   shp,
		Router:   router,
		Hops:     hops,
		Policy:   routes.Policy,
		HopsOf:   func(ctx context.Context) ([]routing.HopSpec, error) { return outbounds.HopSpecs(ctx) },
		Notifier: notifier,
		Interval: cfg.CollectInterval,
		Log:      log,
	})
	rec.Start(ctx)

	if err := shp.Health(ctx); err != nil {
		log.Warn("rate limiting inactive: speed limits are recorded but not applied", "reason", err)
	} else {
		log.Info("rate limiting active", "engine", "tc/htb")
	}

	if err := router.Health(ctx); err != nil {
		// Said once, plainly. Without this an operator can configure a foreign
		// exit, see it listed, and never learn that every customer is still
		// leaving through the server's own address.
		log.Warn("traffic routing inactive: outbounds and routing rules are stored but not applied",
			"reason", err)
	} else {
		log.Info("traffic routing active", "engine", "nftables/fwmark")
	}

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

	notifier.Start(ctx)

	// Watching the other servers. Started before the HTTP listener so the nodes
	// page has answers the first time it is opened rather than an empty table.
	prober := nodes.New(db, log)
	prober.Start(ctx)

	// Re-read on every check rather than captured here, so changing either on
	// the settings page takes effect without a restart.
	scheduler := backup.NewScheduler(backups)
	scheduler.Every = func() time.Duration {
		got, err := settings.Get(ctx)
		if err != nil {
			return 0
		}
		return time.Duration(got.BackupEveryHours) * time.Hour
	}
	scheduler.Keep = func() int {
		got, err := settings.Get(ctx)
		if err != nil {
			return 7
		}
		return got.BackupKeep
	}
	scheduler.OnBackup = func(a backup.Archive) {
		notifier.Send(notify.Event{
			Kind:  notify.KindBackup,
			Title: "Backup taken",
			Body:  fmt.Sprintf("%s (%d bytes)", a.Name, a.Size),
		})
	}
	scheduler.Start(ctx)

	notifier.Send(notify.Event{
		Kind:  notify.KindPanel,
		Title: "Panel started",
		Body:  fmt.Sprintf("W-UI %s on %s", version, cfg.Listen),
	})

	srv, err := buildServer(cfg, db, pools, catalog, enforcer, shp, settings, notifier, backups, prober,
		jwtSecret, sys, rec, outbounds, routes, router, log)
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
	shp shaper.Shaper,
	settings *service.Settings,
	notifier *notify.Notifier,
	backups *backup.Service,
	prober *nodes.Prober,
	jwtSecret []byte,
	sys *sysinfo.Collector,
	rec *reconciler.Reconciler,
	outbounds *service.Outbounds,
	routes *service.Routing,
	router *routing.Applier,
	log *slog.Logger,
) (*http.Server, error) {
	apiSrv := api.New(api.Options{
		DB:         db,
		Clients:    service.NewClients(db, pools, log),
		Interfaces: service.NewInterfaces(db, pools, log),
		Catalog:    catalog,
		Enforcer:   enforcer,
		Shaper:     shp,
		Settings:   settings,
		Notifier:   notifier,
		Backups:    backups,
		Prober:     prober,
		JWTSecret:  jwtSecret,
		Logger:     log,
		Version:    version,
		Listen:     cfg.Listen,
		DBDriver:   string(cfg.DBDriver),
		DBSource:   cfg.DBSource,
		SysInfo:    sys,
		Outbounds:  outbounds,
		Routing:    routes,
		Router:     router,
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
		Handler:           api.LogRequests(log, api.SecureHeaders(root)),
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

// printFirstRunCredentials shows the generated password once.
//
// It goes to stdout, alongside the log. On stderr — where it used to be —
// anyone capturing the panel's output the ordinary way, `wui > wui.log`, was
// told an account had been created and never saw its password. It cannot be
// recovered, so that left them locked out until they found `wui admin reset`.
func printFirstRunCredentials(password string) {
	const line = "────────────────────────────────────────────────────────"
	fmt.Fprintf(os.Stdout, "\n%s\n", line)
	fmt.Fprintf(os.Stdout, "  First run: an admin account has been created.\n\n")
	fmt.Fprintf(os.Stdout, "    username  admin\n")
	fmt.Fprintf(os.Stdout, "    password  %s\n\n", password)
	fmt.Fprintf(os.Stdout, "  This password is shown once and is not recoverable.\n")
	fmt.Fprintf(os.Stdout, "  Change it after signing in.\n")
	fmt.Fprintf(os.Stdout, "%s\n\n", line)
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
