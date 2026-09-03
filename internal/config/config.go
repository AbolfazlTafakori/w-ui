// Package config loads boot-time settings from the environment.
//
// Only settings needed before the database opens live here. Everything an
// operator changes while the panel runs belongs in the settings table instead,
// so it can be edited from the UI.
package config

import (
	"crypto/tls"
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

	// TLSCert and TLSKey turn the panel's own listener into an HTTPS one.
	//
	// The panel terminates TLS itself rather than expecting a reverse proxy in
	// front of it. A server that is already running somebody else's nginx must
	// not have its configuration rewritten by this panel's installer, and a
	// server running nothing else should not have to grow a web server just to
	// get a certificate in front of a login form.
	//
	// Empty means plain HTTP, which is only defensible behind a proxy or an
	// SSH tunnel: the sign-in POST carries an administrator's password.
	TLSCert string
	TLSKey  string

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
	c.TLSCert = env("WUI_TLS_CERT", c.TLSCert)
	c.TLSKey = env("WUI_TLS_KEY", c.TLSKey)
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
	if err := c.validateTLS(); err != nil {
		return err
	}
	return nil
}

// TLS reports whether the panel should serve HTTPS.
func (c Config) TLS() bool { return c.TLSCert != "" && c.TLSKey != "" }

// Scheme is the URL scheme the panel answers on.
func (c Config) Scheme() string {
	if c.TLS() {
		return "https"
	}
	return "http"
}

// validateTLS refuses a certificate that will not work, at boot.
//
// The alternative is a panel that starts, reports itself healthy, and then
// fails every connection -- which looks like a network problem and is not one.
// A renewal that wrote a certificate the key no longer matches is caught here
// too, on the restart that follows it.
func (c Config) validateTLS() error {
	switch {
	case c.TLSCert == "" && c.TLSKey == "":
		return nil
	case c.TLSCert == "":
		return fmt.Errorf("config: WUI_TLS_KEY is set without WUI_TLS_CERT")
	case c.TLSKey == "":
		return fmt.Errorf("config: WUI_TLS_CERT is set without WUI_TLS_KEY")
	}
	for _, f := range []struct{ what, path string }{
		{"WUI_TLS_CERT", c.TLSCert},
		{"WUI_TLS_KEY", c.TLSKey},
	} {
		if _, err := os.Stat(f.path); err != nil {
			return fmt.Errorf("config: %s %q: %w", f.what, f.path, err)
		}
	}
	if _, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey); err != nil {
		return fmt.Errorf("config: the certificate and key do not form a usable pair: %w", err)
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
