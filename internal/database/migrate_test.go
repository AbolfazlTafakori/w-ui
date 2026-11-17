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

// The files a 3x-ui or Pasarguard node already has are this panel's: marked
// placed, so the first round after the update keeps their clients rather
// than making every one again. Files on its own server or a W-UI node, and
// users brought in from the node, are left as they are.
func TestFilesAlreadyOnAnXrayNodeAreMarkedPlaced(t *testing.T) {
	db := bareDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []model.Node{
		{ID: 2, Name: "x", Kind: model.KindXUI}, {ID: 3, Name: "p", Kind: model.KindPG}, {ID: 4, Name: "w", Kind: model.KindRemote},
	} {
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []model.Account{
		{ID: 1, NodeID: 2, ClientID: 1, InterfaceID: 1, IP: "10.0.0.1"},
		{ID: 2, NodeID: 3, ClientID: 1, InterfaceID: 2, IP: "10.0.0.2"},
		{ID: 3, NodeID: 4, ClientID: 1, InterfaceID: 3, IP: "10.0.0.3"},
		{ID: 4, NodeID: 2, ClientID: 1, InterfaceID: 1, IP: "10.0.0.4", Adopted: "theirs-1"},
	} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := markPlacedOnXrayNodes(db); err != nil {
		t.Fatal(err)
	}
	want := map[uint]bool{1: true, 2: true, 3: false, 4: false}
	var got []model.Account
	db.Order("id").Find(&got)
	for _, a := range got {
		if a.XUIPlaced != want[a.ID] {
			t.Errorf("account %d placed %v, want %v", a.ID, a.XUIPlaced, want[a.ID])
		}
	}
}
