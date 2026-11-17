package database

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// openPanelDB opens a SQLite database the way the panel does.
func openPanelDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(config.Config{DBDriver: config.DriverSQLite, DBSource: filepath.Join(t.TempDir(), "wui.db")},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := db.DB(); err == nil {
		t.Cleanup(func() { raw.Close() })
	}
	return db
}

// Every connection the pool opens is set up the same: foreign keys checked
// and a writer that waits its turn, not only the first one.
func TestEveryConnectionIsSetUp(t *testing.T) {
	db := openPanelDB(t)
	raw, err := db.DB()
	must(t, err)
	ctx := context.Background()
	var conns []interface{ Close() error }
	for i := 0; i < sqliteConns; i++ {
		c, err := raw.Conn(ctx)
		must(t, err)
		conns = append(conns, c)
		var fk, busy int
		var mode string
		must(t, c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk))
		must(t, c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy))
		must(t, c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode))
		if fk != 1 || busy < 10000 || !strings.EqualFold(mode, "wal") {
			t.Fatalf("connection %d: foreign_keys=%d busy_timeout=%d journal_mode=%s", i, fk, busy, mode)
		}
	}
	for _, c := range conns {
		c.Close()
	}
}

// A backup reading the whole database holds neither the panel's reads nor
// its writes: both go on, at their usual speed, while it reads.
func TestABackupDoesNotHoldThePanelUp(t *testing.T) {
	db := openPanelDB(t)
	iface := model.Interface{Name: "wg0", Protocol: "wireguard", ListenPort: 51820, Subnet: "10.66.0.0/16", PrivateKey: "k"}
	must(t, db.Create(&iface).Error)
	var cs []model.Client
	for i := 0; i < 30000; i++ {
		cs = append(cs, model.Client{Name: fmt.Sprintf("c%d", i), SubToken: fmt.Sprintf("t%d", i), Status: model.StatusActive})
	}
	must(t, db.CreateInBatches(cs, 1000).Error)

	ctx := context.Background()
	dumping := make(chan struct{})
	var dumpErr error
	var dumpTook time.Duration
	go func() {
		defer close(dumping)
		start := time.Now()
		_, dumpErr = WriteDump(ctx, db, config.DriverSQLite, "t", slowSink{})
		dumpTook = time.Since(start)
	}()
	time.Sleep(50 * time.Millisecond)

	var worst time.Duration
	var n int
	for {
		select {
		case <-dumping:
			must(t, dumpErr)
			if n == 0 {
				t.Fatal("the backup ended before anything else was tried")
			}
			if worst > time.Second {
				t.Fatalf("while the backup read (%s), a read or write took %s", dumpTook, worst)
			}
			t.Logf("backup took %s; %d reads and writes beside it, the slowest %s", dumpTook.Round(time.Millisecond), n, worst.Round(time.Millisecond))
			return
		default:
		}
		start := time.Now()
		var c model.Client
		must(t, db.Where("sub_token = ?", fmt.Sprintf("t%d", n%30000)).First(&c).Error)
		must(t, db.Model(&model.Client{}).Where("id = ?", c.ID).Update("used_bytes", n).Error)
		if took := time.Since(start); took > worst {
			worst = took
		}
		n++
	}
}

// slowSink takes the dump slowly, as a backup written to a slow disk would,
// so the read stays open long enough to be in the way of anything it could be.
type slowSink struct{}

func (slowSink) Write(p []byte) (int, error) {
	time.Sleep(200 * time.Microsecond)
	return len(p), nil
}

// Writers at once queue for SQLite's lock and all get through; none is
// refused with "database is locked".
func TestWritersAtOnceAllGetThrough(t *testing.T) {
	db := openPanelDB(t)
	var wg sync.WaitGroup
	var failed atomic.Int64
	var firstErr atomic.Value
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				err := db.Transaction(func(tx *gorm.DB) error {
					var n int64
					if err := tx.Model(&model.Client{}).Count(&n).Error; err != nil {
						return err
					}
					return tx.Create(&model.Client{Name: fmt.Sprintf("w%d-%d", w, i), SubToken: fmt.Sprintf("w%d-%d", w, i)}).Error
				})
				if err != nil {
					failed.Add(1)
					firstErr.CompareAndSwap(nil, err)
				}
			}
		}(w)
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.Fatalf("%d of 1600 transactions failed: %v", failed.Load(), firstErr.Load())
	}
	var n int64
	db.Model(&model.Client{}).Count(&n)
	if n != 1600 {
		t.Fatalf("%d of 1600 written", n)
	}
}
