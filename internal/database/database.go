// Package database opens the store and keeps the schema current.
//
// The database is the panel's source of truth. Kernel state and the generated
// server config files are outputs derived from it, never inputs: on boot the
// reconciler rebuilds what the kernel holds from these tables, which is what
// makes a reboot or a crash self-healing instead of a resync problem.
package database

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/scope"
)

// Open connects to the configured database and applies migrations.
func Open(cfg config.Config, log *slog.Logger) (*gorm.DB, error) {
	gcfg := &gorm.Config{
		Logger:                                   gormLogger(cfg),
		DisableForeignKeyConstraintWhenMigrating: false,
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
	}

	var (
		db  *gorm.DB
		err error
	)
	switch cfg.DBDriver {
	case config.DriverSQLite:
		if err := os.MkdirAll(filepath.Dir(cfg.DBSource), 0o750); err != nil {
			return nil, fmt.Errorf("database: create data directory: %w", err)
		}
		// The pure-Go SQLite driver keeps the build CGO-free, so the panel
		// stays a single static binary that cross-compiles to any target.
		db, err = gorm.Open(sqlite.Open(sqliteDSN(cfg.DBSource)), gcfg)
	case config.DriverPostgres:
		db, err = gorm.Open(postgres.Open(cfg.DBSource), gcfg)
	default:
		return nil, fmt.Errorf("database: unsupported driver %q", cfg.DBDriver)
	}
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", cfg.DBDriver, err)
	}

	if cfg.DBDriver == config.DriverSQLite {
		if err := tuneSQLite(db); err != nil {
			return nil, err
		}
		keepPrivate(cfg.DBSource, log)
	}
	// Registered before anything can run a statement, so there is no window
	// in which a scoped request reaches an unguarded handle.
	if err := scope.Register(db); err != nil {
		return nil, fmt.Errorf("database: register access scoping: %w", err)
	}
	if err := Migrate(db); err != nil {
		return nil, err
	}

	log.Info("database ready", "driver", cfg.DBDriver, "source", config.RedactDSN(cfg.DBSource))
	return db, nil
}

// sqliteDSN is the database file with the settings every connection to it
// is opened with. On the connection string, not run once: a pool opens
// connections as it needs them, and a setting applied to the first is not on
// the second -- foreign keys unchecked, a writer that fails at once instead
// of waiting its turn.
//
//   - WAL: readers and one writer at once.
//   - synchronous FULL: a commit is on the disk when it returns. Under
//     NORMAL a power cut took every commit since the last checkpoint --
//     measured on the test server: a customer's whole 52 MB written over
//     ten seconds, gone -- and with it the usage written every tick for
//     customers near their allowance, whose kernel quota is rebuilt from
//     it after a reboot: what was lost was handed out again. The kernel's
//     counters do not survive a reboot to make it up. It costs an fsync
//     per commit, measured there at 1.2 ms (3 ms for a 5,000-row commit),
//     against a handful of commits a second.
//   - busy_timeout: a writer waits for the one before it, up to 15 s.
//   - _txlock=immediate: a transaction takes the write lock as it begins, so
//     two that read and then write queue instead of one failing with
//     "database is locked" when it comes to write. A long read that must not
//     hold writers back (the backup's dump) begins its own, deferred.
func sqliteDSN(source string) string {
	params := "_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(15000)&_txlock=immediate"
	if strings.Contains(source, "?") {
		return source + "&" + params
	}
	return source + "?" + params
}

// sqliteConns is how many connections the panel holds to a SQLite file.
//
// It was one, which put every read in the panel behind every other: the
// reconciler's pass over every customer, a backup's read of the whole
// database, each customer's subscription and each page of the panel, one
// after another. With tens of thousands of files a pass held it for seconds
// and a backup for a minute, and the panel stood still meanwhile. In WAL any
// number read at once beside the one writer; writers still go one at a time,
// SQLite's own lock and busy_timeout seeing to that.
const sqliteConns = 8

// tuneSQLite sizes the pool, and checks the settings took.
func tuneSQLite(db *gorm.DB) error {
	var mode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		return fmt.Errorf("database: read journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("database: SQLite is in %q mode, not WAL", mode)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("database: unwrap sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(sqliteConns)
	sqlDB.SetMaxIdleConns(sqliteConns)
	sqlDB.SetConnMaxLifetime(0)
	return nil
}

// KeyFileUsageSince is when this panel began counting each file's usage
// itself, in RFC 3339. A customer's per-user table covers the usage from then
// on; a period that began before it says so instead of showing a split it
// does not have.
const KeyFileUsageSince = "usage.files_since"

// startExactFileUsage sets every file's usage back to zero, once, and records
// when.
//
// Before this panel counted each file in the kernel, a file's usage was its
// customer's total shared out by estimate, and the estimate lost whatever was
// billed on a tick when the tunnel's own counter had not moved -- most of an
// OpenVPN customer's traffic. Those figures cannot be put right after the
// fact, so they are not kept: a table that starts from a stated time is true,
// and one carrying them forward would mix what was measured with what was not.
//
// A file a node holds for the panel that owns it is left alone: what it shows
// is not history but usage not yet reported, and setting it to zero would
// lose bytes the owning panel is about to be told about.
func startExactFileUsage(db *gorm.DB) error {
	if _, done, err := GetSetting(db, KeyFileUsageSince); err != nil || done {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE accounts SET up_bytes = 0, down_bytes = 0 WHERE origin_id = 0`).Error; err != nil {
			return fmt.Errorf("database: start counting each file: %w", err)
		}
		return PutSetting(tx, KeyFileUsageSince, time.Now().UTC().Format(time.RFC3339))
	})
}

// gormLogger keeps the ORM quiet unless it is asked not to be.
//
// The default writes to stdout, in colour, prefixed with the absolute source
// path of the machine the binary was built on — which lands a developer's home
// directory in an operator's terminal. Errors that matter are already returned
// and reported by the caller, so nothing is lost by discarding these.
func gormLogger(cfg config.Config) gormlogger.Interface {
	if !cfg.Debug {
		return gormlogger.Discard
	}
	return gormlogger.Default.LogMode(gormlogger.Info)
}

// keepPrivate makes the SQLite file, its write-ahead log and its shared
// memory the panel's user's alone. They hold every customer's keys and
// every administrator's second factor, and SQLite made them readable by
// everyone (0644); only the data directory's own mode stood between them
// and any other account on the server. SQLite gives the journals it makes
// later the file's own mode, so the file is what matters.
func keepPrivate(source string, log *slog.Logger) {
	path := strings.TrimPrefix(source, "file:")
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 == 0 {
			continue
		}
		if err := os.Chmod(p, info.Mode().Perm()&0o700); err != nil {
			log.Warn("could not make the database file private", "file", p, "error", err)
		}
	}
}
