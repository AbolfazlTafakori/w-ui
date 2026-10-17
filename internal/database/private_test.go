package database

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/abolfazl/w-ui/internal/config"
)

// A database left readable by everyone, as earlier releases left it, is the
// panel's alone once it is opened: its keys and second factors with it.
func TestTheDatabaseIsMadePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a Unix matter")
	}
	path := filepath.Join(t.TempDir(), "wui.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := Open(config.Config{DBDriver: config.DriverSQLite, DBSource: path}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v", filepath.Base(p), info.Mode().Perm())
		}
	}
}
