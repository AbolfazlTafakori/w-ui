package backup

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The old database's write-ahead files are taken out of SQLite's way before
// the restored database is opened; one that cannot be is said, by name, as
// an error -- left silently, SQLite may replay it over the restored data.
func TestTheOldWriteAheadLogIsClearedOrSaid(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"wui.db-wal", "wui.db-shm", "wui.db"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	clearSidecars(dir, log)
	for _, n := range []string{"wui.db-wal", "wui.db-shm"} {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", n)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "wui.db")); err != nil {
		t.Error("the database itself was touched")
	}

	if runtime.GOOS != "windows" {
		return // holding a file open keeps it in place on Windows alone
	}
	stuck := filepath.Join(dir, "wui.db-wal")
	if err := os.WriteFile(stuck, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(stuck)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf.Reset()
	clearSidecars(dir, log)
	if _, err := os.Stat(stuck); err != nil {
		t.Skip("this system let a file held open be removed; the path for one that stays is not exercised here")
	}
	if !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "wui.db-wal") {
		t.Fatalf("a write-ahead file that stayed was not said as an error: %s", buf.String())
	}
}
