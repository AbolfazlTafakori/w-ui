package database

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func migrated(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// The estimated per-file figures of an older panel are set to zero once, and
// the moment recorded; a panel that has already started counting each file
// itself keeps every byte it counted on every later start.
func TestExactFileUsageStartsOnce(t *testing.T) {
	db := migrated(t)
	if err := db.Exec(`DELETE FROM settings WHERE key = ?`, KeyFileUsageSince).Error; err != nil {
		t.Fatal(err)
	}
	own := model.Account{ClientID: 1, InterfaceID: 1, NodeID: 1, DeviceName: "a", IP: "10.66.0.2", UpBytes: 10, DownBytes: 90}
	held := model.Account{ClientID: 2, InterfaceID: 1, NodeID: 1, DeviceName: "b", IP: "10.66.0.3", UpBytes: 5, DownBytes: 5, OriginID: 77}
	if err := db.Create(&own).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&held).Error; err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Add(-time.Second)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var a, b model.Account
	db.First(&a, own.ID)
	db.First(&b, held.ID)
	if a.UpBytes+a.DownBytes != 0 {
		t.Errorf("this panel's file kept its estimate: up %d down %d", a.UpBytes, a.DownBytes)
	}
	if b.UpBytes != 5 || b.DownBytes != 5 {
		t.Errorf("a file a node holds for its panel lost unreported usage: up %d down %d", b.UpBytes, b.DownBytes)
	}
	v, ok, err := GetSetting(db, KeyFileUsageSince)
	if err != nil || !ok {
		t.Fatalf("the start was not recorded: %v %v", ok, err)
	}
	if at, err := time.Parse(time.RFC3339, v); err != nil || at.Before(before.Truncate(time.Second)) {
		t.Fatalf("recorded start %q", v)
	}

	// Counted since: a later start must not wipe it.
	if err := db.Model(&model.Account{}).Where("id = ?", own.ID).
		UpdateColumns(map[string]any{"up_bytes": 3, "down_bytes": 4}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	db.First(&a, own.ID)
	if a.UpBytes != 3 || a.DownBytes != 4 {
		t.Errorf("a restart wiped what was counted: up %d down %d", a.UpBytes, a.DownBytes)
	}
	if again, _, _ := GetSetting(db, KeyFileUsageSince); again != v {
		t.Errorf("the start moved from %q to %q", v, again)
	}
}
