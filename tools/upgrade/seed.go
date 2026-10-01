package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

func seedCommand(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	url := fs.String("url", "", "the panel, with its URL path")
	user := fs.String("user", "", "administrator")
	pass := fs.String("pass", "", "password")
	out := fs.String("out", "", "directory to write the fixture into")
	version := fs.String("version", "", "the release being seeded")
	dbFile := fs.String("sqlite", "", "the panel's SQLite file, copied into the fixture")
	pgDSN := fs.String("postgres", "", "the panel's PostgreSQL database, read for its settings")
	listen := fs.String("listen", "", "the address the panel was listening on, kept for the test")
	subPort := fs.Int("sub-port", 0, "a port of its own for the subscription service")
	endpoint := fs.String("endpoint", "", "the address tunnels give customers, in place of vpn.example.test")
	subOnly := fs.Bool("subscription-only", false, "only set the subscription service up, for a release that opens its port on the next start")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*subOnly && (*url == "" || *out == "" || *version == "" || (*dbFile == "") == (*pgDSN == "")) {
		return errors.New("seed needs -url, -out, -version and one of -sqlite or -postgres")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if *out != "" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return err
		}
	}
	if err := upgradecheck.WaitUp(ctx, *url, time.Minute); err != nil {
		return err
	}
	c, err := upgradecheck.Login(ctx, *url, *user, *pass)
	if err != nil {
		return err
	}
	if *subOnly {
		return upgradecheck.SetUpSubscription(ctx, c, *subPort, nil)
	}
	m, err := upgradecheck.Seed(ctx, c, *out, *version, upgradecheck.Admin{Username: *user, Password: *pass},
		upgradecheck.SeedOptions{SubPort: *subPort, Endpoint: *endpoint})
	if err != nil {
		return err
	}
	m.Extra = map[string]any{"listen": *listen}

	var db *gorm.DB
	if *dbFile != "" {
		// A consistent copy of the live file, as the panel's own backup takes
		// one: the write-ahead log folded in, nothing torn.
		live, err := openSQLite(*dbFile)
		if err != nil {
			return err
		}
		dest := filepath.Join(*out, "wui.db")
		_ = os.Remove(dest)
		if err := live.Exec("VACUUM INTO ?", dest).Error; err != nil {
			return fmt.Errorf("copy the database: %w", err)
		}
		closeDB(live)
		if db, err = openSQLite(dest); err != nil {
			return err
		}
	} else if db, err = gorm.Open(postgres.Open(*pgDSN), quiet()); err != nil {
		return err
	}
	defer closeDB(db)
	if m.Settings, err = upgradecheck.StoredSettings(db); err != nil {
		return fmt.Errorf("read the stored settings: %w", err)
	}
	if err := m.Save(*out); err != nil {
		return err
	}
	fmt.Printf("seeded %s: %d interfaces, %d customers, %d settings -> %s\n",
		*version, len(m.Interfaces), len(m.Clients), len(m.Settings), *out)
	return nil
}

func verifyCommand(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	url := fs.String("url", "", "the panel, with its URL path")
	dir := fs.String("fixture", "", "the directory holding manifest.json")
	user := fs.String("user", "", "administrator, when not the manifest's")
	pass := fs.String("pass", "", "password, when not the manifest's")
	moved := fs.Bool("new-address", false, "the data was restored into another panel, whose own address the links now name")
	inUse := fs.String("in-use", "", "comma-separated customers whose tunnel carried traffic since the fixture was taken; what they used may grow")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := upgradecheck.Load(*dir)
	if err != nil {
		return err
	}
	if *user == "" {
		*user, *pass = m.Admin.Username, m.Admin.Password
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := upgradecheck.WaitUp(ctx, *url, 2*time.Minute); err != nil {
		return err
	}
	c, err := upgradecheck.Login(ctx, *url, *user, *pass)
	if err != nil {
		return err
	}
	if problems := upgradecheck.Verify(ctx, c, m, *dir, upgradecheck.Options{NewAddress: *moved, InUse: names(*inUse)}); len(problems) > 0 {
		return fmt.Errorf("%d problems after the upgrade:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("verified: %d interfaces and %d customers as %s left them, and their links answer\n",
		len(m.Interfaces), len(m.Clients), m.Version)
	return nil
}

func openSQLite(path string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(path), quiet())
}

func quiet() *gorm.Config { return &gorm.Config{Logger: gormlogger.Discard} }

func closeDB(db *gorm.DB) {
	if s, err := db.DB(); err == nil {
		s.Close()
	}
}

// names is the set of a comma-separated list.
func names(list string) map[string]bool {
	set := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			set[n] = true
		}
	}
	return set
}
