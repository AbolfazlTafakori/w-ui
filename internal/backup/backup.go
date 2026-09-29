// Package backup takes a copy of everything that cannot be regenerated.
//
// What is at stake is specific: the interface private keys and every customer's
// credentials. Lose those and no existing configuration works again — every
// customer has to be reissued, which for a reseller means contacting all of them
// at once. The panel binary and the packages around it are replaceable; this is
// not.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Prefix and suffix of a backup file, used to recognise our own archives when
// applying retention. Anything else in the directory is left alone.
const (
	filePrefix = "wui-backup-"
	fileSuffix = ".tar.gz"
	timeLayout = "20060102-150405"
)

// Archive is one backup on disk.
type Archive struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	Taken time.Time `json:"taken"`
}

// Options configures the service.
type Options struct {
	// DataDir is what gets copied.
	DataDir string
	// Dir is where archives are written. It is deliberately allowed to be
	// inside DataDir; the writer skips its own output.
	Dir string
	// Keep is how many archives to retain. Zero keeps all of them.
	Keep int

	// Snapshot writes a consistent copy of the database to the given path.
	//
	// Copying the live file byte by byte can catch it mid-write, and a torn
	// database is worth nothing at the moment it is needed. SQLite can produce
	// a consistent copy of itself instead; this is how the panel asks for one.
	Snapshot func(ctx context.Context, dest string) error

	// DBFile is the SQLite file's name inside DataDir, or empty when the
	// panel runs on another engine. Any other .db in the directory is not
	// the database -- a file left behind by a move between engines -- and
	// archiving it would put a stale, empty or half-migrated one back on
	// a restore, in place of the dump that carries the real data.
	DBFile string

	// Export writes the database as a portable dump -- every table as JSON,
	// independent of the engine. It goes into every archive beside the
	// SQLite snapshot, so a backup taken on one engine restores into the
	// other, and one taken by an older panel restores into a newer one.
	Export func(ctx context.Context, w io.Writer) error

	Log *slog.Logger
}

// ExportFile is the portable dump's name inside an archive.
const ExportFile = "wui-export.json"

// Service takes and prunes backups.
type Service struct {
	dataDir  string
	dir      string
	keep     int
	snapshot func(context.Context, string) error
	export   func(context.Context, io.Writer) error
	dbFile   string
	log      *slog.Logger

	// Only one backup runs at a time. Two at once would read the database
	// mid-write from two directions and cost more than they are worth.
	mu sync.Mutex
}

func New(o Options) *Service {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	dir := o.Dir
	if dir == "" {
		dir = filepath.Join(o.DataDir, "backups")
	}
	return &Service{
		dataDir:  o.DataDir,
		dir:      dir,
		keep:     o.Keep,
		snapshot: o.Snapshot,
		export:   o.Export,
		dbFile:   o.DBFile,
		log:      log,
	}
}

// Dir is where archives are kept.
func (s *Service) Dir() string { return s.dir }

// Create writes a new archive and applies retention.
func (s *Service) Create(ctx context.Context) (Archive, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(ctx)
}

// createLocked is Create with the lock already held.
//
// Restore needs a backup of what it is about to replace, and it holds the lock
// for the whole operation — so it cannot call Create without deadlocking on
// itself. Splitting it is the alternative to a restore that quietly skips its
// own safety copy.
func (s *Service) createLocked(ctx context.Context) (Archive, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Archive{}, fmt.Errorf("backup: create %s: %w", s.dir, err)
	}

	now := time.Now().UTC()
	name := s.freeName(filePrefix + now.Format(timeLayout))
	final := filepath.Join(s.dir, name)

	// Written to a temporary name and renamed, so a backup interrupted halfway
	// never sits in the directory looking like a good one.
	tmp, err := os.CreateTemp(s.dir, ".wui-backup-*")
	if err != nil {
		return Archive{}, fmt.Errorf("backup: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return Archive{}, fmt.Errorf("backup: secure archive: %w", err)
	}

	// A consistent copy of the database is taken first and archived in place of
	// the live file, which a plain byte copy can catch mid-write -- and which,
	// in write-ahead-log mode, lacks whatever has not been checkpointed yet. A
	// snapshot that fails leaves the portable dump to carry the database; the
	// live file is not archived in its place, because a restore would take it
	// over the dump and quietly lose the latest changes.
	snapshot, cleanup, err := s.snapshotDB(ctx)
	if err != nil {
		s.log.Warn("could not take a consistent copy of the database; the backup carries its portable dump instead",
			"error", err)
	}
	defer cleanup()

	if err := s.writeArchive(ctx, tmp, snapshot); err != nil {
		tmp.Close()
		return Archive{}, err
	}
	if err := tmp.Close(); err != nil {
		return Archive{}, fmt.Errorf("backup: close archive: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return Archive{}, fmt.Errorf("backup: install archive: %w", err)
	}

	info, err := os.Stat(final)
	if err != nil {
		return Archive{}, fmt.Errorf("backup: stat archive: %w", err)
	}

	s.prune()
	s.log.Info("backup written", "file", name, "bytes", info.Size())
	return Archive{Name: name, Size: info.Size(), Taken: now}, nil
}

// snapshotDB asks the database for a consistent copy of itself.
//
// The returned path replaces the live database inside the archive. When no
// snapshot function was supplied — or it fails — the live file is archived
// instead, which is worth more than no backup at all.
func (s *Service) snapshotDB(ctx context.Context) (string, func(), error) {
	noop := func() {}
	if s.snapshot == nil {
		return "", noop, nil
	}

	dir, err := os.MkdirTemp("", "wui-snap-*")
	if err != nil {
		return "", noop, fmt.Errorf("backup: snapshot directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	dest := filepath.Join(dir, "wui.db")
	if err := s.snapshot(ctx, dest); err != nil {
		cleanup()
		return "", noop, err
	}
	return dest, cleanup, nil
}

func (s *Service) writeArchive(ctx context.Context, w io.Writer, dbSnapshot string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	// Whether the archive carries the database in either shape. One without
	// it is not a backup -- a restore refuses it -- and is failed here rather
	// than kept looking like one.
	carriesDB := false

	root := filepath.Clean(s.dataDir)
	backupsDir := filepath.Clean(s.dir)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid-walk is not worth failing the whole
			// backup for; the rest is still worth having.
			s.log.Debug("skipping an unreadable path", "path", path, "error", err)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		clean := filepath.Clean(path)
		// Never archive the archives. On a server left alone for a year that
		// would compound into backups of backups of backups.
		if clean == backupsDir {
			return filepath.SkipDir
		}
		// And the directory scheduled backups used to go to before the
		// installer pointed them outside: an install upgraded from then still
		// has it, full of archives.
		if d.IsDir() && clean == filepath.Join(root, "backups") {
			return filepath.SkipDir
		}
		// A restore staged but not yet applied. Archiving it would put a whole
		// second copy of the data inside the backup, and restoring that backup
		// would stage the older one again.
		if d.IsDir() && d.Name() == PendingDirName {
			return filepath.SkipDir
		}
		// SQLite's sidecar files are meaningless without the moment they
		// belonged to, and restoring a stale one corrupts the database.
		if strings.HasSuffix(clean, ".db-wal") || strings.HasSuffix(clean, ".db-shm") {
			return nil
		}
		// Only the database is the database.
		if strings.HasSuffix(clean, ".db") && d.Name() != s.dbFile {
			return nil
		}
		// And only a consistent copy of it. With a snapshot to take, the live
		// file is never archived: the snapshot replaces it, or, when that
		// failed, the portable dump carries the data.
		if s.dbFile != "" && d.Name() == s.dbFile && !d.IsDir() && dbSnapshot == "" && s.snapshot != nil {
			return nil
		}
		// A release waiting for the root helper, and the helper's answer.
		// Restored, the request would set the helper off on another machine.
		if d.IsDir() && clean == filepath.Join(root, updateStageDir) {
			return filepath.SkipDir
		}
		// A dump waiting to be loaded is not data yet.
		if d.Name() == PendingImportFile {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, clean)
		if err != nil {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("backup: header for %s: %w", rel, err)
		}
		hdr.Name = filepath.ToSlash(rel)

		// The database is archived from its snapshot under its own name, so a
		// restore puts a consistent file back where the live one belongs.
		source := clean
		if dbSnapshot != "" && strings.HasSuffix(clean, ".db") {
			if snapInfo, err := os.Stat(dbSnapshot); err == nil {
				source = dbSnapshot
				hdr.Size = snapInfo.Size()
			}
		}

		// Opened before the header is written: a file that cannot be read is
		// skipped whole, rather than leaving a header with no body behind it,
		// which makes the rest of the archive unreadable.
		f, err := os.Open(source)
		if err != nil {
			s.log.Debug("skipping an unreadable file", "path", source, "error", err)
			return nil
		}
		defer f.Close()

		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("backup: write header for %s: %w", rel, err)
		}
		if s.dbFile != "" && d.Name() == s.dbFile {
			carriesDB = true
		}

		// Exactly the number of bytes the header promised, no more and no
		// less. The size came from a stat taken a moment ago, and several of
		// these files are being written to while the backup runs — the panel's
		// own log most of all. An unbounded copy of a growing file makes tar
		// reject the entry and fails the whole backup, which would mean backups
		// only failing on a busy server.
		written, err := io.Copy(tw, io.LimitReader(f, hdr.Size))
		if err != nil {
			return fmt.Errorf("backup: copy %s: %w", rel, err)
		}
		if written < hdr.Size {
			// It shrank instead. The header is already written, so the entry
			// is padded to match rather than left short and unreadable.
			if _, err := io.CopyN(tw, zeroes{}, hdr.Size-written); err != nil {
				return fmt.Errorf("backup: pad %s: %w", rel, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// The portable dump, last. Its size is not known until it is written, so
	// it goes through a buffer; a database is small next to a year of
	// archives of it.
	if s.export != nil {
		var buf bytes.Buffer
		if err := s.export(ctx, &buf); err != nil {
			s.log.Warn("could not write the portable dump into the backup", "error", err)
		} else {
			hdr := &tar.Header{Name: ExportFile, Mode: 0o600, Size: int64(buf.Len()), ModTime: time.Now(), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("backup: write header for %s: %w", ExportFile, err)
			}
			if _, err := io.Copy(tw, &buf); err != nil {
				return fmt.Errorf("backup: write %s: %w", ExportFile, err)
			}
			carriesDB = true
		}
	}
	if !carriesDB {
		return fmt.Errorf("backup: the database could not be copied, so no backup was written; see the log for why")
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: finish archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("backup: compress archive: %w", err)
	}
	return nil
}

// List returns the archives on disk, newest first.
func (s *Service) List() ([]Archive, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Archive{}, nil
		}
		return nil, fmt.Errorf("backup: read %s: %w", s.dir, err)
	}

	out := make([]Archive, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !isArchive(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Archive{
			Name:  e.Name(),
			Size:  info.Size(),
			Taken: takenAt(e.Name(), info.ModTime()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Taken.After(out[j].Taken) })
	return out, nil
}

// Open returns a reader for one archive.
//
// The name is checked rather than joined blindly: it arrives from an HTTP
// request, and a name containing a path would otherwise read any file the panel
// can reach.
func (s *Service) Open(name string) (*os.File, Archive, error) {
	if !isArchive(name) || name != filepath.Base(name) {
		return nil, Archive{}, fmt.Errorf("backup: %q is not a backup file", name)
	}

	path := filepath.Join(s.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil, Archive{}, fmt.Errorf("backup: %q not found", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Archive{}, fmt.Errorf("backup: open %q: %w", name, err)
	}
	return f, Archive{Name: name, Size: info.Size(), Taken: takenAt(name, info.ModTime())}, nil
}

// Delete removes one archive.
func (s *Service) Delete(name string) error {
	if !isArchive(name) || name != filepath.Base(name) {
		return fmt.Errorf("backup: %q is not a backup file", name)
	}
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
		return fmt.Errorf("backup: delete %q: %w", name, err)
	}
	return nil
}

// prune keeps the newest `keep` archives.
func (s *Service) prune() {
	if s.keep <= 0 {
		return
	}
	list, err := s.List()
	if err != nil || len(list) <= s.keep {
		return
	}
	for _, a := range list[s.keep:] {
		if err := os.Remove(filepath.Join(s.dir, a.Name)); err != nil {
			s.log.Warn("could not remove an old backup", "file", a.Name, "error", err)
			continue
		}
		s.log.Debug("removed an old backup", "file", a.Name)
	}
}

// updateStageDir is where a downloaded update waits for the root helper,
// inside the data directory.
const updateStageDir = "update"

// freeName is base with the archive suffix, or base-2, base-3... when an
// archive by that name exists: two taken within the same second -- a manual
// one beside a scheduled one, a restore's safety copy beside either -- used to
// be written over one another.
func (s *Service) freeName(base string) string {
	name := base + fileSuffix
	for n := 2; ; n++ {
		if _, err := os.Lstat(filepath.Join(s.dir, name)); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s-%d%s", base, n, fileSuffix)
	}
}

// zeroes pads a shortened file so its entry still matches its header.
type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func isArchive(name string) bool {
	return strings.HasPrefix(name, filePrefix) && strings.HasSuffix(name, fileSuffix)
}

// takenAt reads the timestamp out of the filename, falling back to the file's
// own time for an archive copied in from somewhere else.
func takenAt(name string, fallback time.Time) time.Time {
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix)
	if t, err := time.Parse(timeLayout, stamp); err == nil {
		return t.UTC()
	}
	return fallback.UTC()
}
