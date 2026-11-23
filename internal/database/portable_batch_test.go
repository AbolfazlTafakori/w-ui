package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// A table keyed on two columns is exported whole however long it is. The
// batches were taken by a single primary key, so past the first thousand
// rows of client_groups or account_endpoints the backup failed with
// "primary key required" and no backup was written at all.
func TestExportTakesEveryRowOfATwoColumnKeyedTable(t *testing.T) {
	ctx := context.Background()
	db := memDB(t)
	const n = 2500
	groups := make([]model.ClientGroup, 0, n)
	seen := make([]model.AccountEndpoint, 0, n)
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		groups = append(groups, model.ClientGroup{ClientID: uint(i/3 + 1), Name: fmt.Sprintf("g%d", i%3+i/3*3)})
		seen = append(seen, model.AccountEndpoint{AccountID: uint(i/2 + 1), Addr: fmt.Sprintf("10.0.%d.%d", i/250, i%250), FirstSeen: now, LastSeen: now, Hits: 1})
	}
	if err := db.CreateInBatches(groups, 500).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(seen, 500).Error; err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Export(ctx, db, config.DriverSQLite, "test", &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	var dump Dump
	if err := json.Unmarshal(buf.Bytes(), &dump); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"client_groups", "account_endpoints"} {
		if got := len(dump.Tables[table]); got != n {
			t.Fatalf("%s: exported %d rows, want %d", table, got, n)
		}
	}

	// And they come back, every one, into a fresh database.
	dst := memDB(t)
	if err := Import(ctx, dst, config.DriverSQLite, bytes.NewReader(buf.Bytes()), nil); err != nil {
		t.Fatalf("import: %v", err)
	}
	var g, e int64
	dst.Model(&model.ClientGroup{}).Count(&g)
	dst.Model(&model.AccountEndpoint{}).Count(&e)
	if g != n || e != n {
		t.Fatalf("imported %d groups and %d endpoints, want %d each", g, e, n)
	}
}
