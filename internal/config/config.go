// Package config loads boot-time settings from the environment.
//
// Only settings needed before the database opens live here. Everything an
// operator changes while the panel runs belongs in the settings table instead,
// so it can be edited from the UI.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Driver names a supported database engine.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

// Config is the panel's boot configuration.
type Config struct {
	// Listen is the panel's own HTTP address.
	Listen string

	// DBDriver selects the storage engine. SQLite is the default and is
	// adequate into the low thousands of accounts; Postgres is for deployments
	// where the traffic writer's insert rate outgrows a single file.
	DBDriver Driver
	DBSource string

	// DataDir holds the SQLite file, backups and generated profiles.
	DataDir string

	// CollectInterval is how often counters are drained and limits evaluated.
	// Two seconds matches what 3x-ui settled on and keeps the reporting lag
	// short; it is not what bounds quota accuracy, since the kernel enforces
	// the limit between ticks.
	CollectInterval time.Duration

	// DefaultLocale is the UI language for new admins. The panel ships
	// English-first with Persian as the second locale.
	DefaultLocale string

	LogLevel  string
	LogFormat string // text | json

	// Debug relaxes a few safety checks and is never appropriate in production.
	Debug bool
}

// Default returns the configuration used when nothing is set.
func Default() Config {
	return Config{
		Listen:          "127.0.0.1:2096",
		DBDriver:        DriverSQLite,
		DataDir:         "./data",
		CollectInterval: 2 * time.Second,
		DefaultLocale:   "en",
		LogLevel:        "info",
		LogFormat:       "text",
	}
}

// Load builds a configuration from defaults overridden by WUI_* environment
// variables, then validates it.
func Load() (Config, error) {
	c := Default()

	c.Listen = env("WUI_LISTEN", c.Listen)
	c.DBDriver = Driver(strings.ToLower(env("WUI_DB_DRIVER", string(c.DBDriver))))
	c.DBSource = env("WUI_DB_SOURCE", c.DBSource)
	c.DataDir = env("WUI_DATA_DIR", c.DataDir)
	c.DefaultLocale = strings.ToLower(env("WUI_DEFAULT_LOCALE", c.DefaultLocale))
	c.LogLevel = strings.ToLower(env("WUI_LOG_LEVEL", c.LogLevel))
	c.LogFormat = strings.ToLower(env("WUI_LOG_FORMAT", c.LogFormat))

	if v := os.Getenv("WUI_COLLECT_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: WUI_COLLECT_INTERVAL %q: %w", v, err)
		}
		c.CollectInterval = d
	}
	if v := os.Getenv("WUI_DEBUG"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("config: WUI_DEBUG %q: %w", v, err)
		}
		c.Debug = b
	}

	if c.DBDriver == DriverSQLite && c.DBSource == "" {
		c.DBSource = strings.TrimRight(c.DataDir, `/\`) + "/wui.db"
	}
	return c, c.Validate()
}

// Validate reports whether the configuration can be used.
func (c Config) Validate() error {
	switch c.DBDriver {
	case DriverSQLite, DriverPostgres:
	default:
		return fmt.Errorf("config: unknown database driver %q, want sqlite or postgres", c.DBDriver)
	}
	if c.DBSource == "" {
		return fmt.Errorf("config: WUI_DB_SOURCE is required for driver %q", c.DBDriver)
	}
	if c.Listen == "" {
		return fmt.Errorf("config: WUI_LISTEN must not be empty")
	}
	if c.CollectInterval < time.Second {
		return fmt.Errorf("config: WUI_COLLECT_INTERVAL is %s, minimum is 1s", c.CollectInterval)
	}
	switch c.DefaultLocale {
	case "en", "fa":
	default:
		return fmt.Errorf("config: unsupported locale %q, want en or fa", c.DefaultLocale)
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
