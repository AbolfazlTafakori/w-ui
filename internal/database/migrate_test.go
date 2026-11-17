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

// The sharing record learns whose customer each entry was: an entry whose
// customer still exists takes their owner, one whose customer is gone stays
// the owner's alone, and one already owned is left as it is.
func TestTheSharingRecordIsOwned(t *testing.T) {
	db := bareDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Client{{ID: 1, Name: "a", OwnerID: 7}, {ID: 2, Name: "b", OwnerID: 0}} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ev := range []model.SharingEvent{
		{ID: 1, Kind: "shared", ClientID: 1, ClientName: "a", DeviceName: "d", Source: "live"},
		{ID: 2, Kind: "shared", ClientID: 2, ClientName: "b", DeviceName: "d", Source: "live"},
		{ID: 3, Kind: "shared", ClientID: 9, ClientName: "gone", DeviceName: "d", Source: "live"},
		{ID: 4, Kind: "held", ClientID: 1, ClientName: "a", DeviceName: "d", Source: "live", OwnerID: 5},
	} {
		if err := db.Create(&ev).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 { // and again: nothing more changes
		if err := ownSharingRecord(db); err != nil {
			t.Fatal(err)
		}
	}
	want := map[uint]uint{1: 7, 2: 0, 3: 0, 4: 5}
	var got []model.SharingEvent
	db.Order("id").Find(&got)
	for _, ev := range got {
		if ev.OwnerID != want[ev.ID] {
			t.Errorf("entry %d owned by %d, want %d", ev.ID, ev.OwnerID, want[ev.ID])
		}
	}
}

// An operator's user ceiling becomes an open account: granted is the old
// ceiling, used the seats of customers still served (an ended one gives its
// seats back), an admin's handed what their resellers were given, and the
// ledger sums to each figure. A second run changes nothing.
func TestUserCeilingsBecomeOpenAccounts(t *testing.T) {
	db := bareDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	db.Exec(`DELETE FROM capacity_accounts`)
	for _, a := range []model.Admin{
		{ID: 2, Username: "ad", PasswordHash: "x", Role: model.RoleAdmin, ClientLimit: 100},
		{ID: 3, Username: "ra", PasswordHash: "x", Role: model.RoleReseller, ClientLimit: 30, ParentAdminID: 2},
		{ID: 4, Username: "rb", PasswordHash: "x", Role: model.RoleReseller, ClientLimit: 0},
	} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, c := range []model.Client{
		{OwnerID: 3, DeviceLimit: 2, Status: model.StatusActive},
		{OwnerID: 3, DeviceLimit: 4, Status: model.StatusDisabled},
		{OwnerID: 3, DeviceLimit: 5, Status: model.StatusExpired},
		{OwnerID: 3, DeviceLimit: 7, Status: model.StatusExhausted},
		{OwnerID: 2, DeviceLimit: 1, Status: model.StatusActive},
	} {
		c.Name = string(rune('a' + i))
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := openUserCapacity(db); err != nil {
			t.Fatal(err)
		}
	}
	var accs []model.CapacityAccount
	db.Order("admin_id").Find(&accs)
	got := map[uint]model.CapacityAccount{}
	for _, a := range accs {
		got[a.AdminID] = a
	}
	if len(accs) != 2 {
		t.Fatalf("accounts: %+v", accs)
	}
	if a := got[2]; a.Mode != model.CapacityOpen || a.Granted != 100 || a.Used != 1 || a.Handed != 30 {
		t.Errorf("admin: %+v", a)
	}
	if r := got[3]; r.Granted != 30 || r.Used != 6 || r.Handed != 0 {
		t.Errorf("reseller: %+v (an ended customer holds no seat)", r)
	}
	var sums []struct {
		AccountID uint
		Field     string
		N         int64
	}
	db.Raw(`SELECT account_id, field, SUM(delta) AS n FROM capacity_entries GROUP BY account_id, field`).Scan(&sums)
	for _, s := range sums {
		for _, a := range accs {
			if a.ID != s.AccountID {
				continue
			}
			want := map[string]int64{"granted": a.Granted, "used": a.Used, "handed": a.Handed}[s.Field]
			if s.N != want {
				t.Errorf("account %d %s: ledger %d, figure %d", a.ID, s.Field, s.N, want)
			}
		}
	}
}
