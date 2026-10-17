// Command wui runs the W-UI control panel.
//
// The binary carries everything: the API, the compiled frontend, the schema and
// the migrations. Deploying it is copying one file.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/api"
	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/ikev2driver"
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/backend/occdriver"
	"github.com/abolfazl/w-ui/internal/backend/ovpndriver"
	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/certfile"
	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/dnsproxy"
	"github.com/abolfazl/w-ui/internal/egress"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/geoip"
	"github.com/abolfazl/w-ui/internal/i18n"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/keyfile"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/nodes"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/oversight"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/shaper"
	"github.com/abolfazl/w-ui/internal/single"
	"github.com/abolfazl/w-ui/internal/sysinfo"
	"github.com/abolfazl/w-ui/internal/update"
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
	log.Info("starting", "version", version, "listen", cfg.Listen, "scheme", cfg.Scheme())

	// Before anything else, and before the database is even opened.
	//
	// Two panels on one machine overwrite each other's firewall rules without
	// either noticing, which is a panel that looks healthy and enforces
	// nothing. Refusing to start is the only honest answer, and it has to come
	// first: a second panel that got as far as migrating a database or opening
	// an interface has already changed things.
	held, err := single.Claim(fmt.Sprintf("pid %d, data %s, listening on %s",
		os.Getpid(), cfg.DataDir, cfg.Listen))
	if err != nil {
		return err
	}
	defer held.Release()

	catalog, err := i18n.Load()
	if err != nil {
		return err
	}
	reportLocaleDrift(catalog, log)

	db, err := openPanelDatabase(cfg, log)
	if err != nil {
		return err
	}

	password, err := database.Bootstrap(db, cfg.DefaultLocale, log)
	if err != nil {
		return err
	}

	// What the settings page saved for this start is laid over the
	// environment now, before anything listens or schedules. Empty values
	// change nothing, which is how a fresh install keeps running on its
	// environment alone.
	settings := service.NewSettings(db, cfg.DefaultLocale)
	applySavedSettings(&cfg, settings, log)
	if password != "" {
		printFirstRunCredentials(password)
	}

	// The key sessions are signed with: a file of its own, never in a backup.
	jwtSecret, err := keyfile.Ensure(cfg.DataDir, "session.key", 32)
	if err != nil {
		return err
	}
	if err := database.DropRetired(db); err != nil {
		log.Warn("could not remove settings the panel no longer keeps", "error", err)
	}

	// OpenVPN keeps its per-interface files under the data directory, so the
	// whole of the panel's state stays in one place and one backup.
	backend.DataDir = cfg.DataDir

	// An OpenConnect tunnel that dials the panel's own name is given the
	// panel's certificate, which devices trust as they are.
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
	// Where a downloaded update waits for the root helper, on a server where
	// the panel may not replace its own binary.
	update.StageDir = filepath.Join(cfg.DataDir, update.StageDirName)
	hops := routing.NewHopManager(log, filepath.Join(cfg.DataDir, "hops"))

	// What the engine page steers on the outbound side, shared by the
	// outbounds, the routing and the engine settings that write it.
	tuning := egress.NewTuning()
	outbounds := egress.NewOutbounds(db, tuning, log)
	routes := egress.NewRouting(db, tuning, log)
	// One of each for the whole process: the API, the bot, the reports and
	// the node syncer all read and change the same customers and tunnels,
	// through the same live counts.
	ifaceSvc := service.NewInterfaces(db, pools, log)
	panelCert := service.PanelCert{CertFile: cfg.TLSCert, KeyFile: cfg.TLSKey}
	ifaceSvc.SetPanelCert(panelCert)
	clientsSvc := service.NewClients(db, pools, log)
	changes := service.NewChanges()
	clientsSvc.SetChanges(changes)
	// The engine page's settings. The ones that only take at start are
	// read now, before anything that uses them is built.
	engine := service.NewEngine(db, routes, tuning, dnsproxy.New(log), log)
	if ov := engine.Overrides(context.Background()); ov.CollectInterval > 0 {
		cfg.CollectInterval = ov.CollectInterval
	}
	engine.Apply(context.Background())
	notifier := notify.New(log)
	notifier.SetConfig(settings.Notify(context.Background()))
	notifier.SetMail(settings.Mail(context.Background()))
	if got, err := settings.Get(context.Background()); err == nil {
		routes.SetPanelOutbound(got.PanelOutbound)
	}

	// An outbound judged down, or back: the bot's outbound events.
	outbounds.DownThreshold = func() int { return notifier.Config().Thresholds.OutboundDown }
	outbounds.OnStatus = func(tag string, up bool, detail string) {
		kind, title := notify.KindOutboundDown, "Outbound down"
		if up {
			kind, title = notify.KindOutboundUp, "Outbound up"
		}
		notifier.Send(notify.Event{Kind: kind, Title: title, Body: tag + " — " + detail})
	}

	backups := newBackupService(db, cfg, cfg.BackupDir, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go ensureL2TPUnits(ctx, log, cfg.DataDir)

	// Which node this panel is. Everything it programs is filtered by it, and
	// everything belonging to another node is pushed there instead.
	local, err := database.LocalNode(db)
	if err != nil {
		return err
	}

	pool, err := openPool(ctx, db, local.ID, log)
	if err != nil {
		return err
	}
	// Deleting a tunnel, or changing what reaches the kernel, takes the live
	// device down; the reconciler brings the new one up.
	ifaceSvc.Teardown = pool.Destroy
	defer pool.CloseAll()

	subs := service.NewSubscriptions(db, pool, service.NewHosts(db, log), log)

	// The two built-in outbounds are created before the reconciler starts, so
	// its first tick already has somewhere to send traffic.
	if err := outbounds.EnsureBuiltins(ctx); err != nil {
		return err
	}
	// Names in the policy are resolved on a timer; the first pass runs now so
	// the opening tick is not applied with empty sets.
	if err := routes.MigrateRules(ctx); err != nil {
		return err
	}
	routes.StartResolver(ctx)
	// Country lists for "geoip:xx" entries, kept under the data directory.
	geo := geoip.New(filepath.Join(cfg.DataDir, "geoip"), log)
	routes.SetCountries(geo)
	go geo.Run(ctx)
	balancers := egress.NewBalancers(db, log)
	go balancers.RunChecks(ctx, outbounds)
	engine.RunDNS(ctx)

	rec := reconciler.New(reconciler.Options{
		DB:          db,
		Enforcer:    enforcer,
		Pool:        pool,
		Shaper:      shp,
		Router:      router,
		Hops:        hops,
		Policy:      routes.Policy,
		HopsOf:      func(ctx context.Context) ([]routing.HopSpec, error) { return outbounds.HopSpecs(ctx) },
		Notifier:    notifier,
		Interval:    cfg.CollectInterval,
		Log:         log,
		LocalNodeID: local.ID,
		Changes:     changes,
		PanelCert:   panelCert,
		DataDir:     cfg.DataDir,
		// A node's reports are this far apart: the span their numbers cover.
		NodeReportEvery: nodes.SyncInterval,
	})
	rec.Start(ctx)
	// The sharing record begins with this release; what earlier ones logged
	// is read back into it once, in the background: a year of log can take a
	// while, and nothing waits on it.
	go rec.ImportSharingOnce(ctx)
	// Outbound subscriptions are fetched on their own intervals.
	go egress.NewOutboundSubs(db, outbounds, log).Run(ctx)

	reportEnforcement(ctx, shp, router, enforcer, log)

	if err := reportReadiness(ctx, db, pools, log); err != nil {
		return err
	}

	sys, history := startReadings(ctx, cfg, log)

	notifier.Start(ctx)

	// Every tunnel brought up again from its stored configuration: what
	// the classic panel's "restart xray" is here. The bot offers it, and so does
	// `systemctl reload wui`, which sends SIGHUP.
	restartTunnels := tunnelRestarter(service.NewDataPlane(db, pool, local.ID, log))
	go reloadOnHangup(ctx, restartTunnels, log)

	startBot(ctx, notifier, clientsSvc, subs, ifaceSvc, settings, sys, backups, restartTunnels, log)

	// The host's own load, for the CPU and memory events.
	notifier.RunSystemMonitor(ctx, func() (float64, float64) {
		snap := sys.Snapshot()
		return snap.CPU.Percent, snap.Memory.Percent
	})
	// The periodic report, on the schedule the Telegram page sets.
	reporter := oversight.NewReporter(db, clientsSvc, settings, backups, version)
	notifier.RunReports(ctx, reporter.Build)
	// The automatic backup to the chat, on its own schedule.
	notifier.RunBackups(ctx, reporter.Backup, settings)
	// Traffic handed to an outside system, when one is configured.
	oversight.NewInformer(db, settings, log).Run(ctx, cfg.CollectInterval)

	prober := startNodes(ctx, db, rec, ifaceSvc, clientsSvc, notifier, log)

	startBackupSchedule(ctx, backups, settings, notifier)

	notifier.Send(notify.Event{
		Kind:  notify.KindPanel,
		Title: "Panel started",
		Body:  fmt.Sprintf("W-UI %s on %s", version, cfg.Listen),
	})

	srv, subOnly, err := buildServer(cfg, db, pools, catalog, enforcer, shp, settings, notifier, backups, prober,
		jwtSecret, sys, rec, outbounds, routes, router, subs, pool, local.ID, log,
		clientsSvc, ifaceSvc, changes,
		// Restart is a tidy stop: the service manager is set to bring the
		// panel back, and this run's history is saved on the way out.
		func() { stop() }, engine, balancers)
	if err != nil {
		return err
	}

	return serve(ctx, cfg, srv, subs, subOnly, history, log)
}

// startSubListener serves the subscription service on one address, over
// HTTPS when given a certificate. Its failure is logged, not fatal: the
// panel's own listener answers the same links (api.SubscriptionRouter).
func startSubListener(addr, certFile, keyFile string, handler http.Handler, log *slog.Logger) *http.Server {
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile != "" && keyFile != "" {
		if ld, err := certfile.New(certFile, keyFile); err == nil {
			conf.GetCertificate = ld.GetCertificate
		} else {
			log.Error("subscription certificate is unusable; serving it plain", "address", addr, "error", err)
			certFile, keyFile = "", ""
		}
	}
	srv := &http.Server{
		Addr:              addr,
		TLSConfig:         conf,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Info("subscription service listening", "address", addr, "tls", certFile != "")
		var err error
		if certFile != "" {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("subscription listener failed", "address", addr, "error", err)
		}
	}()
	return srv
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
	outbounds *egress.Outbounds,
	routes *egress.Routing,
	router *routing.Applier,
	subs *service.Subscriptions,
	pool *backend.Pool,
	localNodeID uint,
	log *slog.Logger,
	clientsSvc *service.Clients,
	ifaceSvc *service.Interfaces,
	changes *service.Changes,
	restart func(),
	engine *service.Engine,
	balancers *egress.Balancers,
) (*http.Server, http.Handler, error) {
	// Who may speak for somebody else. Done before anything serves, so no
	// request is ever handled with the wrong idea of where it came from.
	if bad := api.SetTrustedProxies(strings.Split(cfg.TrustedProxies, ",")); len(bad) > 0 {
		log.Warn("ignoring entries in WUI_TRUSTED_PROXIES that are not an address or a CIDR",
			"entries", strings.Join(bad, " "))
	}

	apiSrv := api.New(api.Options{
		Changes:        changes,
		Balancers:      balancers,
		Engine:         engine,
		Template:       service.NewTemplate(db, ifaceSvc, outbounds, routes, balancers, engine, log),
		DB:             db,
		Clients:        clientsSvc,
		Interfaces:     ifaceSvc,
		Catalog:        catalog,
		Enforcer:       enforcer,
		Shaper:         shp,
		Settings:       settings,
		Notifier:       notifier,
		Backups:        backups,
		Prober:         prober,
		JWTSecret:      jwtSecret,
		Logger:         log,
		Version:        version,
		Listen:         cfg.Listen,
		DBDriver:       string(cfg.DBDriver),
		DBSource:       config.RedactDSN(cfg.DBSource),
		SysInfo:        sys,
		Outbounds:      outbounds,
		Routing:        routes,
		Router:         router,
		Subs:           subs,
		Pool:           pool,
		LocalNodeID:    localNodeID,
		Reconciler:     rec,
		Restart:        restart,
		BasePath:       cfg.BasePath,
		TLSCert:        cfg.TLSCert,
		TLSKey:         cfg.TLSKey,
		TrustedProxies: cfg.TrustedProxies,
	})

	frontend, err := web.Handler(cfg.BasePath)
	if err != nil {
		return nil, nil, fmt.Errorf("frontend: %w", err)
	}

	root := http.NewServeMux()
	root.Handle("/api/", apiSrv.Routes())
	root.Handle("/", frontend)

	// Everything an administrator touches moves behind the configured prefix.
	// Subscriptions deliberately stay outside it: those links go to customers,
	// and putting the panel's own secret path into every one of them would
	// hand it to everybody who was ever sold a tunnel.
	app := web.MountAt(cfg.BasePath, root)

	// The subscription service sits ahead of the frontend so it can answer on
	// whatever path the operator configured, and is checked before the
	// single-page fallback swallows the request as a route.
	handler := apiSrv.SubscriptionRouter(app)

	// The subscription service alone, for a listener of its own.
	subOnly := api.LogRequests(log, api.SecureHeaders(apiSrv.SubscriptionRouter(http.NotFoundHandler())))

	// Nothing below TLS 1.2 is offered. The clients that need less are
	// older than any browser an operator will be signing in from.
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLS() {
		ld, err := certfile.New(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, nil, fmt.Errorf("certificate: %w", err)
		}
		tlsConf.GetCertificate = ld.GetCertificate
	}
	return &http.Server{
		Addr:              cfg.Listen,
		TLSConfig:         tlsConf,
		Handler:           api.LogRequests(log, api.SecureHeaders(handler)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, subOnly, nil
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
	l2tpdriver.Register()
	ikev2driver.Register()
	occdriver.Register()

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

// openPool brings up a driver for every interface that has one.
//
// A driver that will not open is recorded and skipped rather than aborting the
// boot: one broken interface should not take the panel -- and every other
// interface's enforcement -- down with it. The reconciler retries them, and
// picks up interfaces created after this point, which is what the old
// open-once map could not do.
func openPool(ctx context.Context, db *gorm.DB, nodeID uint, log *slog.Logger) (*backend.Pool, error) {
	// Only this node's tunnels. A driver for an interface that lives on another
	// server would open a device here that nobody dials, and its failure to open
	// would be reported as a fault on a machine that is working perfectly.
	var interfaces []model.Interface
	if err := db.Where("enabled = ? AND node_id = ?", true, nodeID).
		Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("load interfaces: %w", err)
	}

	pool := backend.NewPool(log)
	pool.Sync(ctx, interfaces)
	return pool, nil
}

// historySaveInterval is how often the long history is written to disk.
//
// Five minutes: often enough that a machine that is killed loses only a few
// points, rare enough that an idle panel is not writing a file all day. The
// finest tier holds an hour, so nothing older than the last write is ever at
// risk.
const historySaveInterval = 5 * time.Minute

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func importPending(db *gorm.DB, cfg config.Config, path string, log *slog.Logger) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return database.Import(context.Background(), db, cfg.DBDriver, f, log)
}

// newBackupService is the one place a backup is defined, for the panel and
// for the command line alike: the same archive, with the same contents,
// whichever asked for it.
func newBackupService(db *gorm.DB, cfg config.Config, dir string, log *slog.Logger) *backup.Service {
	dbFile := ""
	if cfg.DBDriver == config.DriverSQLite {
		dbFile = filepath.Base(cfg.DBSource)
	}
	// SQLite can write a consistent copy of itself while it is in use.
	// Copying the file byte by byte instead can catch it mid-write, and a
	// torn database is worth nothing at the moment it is needed. Another
	// engine has no file here to copy; its portable dump is the database.
	var snapshot func(ctx context.Context, dest string) error
	if cfg.DBDriver == config.DriverSQLite {
		snapshot = func(ctx context.Context, dest string) error {
			return db.WithContext(ctx).Exec("VACUUM INTO ?", dest).Error
		}
	}
	return backup.New(backup.Options{
		DataDir:  cfg.DataDir,
		Dir:      dir,
		Keep:     7,
		DBFile:   dbFile,
		Log:      log,
		Snapshot: snapshot,
		// And, for every engine, the copy that restores into any other.
		Export: func(ctx context.Context, w io.Writer) error {
			return database.Export(ctx, db, cfg.DBDriver, version, w)
		},
	})
}
