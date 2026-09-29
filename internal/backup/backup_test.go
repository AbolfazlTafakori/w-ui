package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newService(t *testing.T, keep int) (*Service, string) {
	t.Helper()
	data := t.TempDir()
	for name, body := range map[string]string{
		"wui.db":                   "database",
		"openvpn/tun0/server.key":  "a server private key",
		"openvpn/tun0/credentials": "user:secret",
		"wui.db-wal":               "write ahead log",
		"wui.db-shm":               "shared memory",
	} {
		path := filepath.Join(data, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return New(Options{DataDir: data, Keep: keep, DBFile: "wui.db", Log: quiet()}), data
}

func entries(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("archive is not gzip: %v", err)
	}
	defer gz.Close()

	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("archive is not readable: %v", err)
		}
		body, _ := io.ReadAll(tr)
		out[hdr.Name] = string(body)
	}
	return out
}

func TestArchiveCarriesWhatCannotBeRegenerated(t *testing.T) {
	s, _ := newService(t, 0)
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got := entries(t, filepath.Join(s.Dir(), a.Name))
	// These are the files that, if lost, mean reissuing every customer.
	for _, want := range []string{"wui.db", "openvpn/tun0/server.key", "openvpn/tun0/credentials"} {
		if _, ok := got[want]; !ok {
			t.Errorf("archive is missing %s; it holds %v", want, keys(got))
		}
	}
	if got["openvpn/tun0/server.key"] != "a server private key" {
		t.Error("the key was not copied faithfully")
	}
}

func TestSQLiteSidecarsAreLeftOut(t *testing.T) {
	s, _ := newService(t, 0)
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got := entries(t, filepath.Join(s.Dir(), a.Name))
	// A stale write-ahead log restored beside a database it no longer matches
	// corrupts it. They are meaningless without the moment they belonged to.
	for _, unwanted := range []string{"wui.db-wal", "wui.db-shm"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("archive contains %s, which is unsafe to restore", unwanted)
		}
	}
}

func TestBackupsDoNotContainBackups(t *testing.T) {
	s, _ := newService(t, 0)
	if _, err := s.Create(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.Create(context.Background())
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	// The archive directory sits inside the data directory. Without the skip,
	// each backup would swallow the last and a server left alone would grow
	// them exponentially.
	for name := range entries(t, filepath.Join(s.Dir(), second.Name)) {
		if filepath.Ext(name) == ".gz" {
			t.Errorf("archive contains another archive: %s", name)
		}
	}
}

func TestRetentionKeepsTheNewest(t *testing.T) {
	s, _ := newService(t, 2)
	for i := 0; i < 4; i++ {
		if _, err := s.Create(context.Background()); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		// The name carries a whole-second timestamp, so without this the
		// archives would collide rather than accumulate.
		time.Sleep(1100 * time.Millisecond)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("kept %d archives, want 2", len(list))
	}
	if !list[0].Taken.After(list[1].Taken) {
		t.Error("list is not newest first")
	}
}

func TestOpenRefusesToLeaveItsDirectory(t *testing.T) {
	s, _ := newService(t, 0)
	// The name comes from an HTTP request. Joined blindly it would read any
	// file the panel can reach, which on this server includes every key.
	for _, bad := range []string{
		"../wui.db",
		"wui-backup-../../etc/passwd.tar.gz",
		"/etc/passwd",
		"nonsense.txt",
		"",
	} {
		if _, _, err := s.Open(bad); err == nil {
			t.Errorf("Open(%q) was allowed", bad)
		}
	}
}

func TestDeleteRefusesToLeaveItsDirectory(t *testing.T) {
	s, _ := newService(t, 0)
	for _, bad := range []string{"../wui.db", "/etc/passwd", "notes.txt"} {
		if err := s.Delete(bad); err == nil {
			t.Errorf("Delete(%q) was allowed", bad)
		}
	}
}

func TestAnInterruptedBackupLeavesNothingBehind(t *testing.T) {
	s, _ := newService(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = s.Create(ctx)

	// A half-written file that looked like a good backup would be found and
	// trusted at exactly the wrong moment.
	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, a := range list {
		if a.Size == 0 {
			t.Errorf("an empty archive was left behind: %s", a.Name)
		}
	}
}

func TestListIgnoresFilesThatAreNotOurs(t *testing.T) {
	s, _ := newService(t, 0)
	if _, err := s.Create(context.Background()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list = %+v, want only our own archive", list)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestScheduleIsDerivedFromDiskNotFromATimer(t *testing.T) {
	s, _ := newService(t, 0)
	sched := NewScheduler(s)
	sched.Every = func() time.Duration { return time.Hour }
	sched.Keep = func() int { return 5 }

	sched.tick(context.Background())
	first, err := s.List()
	if err != nil || len(first) != 1 {
		t.Fatalf("first tick did not take a backup: %v %+v", err, first)
	}

	// A panel that restarts often would never reach its own interval if the
	// schedule lived in memory, and would quietly take no backups at all.
	sched.tick(context.Background())
	second, _ := s.List()
	if len(second) != 1 {
		t.Errorf("a second backup was taken inside the interval: %d archives", len(second))
	}
}

func TestSchedulingOffTakesNothing(t *testing.T) {
	s, _ := newService(t, 0)
	sched := NewScheduler(s)
	sched.Every = func() time.Duration { return 0 }

	sched.tick(context.Background())
	list, _ := s.List()
	if len(list) != 0 {
		t.Errorf("backups were taken while scheduling was off: %+v", list)
	}
}

func TestAFileGrowingDuringTheBackupDoesNotBreakIt(t *testing.T) {
	s, data := newService(t, 0)

	// The panel's own log is being appended to while the backup runs. Sizing
	// the entry from a stat and then copying whatever is there writes more
	// bytes than the header declared, and tar rejects the whole archive - so
	// backups would fail precisely on a busy server.
	logPath := filepath.Join(data, "panel.log")
	if err := os.WriteFile(logPath, []byte("start\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		for i := 0; i < 400; i++ {
			_, _ = f.WriteString("another line of log output\n")
		}
	}()

	a, err := s.Create(context.Background())
	<-done
	if err != nil {
		t.Fatalf("backup failed while a file was being written: %v", err)
	}

	// And the archive has to be readable afterwards, not merely produced.
	got := entries(t, filepath.Join(s.Dir(), a.Name))
	if _, ok := got["panel.log"]; !ok {
		t.Errorf("the growing file is missing from the archive: %v", keys(got))
	}
}

func TestTheDatabaseIsArchivedFromItsSnapshot(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "wui.db"), []byte("torn live file"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(Options{
		DataDir: data,
		DBFile:  "wui.db",
		Log:     quiet(),
		Snapshot: func(_ context.Context, dest string) error {
			return os.WriteFile(dest, []byte("a consistent snapshot"), 0o600)
		},
	})

	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A byte copy of a database being written to can catch it mid-write, and a
	// torn database is worth nothing at the moment it is needed.
	got := entries(t, filepath.Join(s.Dir(), a.Name))
	if got["wui.db"] != "a consistent snapshot" {
		t.Errorf("archived %q, want the snapshot", got["wui.db"])
	}
}

// A snapshot that fails leaves the portable dump to carry the database. The live
// file is not archived in its place: in write-ahead-log mode it lacks whatever
// has not been checkpointed, and a restore takes the file over the dump, so the
// backup would quietly restore older data than it seemed to hold.
func TestAFailedSnapshotFallsBackToTheDumpNotTheLiveFile(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "wui.db"), []byte("live file"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(Options{
		DataDir: data,
		DBFile:  "wui.db",
		Log:     quiet(),
		Snapshot: func(context.Context, string) error {
			return io.ErrUnexpectedEOF
		},
		Export: func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, `{"format":1,"driver":"sqlite"}`)
			return err
		},
	})

	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatalf("a failing snapshot with a dump to fall back on stopped the backup: %v", err)
	}
	got := entries(t, filepath.Join(s.Dir(), a.Name))
	if _, has := got["wui.db"]; has {
		t.Error("the live database file was archived in place of the failed snapshot")
	}
	if got[ExportFile] == "" {
		t.Error("the portable dump is missing, so the backup carries no database")
	}
}

// And with neither a snapshot nor a dump there is no database to put in the
// archive. That is not a backup -- a restore refuses it -- so it is not written,
// and the failure is said rather than a file left looking like a good one.
func TestABackupWithNoDatabaseIsNotWritten(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "wui.db"), []byte("live file"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		DataDir: data,
		DBFile:  "wui.db",
		Log:     quiet(),
		Snapshot: func(context.Context, string) error {
			return io.ErrUnexpectedEOF
		},
		Export: func(context.Context, io.Writer) error {
			return io.ErrUnexpectedEOF
		},
	})
	if _, err := s.Create(context.Background()); err == nil {
		t.Fatal("a backup with no copy of the database was reported as taken")
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("%d archives were left behind", len(list))
	}
}

// Two taken within the same second -- a manual one beside a scheduled one, a
// restore's safety copy beside either -- are two archives, not one written
// over the other.
func TestTwoBackupsInOneSecondAreBothKept(t *testing.T) {
	s, _ := newService(t, 0)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		a, err := s.Create(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if seen[a.Name] {
			t.Fatalf("%s was written twice", a.Name)
		}
		seen[a.Name] = true
	}
	list, _ := s.List()
	if len(list) != 3 {
		t.Errorf("%d archives on disk after three backups, want 3", len(list))
	}
}

// A release staged for the update helper is not data. Restored on another
// machine, its request would set that machine's helper off.
func TestABackupSkipsAStagedUpdate(t *testing.T) {
	s, data := newService(t, 0)
	stage := filepath.Join(data, updateStageDir)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wui", "wui.sig", "request", "result"} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name := range entries(t, filepath.Join(s.Dir(), a.Name)) {
		if strings.HasPrefix(name, updateStageDir+"/") {
			t.Errorf("%s was archived", name)
		}
	}
}
