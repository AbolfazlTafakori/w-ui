package keyfile

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// A key is made once and read back the same; its file is the owner's alone.
func TestAKeyIsMadeOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	a, err := Ensure(dir, "session.key", 32)
	if err != nil || len(a) != 32 {
		t.Fatalf("made %d bytes: %v", len(a), err)
	}
	b, err := Ensure(dir, "session.key", 32)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("read back another key: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, Dir, "session.key"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v: %v", info.Mode(), err)
		}
	}
}

// Starts racing each other end with one key.
func TestStartsRacingEndWithOneKey(t *testing.T) {
	dir := t.TempDir()
	keys := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, err := Ensure(dir, "session.key", 32)
			if err != nil {
				t.Error(err)
			}
			keys[i] = k
		}(i)
	}
	wg.Wait()
	for _, k := range keys[1:] {
		if !bytes.Equal(k, keys[0]) {
			t.Fatal("two keys for one name")
		}
	}
}

// A file that is not a key this made is refused, not used.
func TestAForeignFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Dir, "session.key"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, "session.key", 32); err == nil {
		t.Fatal("a five-byte key was used")
	}
}

// A secret written is read back; removed, it is gone; removing it twice is
// no error.
func TestASecretIsWrittenReadAndRemoved(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Read(dir, "backup.password"); ok || err != nil {
		t.Fatalf("there before it was written: %v", err)
	}
	if err := Write(dir, "backup.password", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "backup.password", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if b, ok, err := Read(dir, "backup.password"); !ok || err != nil || string(b) != "second" {
		t.Fatalf("read %q %v %v", b, ok, err)
	}
	for i := 0; i < 2; i++ {
		if err := Remove(dir, "backup.password"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := Read(dir, "backup.password"); ok {
		t.Fatal("still there")
	}
}
