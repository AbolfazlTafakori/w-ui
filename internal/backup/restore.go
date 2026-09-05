package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Putting an archive back.
//
// A backup nobody can restore is a file that makes an operator feel safe and is
// not. This is the other half, and it is the half that has to be careful: it
// replaces the database, the keys and every certificate on the server, and the
// worst possible outcome is a restore that fails partway and leaves neither the
// old state nor the new one.
//
// So the order is: read the whole archive and check it before touching
// anything; take a safety copy of what is there now, so a restore is itself
// undoable; unpack to a staging directory; and leave it for the next start to
// put in place, when nothing has the files open. Everything that can fail
// happens while the live data is still untouched.

const (
	// maxRestoreFile bounds a single member. An archive claiming a file larger
	// than this is not one of ours and would fill the disk before failing.
	maxRestoreFile = 2 << 30 // 2 GiB

	// maxRestoreTotal bounds the lot, which is what stops a small archive that
	// expands enormously from being a way to fill the disk.
	maxRestoreTotal = 8 << 30 // 8 GiB
)

// RestoreReport says what a restore did.
type RestoreReport struct {
	// Files restored, relative to the data directory.
	Files []string `json:"files"`

	// SafetyCopy is the archive taken of the state that was replaced, so an
	// operator who restored the wrong one has somewhere to go back to.
	SafetyCopy string `json:"safetyCopy"`
}

// Restore stages an archive to be put in place on the next start.
//
// Not applied here, and that is the whole point. The panel has the database
// open, and SQLite's write-ahead log belongs to the file it was opened
// against: replacing that file underneath a running process means the log is
// checkpointed back over it on the way out, and the restore silently undoes
// itself. That is exactly what happened the first time this was written, and
// it looked like a restore that had worked.
//
// So the archive is unpacked beside the data directory and a marker is left.
// The next start applies it before anything opens the database, which is the
// only moment nothing is holding those files. The caller ends the process; the
// service comes back and the data is there.
func (s *Service) Restore(ctx context.Context, name string, keep *LocalAddresses) (*RestoreReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isArchive(name) || name != filepath.Base(name) {
		return nil, fmt.Errorf("backup: %q is not a backup file", name)
	}
	path := filepath.Join(s.dir, name)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("backup: %q not found", name)
	}

	// Read it through once before anything is staged. A truncated download or a
	// corrupted file fails here, with nothing else having happened.
	if err := verify(path, name); err != nil {
		return nil, err
	}

	// What is being replaced, kept. An operator who restores last month by
	// mistake has otherwise destroyed this month to find that out.
	safety, err := s.createLocked(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: could not save the current state before restoring: %w", err)
	}

	staging := pendingDir(s.dataDir)
	// A restore staged earlier and never applied is stale, and mixing it with
	// this one would restore half of each.
	if err := os.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("backup: clear the staging directory: %w", err)
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, fmt.Errorf("backup: staging directory: %w", err)
	}

	files, err := unpack(path, staging)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if len(files) == 0 {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("backup: %q holds no files", name)
	}

	// Where this machine says it can be reached, carried across the restart so
	// the addresses in the archive — which name the server it was taken on —
	// can be put back afterwards.
	if err := stashLocalAddresses(staging, keep); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	// Written last, and it is what the next start looks for. An unpack that
	// failed or was interrupted leaves a directory with no marker, which is
	// discarded rather than half applied.
	if err := os.WriteFile(filepath.Join(staging, markerFile), []byte(name), 0o600); err != nil {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("backup: mark the restore: %w", err)
	}

	s.log.Warn("a restore is staged and will be applied on the next start",
		"archive", name, "files", len(files), "safetyCopy", safety.Name)

	return &RestoreReport{Files: files, SafetyCopy: safety.Name}, nil
}

// markerFile is what tells the next start that a staged restore is complete.
const markerFile = ".restore-ready"

// PendingDirName is the staging directory, inside the data directory.
//
// Inside rather than beside it, which is the only place that works: the unit
// runs with ProtectSystem=strict and one writable path, so the panel cannot
// create a sibling of its own data directory — the parent belongs to root and
// is read-only to the process. Found by deploying it and looking at the
// permissions, because nothing on a development machine has either.
//
// Being inside means the backup walk has to skip it, the way it already skips
// the archives themselves.
const PendingDirName = ".restore-pending"

func pendingDir(dataDir string) string {
	return filepath.Join(filepath.Clean(dataDir), PendingDirName)
}

// ApplyPending puts a staged restore in place, and is called at startup before
// anything opens the database.
//
// Returns the archive's name when one was applied, so the caller can say so.
// Anything that goes wrong here is reported and the panel starts on the data it
// already had: a failed restore must not also be a panel that will not start.
func ApplyPending(dataDir string, log *slog.Logger) (string, *LocalAddresses, bool) {
	staging := pendingDir(dataDir)

	marker, err := os.ReadFile(filepath.Join(staging, markerFile))
	if err != nil {
		// No marker: either nothing is staged, or an unpack was interrupted and
		// what is there is incomplete. Both are discarded.
		if _, statErr := os.Stat(staging); statErr == nil {
			log.Warn("discarding an incomplete staged restore", "path", staging)
			_ = os.RemoveAll(staging)
		}
		return "", nil, false
	}
	archive := strings.TrimSpace(string(marker))

	// Read before the directory goes, and applied by the caller once the
	// restored database is open.
	keep := readStashedAddresses(staging)

	entries, err := collectFiles(staging)
	if err != nil {
		log.Error("could not read the staged restore; starting on the existing data",
			"error", err)
		return "", nil, false
	}

	applied := 0
	for _, rel := range entries {
		// Neither of these is part of the panel's data; they are how the
		// restore carried itself across the restart.
		if rel == markerFile || rel == localAddressesFile {
			continue
		}
		src := filepath.Join(staging, filepath.FromSlash(rel))
		dst := filepath.Join(dataDir, filepath.FromSlash(rel))

		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			log.Error("could not prepare a restored path", "file", rel, "error", err)
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			if err := copyFile(src, dst); err != nil {
				log.Error("could not restore a file", "file", rel, "error", err)
				continue
			}
		}
		applied++
	}

	// The write-ahead log and shared memory belong to the database that was
	// there before. Left beside the restored one, SQLite replays them over it.
	clearSidecars(dataDir)
	_ = os.RemoveAll(staging)

	log.Warn("restored from a backup", "archive", archive, "files", applied)
	return archive, keep, true
}

// collectFiles lists every regular file under root, relative and slash
// separated.
func collectFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// verify reads the whole archive and checks it is one of ours.
//
// Cheap insurance: the alternative is discovering that the gzip stream ends
// early after half the data directory has already been overwritten.
// label is what the operator called it: an archive on disk has a name they
// chose from a list, and an upload has none yet, so saying ".wui-upload-2650995218
// is damaged" tells them about a file they have never seen.
func verify(path, label string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("backup: open %q: %w", label, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("backup: %q is not a gzip archive", label)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	seenDB := false

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("backup: %q is damaged: %w", label, err)
		}
		if _, err := safeJoin("", hdr.Name); err != nil {
			return err
		}
		if hdr.Size > maxRestoreFile {
			return fmt.Errorf("backup: %q contains a file far too large to be one of ours", label)
		}
		total += hdr.Size
		if total > maxRestoreTotal {
			return fmt.Errorf("backup: %q expands to more than this can restore", label)
		}
		if strings.HasSuffix(hdr.Name, ".db") {
			seenDB = true
		}
		// Read the member out rather than skipping it: the checksum at the end
		// of the gzip stream is only checked once the bytes have gone through.
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return fmt.Errorf("backup: %q is damaged: %w", label, err)
		}
	}

	if !seenDB {
		return fmt.Errorf("backup: %q holds no database, so it is not a W-UI backup", label)
	}
	return nil
}

// unpack extracts into a directory, returning the relative names written.
func unpack(path, into string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backup: open %q: %w", filepath.Base(path), err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("backup: read %q: %w", filepath.Base(path), err)
	}
	defer gz.Close()

	var written []string
	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup: read %q: %w", filepath.Base(path), err)
		}
		if hdr.Typeflag != tar.TypeReg {
			// Directories are made as needed and nothing else — a symlink in an
			// archive is a way to write outside the directory it is unpacked
			// into, and this panel never writes one.
			continue
		}

		dst, err := safeJoin(into, hdr.Name)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, fmt.Errorf("backup: prepare %s: %w", hdr.Name, err)
		}

		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, fmt.Errorf("backup: write %s: %w", hdr.Name, err)
		}
		// Bounded even here: verify has already checked, but an archive is
		// read twice and nothing says the file on disk did not change between.
		if _, err := io.Copy(out, io.LimitReader(tr, maxRestoreFile)); err != nil {
			out.Close()
			return nil, fmt.Errorf("backup: write %s: %w", hdr.Name, err)
		}
		if err := out.Close(); err != nil {
			return nil, fmt.Errorf("backup: write %s: %w", hdr.Name, err)
		}
		written = append(written, filepath.ToSlash(hdr.Name))
	}
	return written, nil
}

// safeJoin resolves a name from an archive against a directory, refusing any
// that would land outside it.
//
// An archive is a file an operator may have been sent, and a tar header can say
// "../../etc/cron.d/anything". Nothing else in the restore path checks this, so
// everything that turns a header into a path comes through here.
func safeJoin(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("backup: the archive contains an entry with no name")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("backup: the archive contains an absolute path (%q); it was not written by this panel", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("backup: the archive tries to write outside the data directory (%q)", name)
	}
	if base == "" {
		return clean, nil
	}
	return filepath.Join(base, clean), nil
}

// clearSidecars removes the write-ahead log and shared-memory files.
//
// They describe the database that was open before. Left beside the one just
// restored, SQLite replays them over it and quietly undoes the restore.
func clearSidecars(dataDir string) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".db-wal") || strings.HasSuffix(n, ".db-shm") {
			_ = os.Remove(filepath.Join(dataDir, n))
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Accept stores an archive somebody uploaded, so it can be restored like any
// other.
//
// This is how a panel moves to another server, and it is the only place the
// panel accepts a file from outside. It is checked before it is kept: an
// archive that is not one of ours is refused here rather than at the point it
// would have been unpacked over the live data.
func (s *Service) Accept(r io.Reader) (Archive, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Archive{}, fmt.Errorf("backup: create %s: %w", s.dir, err)
	}

	tmp, err := os.CreateTemp(s.dir, ".wui-upload-*")
	if err != nil {
		return Archive{}, fmt.Errorf("backup: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return Archive{}, fmt.Errorf("backup: secure upload: %w", err)
	}

	size, err := io.Copy(tmp, io.LimitReader(r, maxRestoreTotal))
	if err != nil {
		tmp.Close()
		return Archive{}, fmt.Errorf("backup: receive upload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Archive{}, fmt.Errorf("backup: receive upload: %w", err)
	}
	if size == 0 {
		return Archive{}, fmt.Errorf("backup: the uploaded file is empty")
	}

	if err := verify(tmpName, "the uploaded file"); err != nil {
		return Archive{}, err
	}

	// Named for when it arrived rather than for whatever the file was called.
	// The name is what every other operation matches on, and a name from
	// outside is a name this panel did not choose.
	now := time.Now().UTC()
	name := filePrefix + now.Format(timeLayout) + "-uploaded" + fileSuffix
	final := filepath.Join(s.dir, name)

	if err := os.Rename(tmpName, final); err != nil {
		return Archive{}, fmt.Errorf("backup: store upload: %w", err)
	}

	s.log.Info("a backup archive was uploaded", "archive", name, "size", size)
	return Archive{Name: name, Size: size, Taken: now}, nil
}
