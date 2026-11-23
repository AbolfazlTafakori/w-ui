package database

import (
	"fmt"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A list of a hundred thousand ids, past SQLite's limit of parameters, is
// one condition that matches exactly the rows it names, in and not in.
func TestALongListOfIDsIsOneCondition(t *testing.T) {
	db := memDB(t)
	rows := make([]model.Client, 0, 3000)
	for i := 0; i < 3000; i++ {
		rows = append(rows, model.Client{Name: fmt.Sprintf("c%d", i)})
	}
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		t.Fatal(err)
	}
	// Every even id up to 200,000: most name no row.
	ids := make([]uint, 0, 100000)
	for i := 2; i <= 200000; i += 2 {
		ids = append(ids, uint(i))
	}
	for _, c := range []struct {
		name string
		cond func() (string, []any)
		want int64
	}{
		{"in", func() (string, []any) { return InIDs(db, "id", ids) }, 1500},
		{"not in", func() (string, []any) { return NotInIDs(db, "id", ids) }, 1500},
		{"in, short", func() (string, []any) { return InIDs(db, "id", []uint{1, 2, 3}) }, 3},
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
