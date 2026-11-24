package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/abolfazl/w-ui/internal/backup"
	"github.com/abolfazl/w-ui/internal/config"
)

// A restore whose database is refused at the start leaves the panel as it
// was: its own data, and its own files -- not the backup's files beside it.
func TestARefusedRestoreLeavesThePanelsOwnFiles(t *testing.T) {
	data := t.TempDir()
	cfg := config.Config{DBDriver: config.DriverSQLite, DataDir: data, DBSource: filepath.Join(data, "wui.db")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := openPanelDatabase(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}

	put := func(root, rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(data, "hops/a.json", "mine")
	staging := filepath.Join(data, backup.PendingDirName)
	put(staging, "hops/a.json", "theirs")
	put(staging, "stealth/r.json", "theirs too")
	// A copy from a newer panel, which this one refuses to load.
	put(staging, backup.ExportFile, `{"format":99,"version":"9.9.9","driver":"postgres","tables":{}}`)
	put(staging, ".restore-ready", "wui-backup-20260101-000000.tar.gz")

	db, err = openPanelDatabase(cfg, log)
	if err != nil {
		t.Fatalf("a refused restore stopped the panel: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
	if b, _ := os.ReadFile(filepath.Join(data, "hops", "a.json")); string(b) != "mine" {
		t.Errorf("the panel's file is %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, "stealth", "r.json")); !os.IsNotExist(err) {
		t.Error("the backup's added file stayed")
	}
	if _, err := os.Stat(filepath.Join(data, backup.PreviousDirName)); !os.IsNotExist(err) {
		t.Error("the set-aside directory stayed")
	}
	if _, err := os.Stat(filepath.Join(data, backup.PendingImportFile+".failed")); err != nil {
		t.Error("the refused copy was not kept aside to look at")
	}
}
