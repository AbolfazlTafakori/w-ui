package database

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// A commit the panel makes is on the disk when it returns: synchronous FULL,
// read back from the connection itself. Under NORMAL a power cut on the
// test server took a customer's whole 52 MB of usage written over ten
// seconds, and the kernel quota of those near their allowance was rebuilt
// from what was left -- the loss handed out again.
func TestTheDatabaseIsOpenedDurable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(sqliteDSN(filepath.Join(t.TempDir(), "w.db"))), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var mode int
	if err := db.Raw("PRAGMA synchronous").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("synchronous = %d, want 2 (FULL): a power cut would lose what was committed", mode)
	}
	var journal string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journal).Error; err != nil || journal != "wal" {
		t.Fatalf("journal mode %q (%v), want wal", journal, err)
	}
}
