package database

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// A panel with many customers, to measure what a backup and a restore cost
// it, on both engines: run with WUI_TEST_PG_DSN pointing at an empty
// PostgreSQL and WUI_DUMP_LOAD set to the number of customers. Each customer
// has a file on every protocol, two addresses seen per file, a group, and
// twenty-five traffic samples. Skipped otherwise; it measures, and checks
// that what comes back is what went in.

var loadProtocols = []model.Protocol{model.ProtocolWireGuard, model.ProtocolOpenVPN, model.ProtocolL2TP,
	model.ProtocolPPTP, model.ProtocolIKEv2, model.ProtocolOpenConnect, model.ProtocolXray}

func loadDB(t *testing.T) (*gorm.DB, int) {
	t.Helper()
	dsn, n := os.Getenv("WUI_TEST_PG_DSN"), os.Getenv("WUI_DUMP_LOAD")
	if dsn == "" || n == "" {
		t.Skip("needs WUI_TEST_PG_DSN and WUI_DUMP_LOAD")
	}
	customers, err := strconv.Atoi(n)
	if err != nil || customers < 1 {
		t.Fatalf("WUI_DUMP_LOAD=%q", n)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	must(t, Migrate(db))
	var have int64
	db.Model(&model.Client{}).Count(&have)
	var protos int64
	db.Model(&model.Interface{}).Count(&protos)
	if int(have) == customers && int(protos) == len(loadProtocols) {
		return db, customers
	}
	must(t, db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public").Error)
	must(t, Migrate(db))

	var ifaces []model.Interface
	for i, p := range loadProtocols {
		f := model.Interface{Name: fmt.Sprintf("%s-load", p), Protocol: p, ListenPort: 40000 + i, Subnet: fmt.Sprintf("10.%d.0.0/16", 100+i), PrivateKey: "k", Enabled: true}
		must(t, db.Create(&f).Error)
		ifaces = append(ifaces, f)
	}
	now := time.Now().UTC()
	const chunk = 1000
	for start := 0; start < customers; start += chunk {
		var cs []model.Client
		for i := start; i < start+chunk && i < customers; i++ {
			exp := now.Add(time.Duration(i%90) * 24 * time.Hour)
			cs = append(cs, model.Client{Name: fmt.Sprintf("customer-%06d", i), SubToken: fmt.Sprintf("tok%027d", i),
				QuotaBytes: 50 << 30, UsedBytes: uint64(i) << 20, UpBytes: 1 << 40, DownBytes: 1<<53 + uint64(i),
				DeviceLimit: 5, ExpiresAt: &exp, Status: model.StatusActive})
		}
		must(t, db.CreateInBatches(cs, 500).Error)
		var accs []model.Account
		var samples []model.TrafficSample
		var groups []model.ClientGroup
		for _, c := range cs {
			groups = append(groups, model.ClientGroup{ClientID: c.ID, Name: fmt.Sprintf("plan-%d", c.ID%7)})
			for p, f := range ifaces {
				n := int(c.ID)
				a := model.Account{ClientID: c.ID, InterfaceID: f.ID, DeviceName: "device-1",
					IP: fmt.Sprintf("10.%d.%d.%d", 100+p, n>>8&255, n&255), PrivateKey: fmt.Sprintf("%044d", n),
					PublicKey: fmt.Sprintf("p%043d-%d", n, p), Username: fmt.Sprintf("u%d-%d", n, p), Secret: "s3cret",
					UpBytes: uint64(n) << 10, DownBytes: uint64(n) << 12}
				accs = append(accs, a)
			}
			for s := 0; s < 25; s++ {
				samples = append(samples, model.TrafficSample{ClientID: c.ID, BucketTS: now.Add(-time.Duration(s) * time.Hour), Granularity: "hour", RX: uint64(s) << 30, TX: uint64(s) << 20})
			}
		}
		must(t, db.CreateInBatches(groups, 1000).Error)
		must(t, db.CreateInBatches(accs, 1000).Error)
		var eps []model.AccountEndpoint
		for _, a := range accs {
			eps = append(eps, model.AccountEndpoint{AccountID: a.ID, Addr: fmt.Sprintf("5.%d.%d.%d", a.ID>>16&255, a.ID>>8&255, a.ID&255)},
				model.AccountEndpoint{AccountID: a.ID, Addr: fmt.Sprintf("2a01::%x", a.ID)})
		}
		must(t, db.CreateInBatches(eps, 2000).Error)
		must(t, db.CreateInBatches(samples, 2000).Error)
	}
	return db, customers
}

// peakHeap runs fn and reports the most heap it held above what was in use
// before, sampled every few milliseconds, and how long it took.
func peakHeap(fn func() error) (uint64, time.Duration, error) {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > base && m.HeapAlloc-base > peak.Load() {
					peak.Store(m.HeapAlloc - base)
				}
			}
		}
	}()
	start := time.Now()
	err := fn()
	took := time.Since(start)
	close(done)
	return peak.Load(), took, err
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

func TestDumpCostOnALargePanel(t *testing.T) {
	src, customers := loadDB(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "dump.jsonl")

	// The backup: the whole database streamed to a file.
	var sum *DumpSummary
	peak, took, err := peakHeap(func() error {
		f, err := os.Create(file)
		if err != nil {
			return err
		}
		defer f.Close()
		sum, err = WriteDump(ctx, src, config.DriverPostgres, "load", f)
		return err
	})
	must(t, err)
	st, _ := os.Stat(file)
	t.Logf("%d customers, %d rows in %d tables", customers, sum.Rows, len(sum.Tables))
	t.Logf("backup (dump)          %10s  peak heap %10s  %s", mib(st.Size()), mib(int64(peak)), took.Round(time.Millisecond))

	// The check a restore makes first.
	peak, took, err = peakHeap(func() error {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = CheckDump(bufio.NewReaderSize(f, 256<<10))
		return err
	})
	must(t, err)
	t.Logf("check                             peak heap %10s  %s", mib(int64(peak)), took.Round(time.Millisecond))

	load := func(name string, db *gorm.DB, drv config.Driver) {
		var got *DumpSummary
		peak, took, err := peakHeap(func() error {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer f.Close()
			got, err = LoadDump(ctx, db, drv, bufio.NewReaderSize(f, 256<<10), nil)
			return err
		})
		must(t, err)
		t.Logf("restore into %-10s           peak heap %10s  %s", name, mib(int64(peak)), took.Round(time.Millisecond))
		// What is now in the database, dumped again: the same, table by table.
		var check countingSink
		again, err := WriteDump(ctx, db, drv, "load", &check)
		must(t, err)
		for i := range sum.Tables {
			if again.Tables[i] != sum.Tables[i] {
				t.Errorf("%s: table %s is not what was backed up", name, sum.Tables[i].Name)
			}
		}
		_ = got
	}

	// Into a second PostgreSQL database, and into SQLite.
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	pg2, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	must(t, pg2.Exec("DROP DATABASE IF EXISTS wui_restore").Error)
	must(t, pg2.Exec("CREATE DATABASE wui_restore").Error)
	dst, err := gorm.Open(postgres.Open(replaceDB(dsn, "wui_restore")), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	must(t, Migrate(dst))
	load("PostgreSQL", dst, config.DriverPostgres)

	lite, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wui.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	must(t, Migrate(lite))
	load("SQLite", lite, config.DriverSQLite)
	if raw, err := lite.DB(); err == nil {
		raw.Close()
	}
	if raw, err := dst.DB(); err == nil {
		raw.Close()
	}
}

type countingSink struct{ n int64 }

func (c *countingSink) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// replaceDB is the DSN with another database's name.
func replaceDB(dsn, name string) string {
	q := ""
	if i := indexByte(dsn, '?'); i >= 0 {
		dsn, q = dsn[:i], dsn[i:]
	}
	if i := lastIndexByte(dsn, '/'); i >= 0 {
		dsn = dsn[:i+1] + name
	}
	return dsn + q
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
