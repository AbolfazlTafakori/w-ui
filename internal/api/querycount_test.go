package api

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// countQueries counts every statement the database runs from now on.
func countQueries(t *testing.T, db *gorm.DB) *atomic.Int64 {
	t.Helper()
	n := &atomic.Int64{}
	add := func(*gorm.DB) { n.Add(1) }
	cb := db.Callback()
	for _, err := range []error{
		cb.Query().Before("gorm:query").Register("test:count_query", add),
		cb.Row().Before("gorm:row").Register("test:count_row", add),
		cb.Raw().Before("gorm:raw").Register("test:count_raw", add),
		cb.Create().Before("gorm:create").Register("test:count_create", add),
		cb.Update().Before("gorm:update").Register("test:count_update", add),
		cb.Delete().Before("gorm:delete").Register("test:count_delete", add),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return n
}

// The main pages ask the database a fixed number of things, however many
// rows they show: no page reads one row per row it lists. Each is asked
// with few customers and with many, and the counts must be the same.
func TestTheMainPagesAskAFixedNumberOfQueries(t *testing.T) {
	mux, sess, db := serversPanel(t)
	n := countQueries(t, db)
	pages := []struct{ who, path string }{
		{"owner", "/api/clients?perPage=100"},
		{"ad", "/api/clients?perPage=100"},
		{"owner", "/api/overview"},
		{"owner", "/api/admins"},
		{"ad", "/api/admins"},
		{"ra", "/api/operator/servers"},
		{"owner", "/api/problems"},
		{"ad", "/api/operator/ledger"},
	}
	measure := func() map[string]int64 {
		out := map[string]int64{}
		for _, p := range pages {
			n.Store(0)
			if code, body := send(t, mux, sess[p.who], http.MethodGet, p.path, ""); code != http.StatusOK {
				t.Fatalf("%s %s: %d %s", p.who, p.path, code, body)
			}
			out[p.who+" "+p.path] = n.Load()
		}
		return out
	}
	few := measure()
	// Forty more customers, of every operator, each with a device.
	ids := map[string]uint{"owner": 0, "ad": idOf(t, db, "ad"), "ra": idOf(t, db, "ra"), "rc": idOf(t, db, "rc")}
	k := 0
	for who, owner := range ids {
		for i := 0; i < 10; i++ {
			k++
			c := model.Client{Name: fmt.Sprintf("more%s%d", who, i), Status: model.StatusActive, OwnerID: owner}
			if who == "rc" {
				c.AdminID = ids["ad"]
			}
			if err := db.Create(&c).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: 1, NodeID: 1, DeviceName: "d",
				IP: fmt.Sprintf("10.1.0.%d", 100+k), Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	many := measure()
	for _, p := range pages {
		key := p.who + " " + p.path
		t.Logf("%-40s %d queries", key, many[key])
		if many[key] != few[key] {
			t.Errorf("%s: %d queries with few rows, %d with many: it reads per row", key, few[key], many[key])
		}
	}
}
