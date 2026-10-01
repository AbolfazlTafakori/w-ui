package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

// Upgrading must never break an install. Each fixture is the database an
// earlier release made -- built from its tag, seeded through its own API, by
// scripts/upgrade/make-fixtures.sh -- and this panel is started on it as an
// update leaves it: same data, same address. Everything that release said
// about its tunnels and customers must still be so, every link it handed out
// must still serve a configuration that connects the same way, and every
// setting it stored must still be there.

const fixtures = "../../internal/upgradecheck/testdata"

// fixtureDirs is every release with a fixture, oldest first.
func fixtureDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[i], out[j]) })
	if len(out) == 0 {
		t.Fatal("no fixtures; run scripts/upgrade/make-fixtures.sh")
	}
	return out
}

func versionLess(a, b string) bool {
	var x, y [3]int
	fmt.Sscanf(a, "v%d.%d.%d", &x[0], &x[1], &x[2])
	fmt.Sscanf(b, "v%d.%d.%d", &y[0], &y[1], &y[2])
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func TestUpgradeFromEveryEarlierRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a panel per release")
	}
	for _, v := range fixtureDirs(t) {
		t.Run(v, func(t *testing.T) {
			dir := filepath.Join(fixtures, v)
			m, err := upgradecheck.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			data := t.TempDir()
			copyFile(t, filepath.Join(dir, "wui.db"), filepath.Join(data, "wui.db"))
			env := panelEnv(data, listenOf(t, m))

			// Started twice: the first start migrates, the second finds
			// nothing left to do and must change nothing.
			for _, start := range []string{"first start", "second start"} {
				p := startPanel(t, env, listenOf(t, m))
				checkPanel(t, start, p, m, dir, upgradecheck.Options{})
				checkSettings(t, start, sqliteFile(t, filepath.Join(data, "wui.db")), m)
				p.stop()
			}
		})
	}
}

// A backup taken by any earlier release restores into this one: staged from
// the terminal, applied as the panel starts, and everything in it as that
// release left it.
func TestEveryEarlierBackupRestores(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a panel per release")
	}
	for _, v := range fixtureDirs(t) {
		t.Run(v, func(t *testing.T) {
			dir := filepath.Join(fixtures, v)
			m, err := upgradecheck.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			data := t.TempDir()
			env := panelEnv(data, listenOf(t, m))
			// The panel this is restored into is a new install.
			if out, err := runCLI(t, env, "admin", "reset", "--username", "newadmin", "--password", "new-install-pass-1"); err != nil {
				t.Fatalf("a new install: %v\n%s", err, out)
			}
			if out, err := runCLI(t, env, "backup", "restore", filepath.Join(dir, m.Backup),
				"--dir", filepath.Join(data, "backups")); err != nil {
				t.Fatalf("staging the %s backup: %v\n%s", v, err, out)
			}
			p := startPanel(t, env, listenOf(t, m))
			if !strings.Contains(p.output(), "restored from a backup") {
				t.Errorf("the staged restore was not applied:\n%s", p.output())
			}
			checkPanel(t, "restored", p, m, dir, upgradecheck.Options{NewAddress: true})
		})
	}
}

// listenOf is the address the fixture's panel listened on, which its links
// name.
func listenOf(t *testing.T, m *upgradecheck.Manifest) string {
	t.Helper()
	l, _ := m.Extra["listen"].(string)
	if l == "" {
		t.Fatal("the manifest does not say where the panel listened")
	}
	return l
}

// checkPanel signs in to p as the fixture's administrator and checks it
// against the manifest.
func checkPanel(t *testing.T, when string, p *panel, m *upgradecheck.Manifest, dir string, opts upgradecheck.Options) {
	t.Helper()
	ctx := context.Background()
	c, err := upgradecheck.Login(ctx, p.base, m.Admin.Username, m.Admin.Password)
	if err != nil {
		t.Fatalf("%s: the %s administrator cannot sign in: %v\n%s", when, m.Version, err, p.output())
	}
	for _, problem := range upgradecheck.Verify(ctx, c, m, dir, opts) {
		t.Errorf("%s: %s", when, problem)
	}
}

// checkSettings: every setting the earlier release stored is still stored,
// with the same value. A panel that renamed or dropped one would lose an
// operator's configuration on update.
func checkSettings(t *testing.T, when string, db *gorm.DB, m *upgradecheck.Manifest) {
	t.Helper()
	now, err := upgradecheck.StoredSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	for k, was := range m.Settings {
		is, ok := now[k]
		switch {
		case !ok:
			t.Errorf("%s: setting %s is gone", when, k)
		case is != was:
			t.Errorf("%s: setting %s was %q, is %q", when, k, was, is)
		}
	}
}

func sqliteFile(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})
	return db
}

// The same on PostgreSQL. The fixtures are made where a server is -- CI's,
// by make-fixtures.sh with WUI_FIXTURE_PG_DSN -- into the directory
// WUI_UPGRADE_PG_FIXTURES names; each pg-vX.Y.Z there is a dump of the
// database that release made, loaded into a database of its own here.
func TestUpgradeOnPostgres(t *testing.T) {
	dsn, dir := os.Getenv("WUI_TEST_PG_DSN"), os.Getenv("WUI_UPGRADE_PG_FIXTURES")
	if dsn == "" || dir == "" {
		t.Skip("needs WUI_TEST_PG_DSN and WUI_UPGRADE_PG_FIXTURES")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "pg-v") {
			continue
		}
		ran++
		fx := filepath.Join(dir, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			m, err := upgradecheck.Load(fx)
			if err != nil {
				t.Fatal(err)
			}
			own := freshPostgres(t, dsn, "wui_upgrade_"+strings.NewReplacer("-", "_", ".", "_").Replace(e.Name()))
			dump, err := os.ReadFile(filepath.Join(fx, "pg.sql"))
			if err != nil {
				t.Fatal(err)
			}
			db := postgresDB(t, own)
			if err := db.Exec(string(dump)).Error; err != nil {
				t.Fatalf("loading the %s dump: %v", m.Version, err)
			}
			env := panelEnv(t.TempDir(), listenOf(t, m), "WUI_DB_DRIVER=postgres", "WUI_DB_SOURCE="+own)
			for _, start := range []string{"first start", "second start"} {
				p := startPanel(t, env, listenOf(t, m))
				checkPanel(t, start, p, m, fx, upgradecheck.Options{})
				checkSettings(t, start, db, m)
				p.stop()
			}
		})
	}
	if ran == 0 {
		t.Fatalf("no pg-v* fixtures in %s", dir)
	}
}

// freshPostgres makes an empty database called name on the server dsn names,
// and returns the DSN that reaches it.
func freshPostgres(t *testing.T, dsn, name string) string {
	t.Helper()
	admin := postgresDB(t, dsn)
	if err := admin.Exec(`DROP DATABASE IF EXISTS ` + name).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(`CREATE DATABASE ` + name).Error; err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	t.Cleanup(func() {
		// The panel and this test's own connection are gone by now.
		_ = postgresDB(t, dsn).Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`).Error
	})
	return u.String()
}

func postgresDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})
	return db
}

// newSinceNewestFixture are setting keys this build writes that the newest
// fixture does not hold yet, each with the release that added it. A key added
// before a release is listed here; once that release ships, its fixture is
// added (scripts/upgrade/make-fixtures.sh) and the entry is removed.
var newSinceNewestFixture = map[string]string{}

// runtimeKeys are kept by the panel for itself, not set by an operator.
var runtimeKeys = map[string]bool{
	"notify.backupLastAt": true, "notify.backupLastSchedule": true,
}

// The newest fixture holds every setting this build can store, so the
// upgrade test above checks each one survives. A key added without a fixture
// or an entry in newSinceNewestFixture fails here, rather than going into a
// release no upgrade test has seen.
func TestTheNewestFixtureHoldsEverySetting(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a panel")
	}
	dirs := fixtureDirs(t)
	newest := filepath.Join(fixtures, dirs[len(dirs)-1])
	m, err := upgradecheck.Load(newest)
	if err != nil {
		t.Fatal(err)
	}

	// What this build stores when every settings page is saved: a new
	// install, given the same settings the fixtures are.
	data := t.TempDir()
	listen := "127.0.0.1:47391"
	env := panelEnv(data, listen)
	if out, err := runCLI(t, env, "admin", "reset", "--username", "keys", "--password", "every-setting-1"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	p := startPanel(t, env, listen)
	ctx := context.Background()
	c, err := upgradecheck.Login(ctx, p.base, "keys", "every-setting-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradecheck.SetUpSubscription(ctx, c, 47392, nil); err != nil {
		t.Fatal(err)
	}
	var iface struct {
		Interface struct {
			ID uint `json:"id"`
		} `json:"interface"`
	}
	if err := c.Do(ctx, "POST", "api/interfaces", map[string]any{"name": "keys-wg", "protocol": "wireguard",
		"listenPort": 51991, "subnet": "10.91.0.0/24", "endpointHost": "vpn.example.test"}, &iface); err != nil {
		t.Fatal(err)
	}
	if _, err := upgradecheck.SeedSettings(ctx, c, iface.Interface.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := upgradecheck.StoredSettings(sqliteFile(t, filepath.Join(data, "wui.db")))
	if err != nil {
		t.Fatal(err)
	}
	p.stop()

	for k := range stored {
		if runtimeKeys[k] || newSinceNewestFixture[k] != "" {
			continue
		}
		if _, ok := m.Settings[k]; !ok {
			t.Errorf("this build stores %s, which the newest fixture (%s) does not hold: "+
				"add the release that brings it as a fixture, or list it in newSinceNewestFixture", k, m.Version)
		}
	}
	for k, release := range newSinceNewestFixture {
		if _, ok := m.Settings[k]; ok {
			t.Errorf("%s is listed as new in %s, but the newest fixture holds it: remove the entry", k, release)
		}
	}
}
