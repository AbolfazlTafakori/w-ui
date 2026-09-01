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
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
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
		db, err = gorm.Open(sqlite.Open(cfg.DBSource), gcfg)
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
	}
	if err := Migrate(db); err != nil {
		return nil, err
	}

	log.Info("database ready", "driver", cfg.DBDriver, "source", cfg.DBSource)
	return db, nil
}

// tuneSQLite applies the settings the collector's write rate depends on.
func tuneSQLite(db *gorm.DB) error {
	pragmas := []string{
		// WAL lets the collector write while the API reads.
		"PRAGMA journal_mode = WAL",
		// NORMAL trades an fsync per commit for durability only against power
		// loss, not process crashes. Traffic samples are re-derivable from the
		// kernel counters, so that trade is worth the write throughput.
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		// Wait rather than fail when a write collides with a reader.
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if err := db.Exec(p).Error; err != nil {
			return fmt.Errorf("database: %s: %w", p, err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("database: unwrap sql.DB: %w", err)
	}
	// SQLite serialises writers anyway; a single connection avoids
	// SQLITE_BUSY churn between the collector and the API.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	return nil
}

// Migrate brings the schema up to date.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}
	return nil
}

func gormLogger(cfg config.Config) gormlogger.Interface {
	level := gormlogger.Warn
	if cfg.Debug {
		level = gormlogger.Info
	}
	return gormlogger.Default.LogMode(level)
}
