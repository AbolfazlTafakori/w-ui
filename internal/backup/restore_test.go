package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildArchive writes a gzip tar of the given name/content pairs.
func buildArchive(t *testing.T, path string, files map[string]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// An archive is a file somebody may have been sent, and a tar header can name
// any path it likes. Nothing else in the restore path checks this, so an entry
// that climbs out of the data directory has to be refused before a single byte
// is written — it is the difference between a restore and a way to write into
// /etc on the server.
func TestAnArchiveCannotWriteOutsideTheDataDirectory(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{
		"../../../../etc/cron.d/pwned",
		"../outside.txt",
		"/etc/passwd",
		`..\..\windows\system32\evil`,
	} {
		path := filepath.Join(dir, "evil.tar.gz")
		buildArchive(t, path, map[string]string{name: "x", "wui.db": "SQLite"})

		if err := verify(path, "evil.tar.gz"); err == nil {
			t.Errorf("an archive containing %q was accepted", name)
		}
	}
}

// A file that is not a backup of this panel must be refused before it replaces
// anything. Restoring somebody's holiday photos over the data directory is not
// a recoverable mistake.
func TestSomethingThatIsNotOurBackupIsRefused(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "notours.tar.gz")
	buildArchive(t, path, map[string]string{"readme.txt": "hello"})
	err := verify(path, "notours.tar.gz")
	if err == nil {
		t.Fatal("an archive with no database was accepted")
	}
	if !strings.Contains(err.Error(), "no database") {
		t.Errorf("the reason given was %q; it should say what is missing", err)
	}

	// Not a gzip file at all.
	plain := filepath.Join(dir, "plain.tar.gz")
	if err := os.WriteFile(plain, []byte("this is just text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(plain, "plain.tar.gz"); err == nil {
		t.Error("a file that is not an archive was accepted")
	}
}

// A half-downloaded archive is the ordinary way this goes wrong, and it has to
// fail while the live data is still untouched rather than partway through
// overwriting it.
func TestATruncatedArchiveIsRefused(t *testing.T) {
	dir := t.TempDir()

	full := filepath.Join(dir, "full.tar.gz")
	buildArchive(t, full, map[string]string{"wui.db": strings.Repeat("SQLite format 3", 500)})

	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(dir, "cut.tar.gz")
	if err := os.WriteFile(cut, raw[:len(raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verify(cut, "cut.tar.gz"); err == nil {
		t.Error("a truncated archive was accepted")
	}
}

// The ordinary case still has to pass, or the checks above have just turned
// restore off.
func TestARealBackupPassesTheChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "good.tar.gz")
	buildArchive(t, path, map[string]string{
		"wui.db":                      "SQLite format 3",
		"openvpn/ovpn443/server.conf": "dev ovpn443",
	})

	if err := verify(path, "good.tar.gz"); err != nil {
		t.Errorf("a real backup was refused: %v", err)
	}
}

// ── what the next start does with a staged restore ──────────────────────────

// The marker is written last. A staging directory without one is an unpack
// that was interrupted, and applying half of it would leave neither the old
// data nor the new.
func TestAnIncompleteStagedRestoreIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "wui.db"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}

	staging := pendingDir(data)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "wui.db"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No marker.

	if _, ok := ApplyPending(data, quiet()); ok {
		t.Error("an unmarked staging directory was applied")
	}
	live, err := os.ReadFile(filepath.Join(data, "wui.db"))
	if err != nil || string(live) != "live" {
		t.Errorf("the live database was replaced from an incomplete restore: %q", live)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Error("the incomplete staging directory was left behind to be tried again")
	}
}

// And a complete one is applied, sidecars and all.
func TestAStagedRestoreIsAppliedAndCleanedUp(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"wui.db":     "live",
		"wui.db-wal": "a write-ahead log for the database being replaced",
		"wui.db-shm": "shared memory for the same",
	} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	staging := pendingDir(data)
	if err := os.MkdirAll(filepath.Join(staging, "openvpn"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"wui.db":              "restored",
		"openvpn/server.conf": "dev ovpn443",
		markerFile:            "wui-backup-20260101-000000.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(staging, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	archive, ok := ApplyPending(data, quiet())
	if !ok {
		t.Fatal("a complete staged restore was not applied")
	}
	if archive != "wui-backup-20260101-000000.tar.gz" {
		t.Errorf("the archive applied was reported as %q", archive)
	}

	db, err := os.ReadFile(filepath.Join(data, "wui.db"))
	if err != nil || string(db) != "restored" {
		t.Errorf("the database was not replaced: %q, %v", db, err)
	}
	conf, err := os.ReadFile(filepath.Join(data, "openvpn", "server.conf"))
	if err != nil || string(conf) != "dev ovpn443" {
		t.Errorf("a file in a subdirectory was not restored: %q, %v", conf, err)
	}

	// The sidecars belong to the database that was there before. Left behind,
	// SQLite replays them over the restored one and quietly undoes all of this.
	for _, name := range []string{"wui.db-wal", "wui.db-shm"} {
		if _, err := os.Stat(filepath.Join(data, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the restore and will be replayed over it", name)
		}
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Error("the staging directory was left behind and would be applied again next start")
	}
	// The marker must not become a file in the data directory.
	if _, err := os.Stat(filepath.Join(data, markerFile)); !os.IsNotExist(err) {
		t.Error("the marker was copied into the data directory")
	}
}

// Nothing staged, nothing done — every ordinary start goes through this.
func TestAnOrdinaryStartDoesNothing(t *testing.T) {
	data := t.TempDir()
	if _, ok := ApplyPending(data, quiet()); ok {
		t.Error("a start with nothing staged reported a restore")
	}
}

// unpack must write only where it was told, whatever the archive says.
func TestUnpackStaysInsideItsDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.tar.gz")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "x"
	_ = tw.WriteHeader(&tar.Header{
		Name: "../escaped.txt", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg,
	})
	_, _ = io.WriteString(tw, body)
	_ = tw.Close()
	_ = gz.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	into := filepath.Join(dir, "staging")
	if err := os.MkdirAll(into, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := unpack(path, into); err == nil {
		t.Error("unpack accepted an entry that climbs out of its directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); !os.IsNotExist(err) {
		t.Error("unpack wrote a file outside the directory it was given")
	}
}
