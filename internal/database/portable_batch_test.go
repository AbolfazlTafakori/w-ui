package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/config"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// fillTwoColumnTables puts n rows in two tables keyed on two columns.
func fillTwoColumnTables(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	groups := make([]model.ClientGroup, 0, n)
	seen := make([]model.AccountEndpoint, 0, n)
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		groups = append(groups, model.ClientGroup{ClientID: uint(i/3 + 1), Name: fmt.Sprintf("g%d", i)})
		seen = append(seen, model.AccountEndpoint{AccountID: uint(i/2 + 1), Addr: fmt.Sprintf("10.0.%d.%d", i/250, i%250), FirstSeen: now, LastSeen: now, Hits: 1})
	}
	if err := db.CreateInBatches(groups, 500).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(seen, 500).Error; err != nil {
		t.Fatal(err)
	}
}

// The streaming dump the backups write reads each table as one query, so
// a table keyed on two columns is dumped and loaded whole however long.
func TestTheBackupDumpTakesEveryRowOfATwoColumnKeyedTable(t *testing.T) {
	ctx := context.Background()
	db := memDB(t)
	const n = 2500
	fillTwoColumnTables(t, db, n)
	var buf bytes.Buffer
	sum, err := WriteDump(ctx, db, config.DriverSQLite, "test", &buf)
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	for _, tb := range sum.Tables {
		if (tb.Name == "client_groups" || tb.Name == "account_endpoints") && tb.Rows != n {
			t.Fatalf("%s: dumped %d rows, want %d", tb.Name, tb.Rows, n)
		}
	}
	dst := memDB(t)
	if _, err := LoadDump(ctx, dst, config.DriverSQLite, bytes.NewReader(buf.Bytes()), nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	var g, e int64
	dst.Model(&model.ClientGroup{}).Count(&g)
	dst.Model(&model.AccountEndpoint{}).Count(&e)
	if g != n || e != n {
		t.Fatalf("loaded %d groups and %d endpoints, want %d each", g, e, n)
	}
}

// A table keyed on two columns is exported whole however long it is. The
// batches were taken by a single primary key, so past the first thousand
// rows of client_groups or account_endpoints the backup failed with
// "primary key required" and no backup was written at all.
func TestExportTakesEveryRowOfATwoColumnKeyedTable(t *testing.T) {
	ctx := context.Background()
	db := memDB(t)
	const n = 2500
	fillTwoColumnTables(t, db, n)

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
