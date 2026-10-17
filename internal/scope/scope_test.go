package scope

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// The rows this package narrows, as the panel stores them.
type client struct {
	ID      uint
	OwnerID uint
	Name    string
}

func (client) TableName() string { return "clients" }

type account struct {
	ID       uint
	ClientID uint
}

func (account) TableName() string { return "accounts" }

// panel is a database with two resellers' customers and the owner's, the
// narrowing registered as the panel registers it.
func panel(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&client{}, &account{}); err != nil {
		t.Fatal(err)
	}
	if err := Register(db); err != nil {
		t.Fatal(err)
	}
	for _, c := range []client{{1, 0, "owner's"}, {2, 7, "reza's"}, {3, 7, "reza's too"}, {4, 9, "sara's"}} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&account{ClientID: c.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

var reza = With(context.Background(), Scope{AdminID: 7, Restricted: true})

func TestAResellerReadsOnlyTheirOwn(t *testing.T) {
	db := panel(t)
	var mine []client
	if err := db.WithContext(reza).Find(&mine).Error; err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Fatalf("reza read %d customers: %+v", len(mine), mine)
	}
	var other client
	if err := db.WithContext(reza).First(&other, 4).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("reza read sara's customer by id: %+v, %v", other, err)
	}
	var n int64
	db.WithContext(reza).Model(&client{}).Count(&n)
	if n != 2 {
		t.Fatalf("counted %d", n)
	}
	// The owner, and anything acting for nobody -- the reconciler, the
	// subscription service -- reads everything.
	var all []client
	db.WithContext(With(context.Background(), Scope{})).Find(&all)
	if len(all) != 4 {
		t.Fatalf("the owner read %d", len(all))
	}
}

func TestAResellerChangesAndDeletesOnlyTheirOwn(t *testing.T) {
	db := panel(t)
	res := db.WithContext(reza).Model(&client{}).Where("id = ?", 4).Update("name", "taken")
	if res.Error != nil || res.RowsAffected != 0 {
		t.Fatalf("reza renamed sara's customer: %d, %v", res.RowsAffected, res.Error)
	}
	res = db.WithContext(reza).Delete(&client{}, 4)
	if res.Error != nil || res.RowsAffected != 0 {
		t.Fatalf("reza deleted sara's customer: %d, %v", res.RowsAffected, res.Error)
	}
	if res := db.WithContext(reza).Model(&client{}).Where("id = ?", 2).Update("name", "renamed"); res.RowsAffected != 1 {
		t.Fatalf("reza could not rename their own: %v", res.Error)
	}
	var sara client
	db.First(&sara, 4)
	if sara.Name != "sara's" {
		t.Fatalf("sara's customer is now %q", sara.Name)
	}
}

// SQL written by hand on the customers' tables is refused for a reseller
// unless it says it narrows itself; for the owner it runs as written.
func TestHandWrittenSQLMustNarrowItself(t *testing.T) {
	db := panel(t)
	var n int64
	for name, run := range map[string]func(*gorm.DB) error{
		"Raw().Scan": func(d *gorm.DB) error { return d.Raw("SELECT COUNT(*) FROM clients").Scan(&n).Error },
		"Raw().Find": func(d *gorm.DB) error { var cs []client; return d.Raw("SELECT * FROM clients").Find(&cs).Error },
		"Exec":       func(d *gorm.DB) error { return d.Exec("UPDATE clients SET name = name").Error },
		"accounts":   func(d *gorm.DB) error { return d.Raw("SELECT COUNT(*) FROM accounts").Scan(&n).Error },
	} {
		if err := run(db.WithContext(reza)); !errors.Is(err, ErrUnscopedSQL) {
			t.Errorf("%s for a reseller: %v", name, err)
		}
		if err := run(db.WithContext(Vouch(reza))); err != nil {
			t.Errorf("%s vouched for: %v", name, err)
		}
		if err := run(db.WithContext(context.Background())); err != nil {
			t.Errorf("%s for the owner: %v", name, err)
		}
	}
	// A table whose name only contains one of theirs is not one of theirs.
	if err := db.WithContext(reza).Raw("SELECT 1 AS client_groups_seen").Scan(&n).Error; err != nil {
		t.Errorf("a column named like a table: %v", err)
	}
	if err := db.WithContext(reza).Raw("SELECT COUNT(*) FROM sqlite_master WHERE name = 'my_clients_x'").Scan(&n).Error; err != nil {
		t.Errorf("a word containing clients: %v", err)
	}
}

func TestTheScopeSaysWhatItAllows(t *testing.T) {
	s := Scope{Restricted: true, AdminID: 7, Hidden: "Reza", Interfaces: map[uint]bool{3: true}}
	if !s.Allows(3) || s.Allows(4) {
		t.Fatal("tunnels")
	}
	if (Scope{}).Allows(4) != true {
		t.Fatal("an unrestricted scope sells everything")
	}
	if !s.Hides("reza") || s.Hides("other") {
		t.Fatal("the hidden group")
	}
	if got := s.Visible([]string{"a", "Reza", "b"}); len(got) != 2 {
		t.Fatalf("visible %v", got)
	}
	if OwnerOf(With(context.Background(), s)) != 7 || OwnerOf(context.Background()) != 0 {
		t.Fatal("owner of")
	}
}

// Narrow puts the same condition on a table Register does not see.
func TestNarrowTakesTheConditionToAnotherTable(t *testing.T) {
	db := panel(t)
	var n int64
	q := db.Model(&account{}).Joins("JOIN clients c ON c.id = accounts.client_id")
	if err := Narrow(reza, q, "c.owner_id").Count(&n).Error; err != nil || n != 2 {
		t.Fatalf("reza's accounts: %d, %v", n, err)
	}
	q = db.Model(&account{}).Joins("JOIN clients c ON c.id = accounts.client_id")
	if err := Narrow(context.Background(), q, "c.owner_id").Count(&n).Error; err != nil || n != 4 {
		t.Fatalf("every account: %d, %v", n, err)
	}
}
