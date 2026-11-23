package database

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A list of a hundred thousand ids, past SQLite's limit of parameters, is
// one condition that matches exactly the rows it names, in and not in.
func TestALongListOfIDsIsOneCondition(t *testing.T) {
	longListOfIDs(t, memDB(t))
}

// The same on PostgreSQL, which unpacks the list its own way.
func TestALongListOfIDsIsOneConditionOnPostgres(t *testing.T) {
	dsn := os.Getenv("WUI_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("WUI_TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	db.Exec("DELETE FROM accounts")
	db.Exec("DELETE FROM clients")
	longListOfIDs(t, db)
}

func longListOfIDs(t *testing.T, db *gorm.DB) {
	t.Helper()
	rows := make([]model.Client, 0, 3000)
	for i := 0; i < 3000; i++ {
		rows = append(rows, model.Client{Name: fmt.Sprintf("c%d", i)})
	}
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		t.Fatal(err)
	}
	// Every other id from the first row's, a hundred thousand of them:
	// half the rows, and most of the list naming no row at all.
	first := rows[0].ID
	ids := make([]uint, 0, 100000)
	for i := 0; i < 100000; i++ {
		ids = append(ids, first+uint(2*i+1))
	}
	for _, c := range []struct {
		name string
		cond func() (string, []any)
		want int64
	}{
		{"in", func() (string, []any) { return InIDs(db, "id", ids) }, 1500},
		{"not in", func() (string, []any) { return NotInIDs(db, "id", ids) }, 1500},
		{"in, short", func() (string, []any) { return InIDs(db, "id", []uint{first, first + 1, first + 2}) }, 3},
		{"in, none", func() (string, []any) { return InIDs(db, "id", nil) }, 0},
		{"not in, none", func() (string, []any) { return NotInIDs(db, "id", nil) }, 3000},
	} {
		q, args := c.cond()
		var n int64
		if err := db.Model(&model.Client{}).Where(q, args...).Count(&n).Error; err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n != c.want {
			t.Fatalf("%s: %d rows, want %d", c.name, n, c.want)
		}
	}
}
