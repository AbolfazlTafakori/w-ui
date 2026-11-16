package database

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A panel with many customers, to measure what a backup costs it: run with
// WUI_TEST_PG_DSN pointing at an empty PostgreSQL and WUI_DUMP_LOAD set to
// the number of customers (each with five files and twenty-five traffic
// samples). Skipped otherwise; it is a measurement, not a check.

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
	var have int64
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Client{}).Count(&have)
	if int(have) == customers {
		return db, customers
	}
	for _, m := range []string{"traffic_samples", "user_subs", "accounts", "client_groups", "clients", "interfaces"} {
		db.Exec("DELETE FROM " + m)
	}
	iface := model.Interface{Name: "wg-load", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.0.0.0/8", PrivateKey: "k"}
	if err := db.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const chunk = 1000
	for start := 0; start < customers; start += chunk {
		var cs []model.Client
		for i := start; i < start+chunk && i < customers; i++ {
			cs = append(cs, model.Client{Name: fmt.Sprintf("c%06d", i), SubToken: fmt.Sprintf("tok%026d", i),
				QuotaBytes: 50 << 30, UsedBytes: uint64(i) << 20, UpBytes: 1 << 40, DownBytes: 1<<53 + uint64(i), DeviceLimit: 5})
		}
		if err := db.CreateInBatches(cs, 500).Error; err != nil {
			t.Fatal(err)
		}
		var accs []model.Account
		var samples []model.TrafficSample
		for _, c := range cs {
			for d := 0; d < 5; d++ {
				n := int(c.ID)*5 + d
				accs = append(accs, model.Account{ClientID: c.ID, InterfaceID: iface.ID, DeviceName: fmt.Sprintf("user-%d", d+1),
					IP: fmt.Sprintf("10.%d.%d.%d", n>>16&255, n>>8&255, n&255), PrivateKey: fmt.Sprintf("%044d", n), PublicKey: fmt.Sprintf("p%043d", n)})
			}
			for s := 0; s < 25; s++ {
				samples = append(samples, model.TrafficSample{ClientID: c.ID, BucketTS: now.Add(-time.Duration(s) * time.Hour), Granularity: "hour", RX: uint64(s) << 30, TX: uint64(s) << 20})
			}
		}
		if err := db.CreateInBatches(accs, 1000).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.CreateInBatches(samples, 2000).Error; err != nil {
			t.Fatal(err)
		}
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

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

func TestDumpCostOnALargePanel(t *testing.T) {
	db, customers := loadDB(t)
	ctx := context.Background()
	// The new dump first: the old export drives the collector's goal up to
	// the gigabyte it holds, and anything measured after it would be charged
	// with garbage the collector had no reason to clear yet.
	var w2 countingWriter
	peak, took, err := peakHeap(func() error { _, err := WriteDump(ctx, db, "postgres", "load", &w2); return err })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("new dump, %d customers: %s written, peak heap %s, %s", customers, mib(w2.n), mib(int64(peak)), took.Round(time.Millisecond))

	var w countingWriter
	peak, took, err = peakHeap(func() error { return Export(ctx, db, "postgres", "load", &w) })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("old JSON export, %d customers: %s written, peak heap %s, %s", customers, mib(w.n), mib(int64(peak)), took.Round(time.Millisecond))
	_ = io.Discard
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }
