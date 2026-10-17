package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/nodes"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/shaper"
	"github.com/abolfazl/w-ui/internal/sysinfo"
	"github.com/abolfazl/w-ui/internal/tgbot"
	"github.com/abolfazl/w-ui/internal/web"
)

// The steps the panel's start takes, each on its own so run reads as the
// order they come in.

// openPanelDatabase opens the panel's database, with a restore that was waiting
// applied first.
func openPanelDatabase(cfg config.Config, log *slog.Logger) (*gorm.DB, error) {
	// Before the database is opened, and that is the point: a restore replaces
	// the file it would be opened against, and SQLite's write-ahead log belongs
	// to whichever file was open when it was written. Applying a restore under
	// a running process means the log is checkpointed back over it and the
	// restore quietly undoes itself.
	dbFile := ""
	if cfg.DBDriver == config.DriverSQLite {
		dbFile = filepath.Base(cfg.DBSource)
	}
	restoredFrom, keepAddresses, restored := backup.ApplyPending(cfg.DataDir, dbFile, log)

	db, err := database.Open(cfg, log)
	if err != nil {
		return nil, err
	}

	// A restore that crossed engines, or came from a panel on the other one:
	// the archive's portable dump is loaded now that the schema is current.
	if pending := filepath.Join(cfg.DataDir, backup.PendingImportFile); fileExists(pending) {
		if err := importPending(db, cfg, pending, log); err != nil {
			log.Error("the restored backup could not be loaded into the database; the panel is running on the data it had",
				"error", err, "kept", pending+".failed")
			if err := os.Rename(pending, pending+".failed"); err != nil {
				log.Warn("could not set the failed restore aside", "error", err)
			}
		} else {
			if err := os.Remove(pending); err != nil {
				log.Warn("could not remove the loaded restore", "error", err)
			}
			restored = true
		}
	}

	if restored {
		log.Info("this panel is running on data restored from a backup", "archive", restoredFrom)
		// After the database is open, because this is a change to the restored
		// data: the addresses in the archive name the server it was taken on,
		// and this puts back the ones that name this one.
		if kept, unknown, err := backup.ApplyLocalAddresses(db, keepAddresses); err != nil {
			log.Error("restored, but this server's own addresses could not be put back",
				"error", err,
				"consequence", "customers may be handed the address of the server the backup came from")
		} else if keepAddresses != nil {
			log.Info("kept this server's own addresses over the ones in the backup",
				"interfaces", kept, "notOnThisServer", unknown)
			if unknown > 0 {
				log.Warn("some tunnels in the backup are not on this server and kept their old address",
					"count", unknown, "fix", "check the endpoint of each interface on the Interfaces page")
			}
		}
	}
	return db, nil
}

// applySavedSettings lays what the settings page saved for this start over
// the environment, before anything listens or schedules. Empty values change
// nothing, which is how a fresh install keeps running on its environment
// alone.
func applySavedSettings(cfg *config.Config, settings *service.Settings, log *slog.Logger) {
	ov := settings.Overrides(context.Background())
	if ov == (service.Overrides{}) {
		return
	}
	if ov.ListenHost != "" || ov.ListenPort > 0 {
		cfg.Listen = service.MergeListen(cfg.Listen, ov.ListenHost, ov.ListenPort)
	}
	if ov.BasePath != "" {
		cfg.BasePath = ov.BasePath
	}
	if ov.TLSCert != "" && ov.TLSKey != "" {
		cfg.TLSCert, cfg.TLSKey = ov.TLSCert, ov.TLSKey
	}
	if ov.TrustedProxies != "" {
		cfg.TrustedProxies = ov.TrustedProxies
	}
	if ov.TimeLocation != "" {
		if loc, err := time.LoadLocation(ov.TimeLocation); err == nil {
			time.Local = loc
		} else {
			log.Warn("ignoring the saved time zone", "zone", ov.TimeLocation, "error", err)
		}
	}
	log.Info("settings from the panel applied over the environment",
		"listen", cfg.Listen, "basePath", cfg.BasePath, "tls", cfg.TLS())
}

// reportEnforcement says once, at start, which of the panel's promises the
// machine can keep: speed limits, routing, quotas, and the page itself.
func reportEnforcement(ctx context.Context, shp shaper.Shaper, router *routing.Applier, enforcer enforce.Enforcer, log *slog.Logger) {
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
}

// startReadings starts reading the host's load, with the long history:
// exact for the last hour, averaged out to a week, and kept on disk so a
// restart does not erase the very thing somebody restarted the panel to go
// and look at. The caller saves the history on the way out.
func startReadings(ctx context.Context, cfg config.Config, log *slog.Logger) (*sysinfo.Collector, *sysinfo.Store) {
	sys := sysinfo.New(cfg.DataDir, cfg.CollectInterval, log)
	history := sysinfo.NewStore(filepath.Join(cfg.DataDir, "history.gob"))
	if err := history.Load(); err != nil {
		// Starting without it is a much smaller problem than not starting.
		log.Warn("could not read the stored history; starting a new one", "error", err)
	}
	sys.UseStore(history)
	sys.Start(ctx)

	// Written periodically, because a panel does not always get to shut down
	// tidily -- and the run that ends in a kill is the one whose history is
	// worth having. The shutdown write is done by the caller, in the shutdown
	// path itself: a goroutine woken by ctx.Done() races the process exiting
	// and loses it about as often as it wins.
	go func() {
		t := time.NewTicker(historySaveInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := history.Save(); err != nil {
					log.Warn("could not write the history", "error", err)
				}
			}
		}
	}()
	return sys, history
}

// tunnelRestarter brings every enabled tunnel on this server up again from
// its stored configuration, saying which would not come up.
func tunnelRestarter(dp *service.DataPlane) func(context.Context) error {
	return func(ctx context.Context) error {
		res, err := dp.RestartAll(ctx)
		if err != nil {
			return err
		}
		if len(res.Failures) == 0 {
			return nil
		}
		failed := make([]string, 0, len(res.Failures))
		for name, err := range res.Failures {
			failed = append(failed, name+": "+err.Error())
		}
		sort.Strings(failed)
		return errors.New(strings.Join(failed, "; "))
	}
}

// reloadOnHangup restarts every tunnel when the service manager asks for a
// reload.
func reloadOnHangup(ctx context.Context, restart func(context.Context) error, log *slog.Logger) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			log.Info("reload requested: restarting every tunnel")
			if err := restart(ctx); err != nil {
				log.Error("tunnel restart", "error", err)
			} else {
				log.Info("every tunnel restarted")
			}
		}
	}
}

// startBackupSchedule takes backups on the settings page's schedule. Both
// values are read again on every check rather than captured here, so
// changing either takes effect without a restart.
func startBackupSchedule(ctx context.Context, backups *backup.Service, settings *service.Settings, notifier *notify.Notifier) {
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
}

// startSubListeners serves the subscription service on its own address,
// when the settings ask for one that is not the panel's, and on every address
// it had before and still answers (SubSettings.Former): a link handed out on
// one of those is a customer's only link.
func startSubListeners(ctx context.Context, cfg config.Config, subs *service.Subscriptions, handler http.Handler, log *slog.Logger) []*http.Server {
	sc, err := subs.Settings(ctx)
	if err != nil {
		log.Warn("the subscription service's own addresses are not served", "error", err)
		return nil
	}
	var out []*http.Server
	taken := map[string]bool{cfg.Listen: true}
	if sc.Port > 0 {
		addr := net.JoinHostPort(sc.Listen, strconv.Itoa(sc.Port))
		if !taken[addr] {
			taken[addr] = true
			out = append(out, startSubListener(addr, sc.CertFile, sc.KeyFile, handler, log))
		}
	}
	for _, f := range sc.Former {
		addr := net.JoinHostPort(sc.Listen, strconv.Itoa(f.Port))
		if f.Port == 0 || taken[addr] {
			continue // the panel's own, or one already answered
		}
		taken[addr] = true
		certFile, keyFile := "", ""
		if f.TLS {
			certFile, keyFile = sc.CertFile, sc.KeyFile
		}
		out = append(out, startSubListener(addr, certFile, keyFile, handler, log))
	}
	return out
}

// startBot runs the Telegram bot: the same chat the notifications go to
// answers questions and takes orders, as the classic panel's bot does.
func startBot(ctx context.Context, notifier *notify.Notifier, clients *service.Clients, subs *service.Subscriptions,
	ifaces *service.Interfaces, settings *service.Settings, sys *sysinfo.Collector, backups *backup.Service,
	restartTunnels func(context.Context) error, log *slog.Logger) {
	go tgbot.New(tgbot.Deps{
		Config:   notifier.Config,
		Clients:  clients,
		Subs:     subs,
		Ifaces:   ifaces,
		Settings: settings,
		Sys:      sys.Snapshot,
		Version:  version,
		Backup: func(ctx context.Context) (string, []byte, error) {
			a, err := backups.Create(ctx)
			if err != nil {
				return "", nil, err
			}
			f, _, err := backups.Open(a.Name)
			if err != nil {
				return "", nil, err
			}
			defer f.Close()
			data, err := io.ReadAll(f)
			return a.Name, data, err
		},
		Restart: restartTunnels,
		LoginFailures: func() []string {
			var out []string
			for _, e := range logger.Recent.Recent(200, "warn", "request") {
				if p, _ := e.Fields["path"].(string); strings.HasSuffix(p, "/auth/login") {
					out = append(out, fmt.Sprintf("%s  %v  %v", e.Time.Local().Format("01-02 15:04"), e.Fields["ip"], e.Fields["status"]))
				}
			}
			return out
		},
	}, log).Run(ctx)
}

// startNodes watches the other servers and carries this panel's decisions
// out to them. Started before the HTTP listener so the nodes page has
// answers the first time it is opened rather than an empty table.
func startNodes(ctx context.Context, db *gorm.DB, rec *reconciler.Reconciler, ifaces *service.Interfaces,
	clients *service.Clients, notifier *notify.Notifier, log *slog.Logger) *nodes.Prober {
	prober := nodes.New(db, log)
	prober.OnChange = func(node model.Node, reachable bool, detail string) {
		kind, title := notify.KindNodeDown, "Node unreachable"
		if reachable {
			kind, title = notify.KindNodeUp, "Node reachable"
		}
		notifier.Send(notify.Event{Kind: kind, Title: title, Body: node.Name + " — " + detail})
	}
	prober.Start(ctx)

	// Carrying this panel's decisions out to the servers that terminate
	// tunnels, and bringing back what they counted. A node's traffic lands in
	// the same per-customer total the local kernel feeds, which is what makes
	// one allowance span every server the customer reaches.
	syncer := nodes.NewSyncer(db, rec.AddNodeReport, log)
	// The connections limit spans every server: what is live on each node
	// comes back here every few seconds, the reconciler counts it with what
	// this kernel sees, and a device it holds off on a node is told to that
	// node at once and again with every push.
	syncer.Sessions = rec.SetRemoteSessions
	syncer.Holds = rec.Holds
	syncer.XUIMirror = ifaces.MirrorXUI
	ifaces.SetRemote(nodes.Remote{})
	clients.SetRemote(nodes.Remote{})
	rec.OnHold = syncer.PushHold
	clients.SetLive(rec)
	ifaces.SetLive(rec)
	syncer.Start(ctx)
	return prober
}

// serve answers on the panel's address, and the subscription service on
// its own, until the panel is told to stop; then it saves the history and
// lets the requests in flight finish.
func serve(ctx context.Context, cfg config.Config, srv *http.Server, subs *service.Subscriptions,
	subOnly http.Handler, history *sysinfo.Store, log *slog.Logger) error {
	errc := make(chan error, 1)
	subSrvs := startSubListeners(ctx, cfg, subs, subOnly, log)
	go func() {
		log.Info("listening",
			"address", cfg.Listen,
			"url", cfg.Scheme()+"://"+cfg.Listen+cfg.BasePath,
			"tls", cfg.TLS())
		var err error
		if cfg.TLS() {
			// The certificate is read from its files again whenever they
			// change, so a renewal is served without a restart.
			err = srv.ListenAndServeTLS("", "")
		} else {
			// Worth saying once. An administrator's password crosses this
			// listener in the clear, and a panel that never mentions it is how
			// that goes unnoticed for a year.
			log.Warn("serving plain HTTP: sign-in credentials are not encrypted",
				"advice", "set WUI_TLS_CERT and WUI_TLS_KEY, or put a TLS-terminating proxy in front")
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Before the server is asked to stop, and on this goroutine rather than
	// another: an hour of readings is what somebody restarted the panel to go
	// and look at, and losing it on every tidy shutdown would make the whole
	// store worth only as much as the few minutes held in memory.
	if err := history.Save(); err != nil {
		log.Warn("could not write the history on the way out", "error", err)
	}

	// A fresh context: the one the panel ran under is already done, and the
	// requests in flight are given their ten seconds from now.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range subSrvs {
		if err := s.Shutdown(shutdownCtx); err != nil {
			log.Warn("the subscription listener did not stop cleanly", "address", s.Addr, "error", err)
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
