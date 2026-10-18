package database

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func bareDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// Every migration runs once, is recorded, and the numbers only go up: a
// database says which it has been through, and a second start runs none.
func TestMigrationsRunOnceInOrderAndAreRecorded(t *testing.T) {
	for i, m := range migrations {
		if m.id != i+1 {
			t.Fatalf("migration %q is number %d, want %d: numbers are never reused or reordered", m.name, m.id, i+1)
		}
	}
	db := bareDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var rows []schemaMigration
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(migrations) {
		t.Fatalf("%d recorded, %d migrations", len(rows), len(migrations))
	}
	first := rows[len(rows)-1].AppliedAt
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var again []schemaMigration
	db.Order("id").Find(&again)
	if len(again) != len(rows) || !again[len(again)-1].AppliedAt.Equal(first) {
		t.Fatal("a second start ran the migrations again")
	}
}

// Running the data migrations twice changes nothing -- what lets a restore
// put older rows through them again.
func TestDataMigrationsCanRunAgain(t *testing.T) {
	db := bareDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	c := model.Client{Name: "ali", Group: "vip"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE admins SET role = '' `)
	for range 2 {
		if err := Replay(db); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	db.Model(&model.ClientGroup{}).Where("client_id = ? AND name = ?", c.ID, "vip").Count(&n)
	if n != 1 {
		t.Fatalf("%d group rows for one label carried twice", n)
	}
}
