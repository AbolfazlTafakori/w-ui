package database

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// A backup taken while the panel is busy -- customers made with their files,
// and deleted with them, all the while -- is one moment of the database: it
// loads with every foreign key checked, no file without its customer, no
// customer missing a file it had, whatever was changing as it was read.
func dumpWhileWriting(t *testing.T, db *gorm.DB, drv config.Driver) {
	t.Helper()
	ctx := context.Background()
	iface := model.Interface{Name: "wg-busy", Protocol: "wireguard", ListenPort: 51999, Subnet: "10.250.0.0/16", PrivateKey: "k"}
	must(t, db.Create(&iface).Error)
	for i := 0; i < 3000; i++ {
		c := model.Client{Name: fmt.Sprintf("seed-%d", i), SubToken: fmt.Sprintf("seed-%d", i)}
		must(t, db.Create(&c).Error)
		must(t, db.Create(&model.Account{ClientID: c.ID, InterfaceID: iface.ID, DeviceName: "d", IP: fmt.Sprintf("10.250.%d.%d", i/250, i%250+2)}).Error)
	}

	stop := make(chan struct{})
	var made, gone atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = db.Transaction(func(tx *gorm.DB) error {
				c := model.Client{Name: fmt.Sprintf("busy-%d", i), SubToken: fmt.Sprintf("busy-%d", i)}
				if err := tx.Create(&c).Error; err != nil {
					return err
				}
				for d := 0; d < 3; d++ {
					if err := tx.Create(&model.Account{ClientID: c.ID, InterfaceID: iface.ID, DeviceName: fmt.Sprintf("d%d", d), IP: fmt.Sprintf("10.251.%d.%d", i%250, d+2+(i/250)*3%240)}).Error; err != nil {
						return err
					}
				}
				made.Add(1)
				return nil
			})
			// And one of the older ones goes, with its files.
			var old model.Client
			if db.Where("name LIKE ?", "seed-%").Order("id").Limit(1).Find(&old).Error == nil && old.ID != 0 {
				if db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Where("client_id = ?", old.ID).Delete(&model.Account{}).Error; err != nil {
						return err
					}
					return tx.Delete(&model.Client{}, old.ID).Error
				}) == nil {
					gone.Add(1)
				}
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	var buf bytes.Buffer
	_, err := WriteDump(ctx, db, drv, "busy", &buf)
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	must(t, err)
	if made.Load() == 0 || gone.Load() == 0 {
		t.Fatalf("the panel was not busy while the backup ran: %d made, %d gone", made.Load(), gone.Load())
	}

	// Loaded with every foreign key enforced, and checked: every file has
	// its customer, and every busy customer has all three of its files.
	dst, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "dst.db")+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: gormlogger.Discard})
	must(t, err)
	if raw, err := dst.DB(); err == nil {
		t.Cleanup(func() { raw.Close() })
	}
	must(t, Migrate(dst))
	if _, err := LoadDump(ctx, dst, config.DriverSQLite, bytes.NewReader(buf.Bytes()), nil); err != nil {
		t.Fatalf("a backup taken while busy did not load: %v", err)
	}
	var orphans, short int64
	dst.Raw(`SELECT COUNT(*) FROM accounts a LEFT JOIN clients c ON c.id = a.client_id WHERE c.id IS NULL`).Scan(&orphans)
	dst.Raw(`SELECT COUNT(*) FROM clients c WHERE c.name LIKE 'busy-%' AND (SELECT COUNT(*) FROM accounts a WHERE a.client_id = c.id) <> 3`).Scan(&short)
	if orphans != 0 || short != 0 {
		t.Fatalf("not one moment: %d files without their customer, %d customers missing files", orphans, short)
	}
	var n int64
	dst.Model(&model.Client{}).Count(&n)
	t.Logf("%s: dumped while %d customers were made and %d deleted; it loaded whole, %d customers, every file with its owner", drv, made.Load(), gone.Load(), n)
}

func TestADumpTakenWhileBusyIsOneMoment(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "busy.db")+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)"), &gorm.Config{Logger: gormlogger.Discard})
		must(t, err)
		if raw, err := db.DB(); err == nil {
			t.Cleanup(func() { raw.Close() })
		}
		must(t, Migrate(db))
		dumpWhileWriting(t, db, config.DriverSQLite)
	})
	t.Run("postgres", func(t *testing.T) {
		dumpWhileWriting(t, pgFresh(t), config.DriverPostgres)
	})
}
