package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database"
)

// sqliteFile is a real database of this panel, as a backup carries it.
func sqliteFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "made.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.Close()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, path string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// Restoring the oldest archive kept: its own safety copy pushed it past the
// number kept, and retention deleted it before it was unpacked.
func TestRestoringTheOldestArchiveDoesNotDeleteIt(t *testing.T) {
	s, _ := newService(t, 2)
	ctx := context.Background()
	old, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(ctx, old.Name, nil); err != nil {
		t.Fatalf("restoring the oldest of the kept: %v", err)
	}
	if _, ok := read(t, filepath.Join(s.Dir(), old.Name)); !ok {
		t.Fatal("the restored archive was deleted")
	}
}

// A restore whose database comes by a load sets aside what its files
// replace: the load refused, they go back and the ones it added go; the
// load in, the backup's stay.
func TestARefusedLoadTakesTheRestoresFilesBackOut(t *testing.T) {
	for _, refused := range []bool{true, false} {
		data := filepath.Join(t.TempDir(), "data")
		write(t, data, map[string]string{"hops/a.json": "mine", "geoip/keep.dat": "untouched"})
		write(t, pendingDir(data), map[string]string{
			"hops/a.json":  "theirs",
			"stealth/r.db": "theirs too",
			ExportFile:     `{"format":1,"driver":"sqlite"}`,
			markerFile:     "wui-backup-20260101-000000.tar.gz",
		})
		if _, _, ok := ApplyPending(data, "", quiet()); !ok {
			t.Fatal("not applied")
		}
		if got, _ := read(t, filepath.Join(data, "hops/a.json")); got != "theirs" {
			t.Fatalf("before the load: %q", got)
		}
		if refused {
			if !UndoRestoredFiles(data, quiet()) {
				t.Fatal("nothing to undo")
			}
			if got, _ := read(t, filepath.Join(data, "hops/a.json")); got != "mine" {
				t.Errorf("refused: the replaced file is %q", got)
			}
			if _, ok := read(t, filepath.Join(data, "stealth/r.db")); ok {
				t.Error("refused: the added file stayed")
			}
		} else {
			FinishRestoredFiles(data, quiet())
			if got, _ := read(t, filepath.Join(data, "hops/a.json")); got != "theirs" {
				t.Errorf("loaded: the restored file is %q", got)
			}
		}
		if got, _ := read(t, filepath.Join(data, "geoip/keep.dat")); got != "untouched" {
			t.Errorf("a file the archive did not have: %q", got)
		}
		if _, err := os.Stat(filepath.Join(data, PreviousDirName)); !os.IsNotExist(err) {
			t.Error("the set-aside directory stayed")
		}
	}
}

// Run again after being cut off, a restore does not set aside the backup's
// own files as the panel's.
func TestARestoreRunTwiceKeepsThePanelsOwnFiles(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	write(t, data, map[string]string{"hops/a.json": "mine"})
	staged := map[string]string{"hops/a.json": "theirs", "new.txt": "new", ExportFile: `{"format":1}`, markerFile: "x.tar.gz"}
	write(t, pendingDir(data), staged)
	ApplyPending(data, "", quiet())
	write(t, pendingDir(data), staged) // as if the first run never finished
	ApplyPending(data, "", quiet())
	UndoRestoredFiles(data, quiet())
	if got, _ := read(t, filepath.Join(data, "hops/a.json")); got != "mine" {
		t.Fatalf("after two runs and an undo: %q", got)
	}
	if _, ok := read(t, filepath.Join(data, "new.txt")); ok {
		t.Fatal("the added file stayed")
	}
}

// The database's write-ahead log is its latest writes where the database
// itself stays: a load comes over it, or the archive has none. Only a
// database file put in its place takes it away.
func TestTheLogOfADatabaseThatStaysIsKept(t *testing.T) {
	for _, tc := range []struct {
		name   string
		staged map[string]string
		keeps  bool
	}{
		{"a dump", map[string]string{ExportFile: `{"format":1,"driver":"postgres"}`}, true},
		{"no database", map[string]string{"hops/a.json": "x"}, true},
		{"the file", map[string]string{"wui.db": "restored"}, false},
	} {
		data := filepath.Join(t.TempDir(), "data")
		write(t, data, map[string]string{"wui.db": "live", "wui.db-wal": "latest writes"})
		tc.staged[markerFile] = "x.tar.gz"
		write(t, pendingDir(data), tc.staged)
		ApplyPending(data, "wui.db", quiet())
		if _, ok := read(t, filepath.Join(data, "wui.db-wal")); ok != tc.keeps {
			t.Errorf("%s: the log kept = %v, want %v", tc.name, ok, tc.keeps)
		}
	}
}

// A database file that would be put in place is opened and checked at the
// restore: damaged, or from a panel with database changes this one does not
// know, it is refused then, not found by a panel that will not start.
func TestARestoredDatabaseFileIsCheckedFirst(t *testing.T) {
	good := sqliteFile(t)
	path := filepath.Join(t.TempDir(), "newer.db")
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO schema_migrations (id, name, applied_at) VALUES (?, 'from the future', CURRENT_TIMESTAMP)",
		database.LatestMigration()+1).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.Close()
	newer, _ := read(t, path)

	damaged := []byte(good)
	for i := 100; i < len(damaged); i++ {
		damaged[i] = 0xA5
	}
	for _, tc := range []struct {
		name, db, want string
	}{
		{"damaged", string(damaged), "database file"},
		{"newer", newer, "newer panel"},
		{"good", good, ""},
	} {
		s, _ := newService(t, 0)
		name := "wui-backup-20260101-000000.tar.gz"
		if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
			t.Fatal(err)
		}
		buildArchive(t, filepath.Join(s.Dir(), name), map[string]string{"wui.db": tc.db, "hops/a.json": "x"})
		_, err := s.Restore(context.Background(), name, nil)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: got %v", tc.name, err)
		}
		if tc.want != "" {
			if _, err := os.Stat(pendingDir(s.dataDir)); !os.IsNotExist(err) {
				t.Errorf("%s: a refused restore stayed staged", tc.name)
			}
		}
	}
}

// An archive uploaded from another panel does not put this panel's own
// next backup off.
func TestAnUploadDoesNotPutOffTheScheduledBackup(t *testing.T) {
	s, _ := newService(t, 0)
	src, _ := newService(t, 0)
	a, err := src.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(src.Dir(), a.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := s.Accept(f); err != nil {
		t.Fatal(err)
	}
	sched := NewScheduler(s)
	sched.Every = func() time.Duration { return time.Hour }
	sched.tick(context.Background())
	list, _ := s.List()
	if len(list) != 2 {
		t.Fatalf("%d archives: the upload put the panel's own backup off", len(list))
	}
}
