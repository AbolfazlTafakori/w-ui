package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeArchive puts a hand-made archive in the service's directory, the way
// one uploaded from anywhere arrives.
func writeCrafted(t *testing.T, dir, name string, entries []tar.Header, bodies map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		body := bodies[h.Name]
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		h.Mode = 0o600
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
}

// An archive is a file that may have come from anywhere. What the processes
// running as root read from the data directory -- the firewall's ports, the
// update helper's stage, the PPP agent's tunnel descriptions -- and the
// panel's own keys are never put back from one, so restoring a crafted
// archive cannot open a port, stage a release or start a tunnel as root.
func TestARestoreNeverPutsBackWhatRootReads(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "wui.db"), []byte("the current database"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{DataDir: data, DBFile: "wui.db", Log: quiet()})
	planted := map[string]string{
		"wui.db":                       "the database",
		"openvpn/ovpn1/server.conf":    "kept: this is data",
		"firewall/ports.json":          `{"tcp":[1,22,5432,65535]}`,
		"update/request":               "install",
		"update/wui":                   "a binary",
		"l2tp/state.json":              `{"tunnels":[]}`,
		"pptp/state.json":              `{}`,
		"ikev2/state.json":             `{}`,
		"openconnect/state.json":       `{}`,
		"openconnect/kick":             "1",
		"keys/session.key":             "someone else's key",
		"keys/../keys/backup.password": "x",
	}
	var hdrs []tar.Header
	for name := range planted {
		hdrs = append(hdrs, tar.Header{Name: name})
	}
	hdrs = append(hdrs, tar.Header{Name: "evil-link", Typeflag: tar.TypeSymlink, Linkname: "/etc"})
	name := "wui-backup-20260101-000000.tar.gz"
	writeCrafted(t, s.Dir(), name, hdrs, planted)

	if _, err := s.Restore(context.Background(), name, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, ok := ApplyPending(data, "wui.db", quiet()); !ok {
		t.Fatal("the restore was not applied")
	}
	for _, rel := range []string{"firewall/ports.json", "update/request", "update/wui", "l2tp/state.json",
		"pptp/state.json", "ikev2/state.json", "openconnect/state.json", "openconnect/kick",
		"keys/session.key", "keys/backup.password", "evil-link"} {
		if _, err := os.Lstat(filepath.Join(data, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was put back from the archive", rel)
		}
	}
	if b, err := os.ReadFile(filepath.Join(data, "openvpn", "ovpn1", "server.conf")); err != nil || string(b) != "kept: this is data" {
		t.Errorf("the archive's data was not restored: %q %v", b, err)
	}
}

// A name that climbs out of the data directory refuses the whole archive,
// before anything is staged.
func TestAnArchiveThatClimbsOutIsRefusedWhole(t *testing.T) {
	data := t.TempDir()
	s := New(Options{DataDir: data, Log: quiet()})
	for i, evil := range []string{"../escape.txt", "/etc/cron.d/evil", "openvpn/../../escape.txt", `..\escape.txt`} {
		name := "wui-backup-2026010" + string(rune('1'+i)) + "-000000.tar.gz"
		writeCrafted(t, s.Dir(), name, []tar.Header{{Name: "wui.db"}, {Name: evil}}, map[string]string{"wui.db": "db", evil: "x"})
		if _, err := s.Restore(context.Background(), name, nil); err == nil {
			t.Errorf("%q: the archive was accepted", evil)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(data), "escape.txt")); err == nil {
			t.Fatalf("%q wrote outside the data directory", evil)
		}
		if _, err := os.Stat(pendingDir(data)); err == nil {
			entries, _ := os.ReadDir(pendingDir(data))
			if len(entries) > 0 {
				t.Errorf("%q: something was staged", evil)
			}
		}
	}
}

// A dump that failed to load is set aside in the data directory for the
// operator; it is the whole database in the clear and never goes into a
// backup.
func TestASetAsideDumpIsNotBackedUp(t *testing.T) {
	data := t.TempDir()
	for _, name := range []string{"wui.db", PendingDumpFile + ".failed", PendingImportFile + ".failed", PendingDumpFile, PendingDumpFile + ".tmp"} {
		if err := os.WriteFile(filepath.Join(data, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := New(Options{DataDir: data, DBFile: "wui.db", Log: quiet()})
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name := range entries(t, filepath.Join(s.Dir(), a.Name)) {
		if name != "wui.db" {
			t.Errorf("%s went into the backup", name)
		}
	}
}
