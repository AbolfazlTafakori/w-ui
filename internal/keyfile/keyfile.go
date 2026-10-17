// Package keyfile keeps the panel's own secrets in files of their own.
//
// A secret the panel makes for itself -- the key its sessions are signed
// with -- does not belong in the database. The database goes into every
// backup, and a backup goes where its operator sends it: to Telegram, to
// another server, to a laptop. Whoever held one held the key to the panel it
// came from, and could sign themselves in as its owner. A key in a file
// under keys/ in the data directory is never archived (backup skips the
// directory), so a backup carries nothing that opens the panel.
package keyfile

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Dir is the directory, inside the data directory, the keys are kept in.
const Dir = "keys"

// Ensure is the key at name in dir/keys: read when it is there, made from
// size random bytes when it is not.
//
// Made under a temporary name and linked into place, so two starts racing
// end with one key, and an interrupted write never leaves half of one. A key
// of any other size is refused rather than used: it is not one this wrote.
func Ensure(dataDir, name string, size int) ([]byte, error) {
	dir := filepath.Join(dataDir, Dir)
	path := filepath.Join(dir, name)
	if key, err := os.ReadFile(path); err == nil {
		if len(key) != size {
			return nil, fmt.Errorf("keyfile: %s is %d bytes, not the %d of a key this panel made", path, len(key), size)
		}
		return key, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("keyfile: read %s: %w", path, err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("keyfile: create %s: %w", dir, err)
	}
	key := make([]byte, size)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("keyfile: make %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return nil, fmt.Errorf("keyfile: write %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("keyfile: secure %s: %w", name, err)
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("keyfile: write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("keyfile: write %s: %w", name, err)
	}
	// A link fails when the name is taken: another start got there first,
	// and its key is the key.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Ensure(dataDir, name, size)
		}
		return nil, fmt.Errorf("keyfile: install %s: %w", name, err)
	}
	return key, nil
}

// Read is the secret at name in dir/keys, and whether there is one.
func Read(dataDir, name string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("keyfile: read %s: %w", name, err)
	}
	return b, true, nil
}

// Write keeps secret at name in dir/keys, replacing what was there, written
// to a temporary name and renamed so no reader sees half of it.
func Write(dataDir, name string, secret []byte) error {
	dir := filepath.Join(dataDir, Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("keyfile: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("keyfile: write %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("keyfile: secure %s: %w", name, err)
	}
	if _, err := tmp.Write(secret); err != nil {
		tmp.Close()
		return fmt.Errorf("keyfile: write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keyfile: write %s: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("keyfile: install %s: %w", name, err)
	}
	return nil
}

// Remove forgets the secret at name; one that is not there is no error.
func Remove(dataDir, name string) error {
	err := os.Remove(filepath.Join(dataDir, Dir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("keyfile: remove %s: %w", name, err)
	}
	return nil
}
