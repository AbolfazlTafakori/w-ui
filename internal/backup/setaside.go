package backup

import (
	"bufio"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// What a restore whose database comes by a load replaces, kept until the
// load is in.
//
// The files of an archive are put in place before the database is opened;
// its database, where it comes as a dump, is loaded only after. A load that
// is refused -- a dump this panel cannot take -- leaves the database as it
// was, and without this the backup's files beside it: its hops, its replay
// records, from another moment or another server. So each file a restore
// replaces is moved here first, and each it adds is listed, and the start
// either puts them all back (UndoRestoredFiles) or lets them go
// (FinishRestoredFiles). The list is written as it goes, so a restore cut
// off and run again on the next start never sets aside the backup's own
// files as the panel's.

// PreviousDirName holds what a restore replaced, inside the data directory.
const PreviousDirName = ".restore-previous"

// createdList names, one a line, the files a restore added.
const createdList = ".created"

type setAside struct {
	dataDir string
	dir     string
	created map[string]bool
}

func newSetAside(dataDir string) *setAside {
	a := &setAside{dataDir: dataDir, dir: filepath.Join(dataDir, PreviousDirName), created: map[string]bool{}}
	for _, rel := range readCreated(a.dir) {
		a.created[rel] = true
	}
	return a
}

// keep sets aside the file at rel, or lists it as new, before a restored
// one is put there. A file already set aside or listed, by a run of this
// restore that was cut off, is the backup's now and is left.
func (a *setAside) keep(rel string) error {
	if a.created[rel] {
		return nil
	}
	to := filepath.Join(a.dir, filepath.FromSlash(rel))
	if _, err := os.Lstat(to); err == nil {
		return nil
	}
	dst := filepath.Join(a.dataDir, filepath.FromSlash(rel))
	_, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(a.dir, 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(a.dir, createdList), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(rel + "\n"); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		a.created[rel] = true
		return f.Close()
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return os.Rename(dst, to)
}

// close leaves the directory in place, even with nothing in it: its being
// there is what says the start has a load to settle.
func (a *setAside) close() error {
	return os.MkdirAll(a.dir, 0o700)
}

func readCreated(dir string) []string {
	f, err := os.Open(filepath.Join(dir, createdList))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rel := strings.TrimSpace(sc.Text()); rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

// UndoRestoredFiles puts back what a restore replaced and removes what it
// added, after its database load was refused. Reports whether there was
// anything to undo.
func UndoRestoredFiles(dataDir string, log *slog.Logger) bool {
	dir := filepath.Join(dataDir, PreviousDirName)
	if _, err := os.Stat(dir); err != nil {
		return false
	}
	for _, rel := range readCreated(dir) {
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		if !inside(dataDir, path) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Error("could not remove a file the refused restore added", "file", rel, "error", err)
		}
	}
	failed := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == filepath.Join(dir, createdList) {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err == nil {
			err = os.Rename(path, dst)
		}
		if err != nil {
			failed++
			log.Error("could not put back a file the refused restore replaced", "file", filepath.ToSlash(rel), "error", err)
		}
		return nil
	})
	if err != nil || failed > 0 {
		log.Error("the files a refused restore replaced are not all back; what is left is kept", "dir", dir, "error", err)
		return true
	}
	if err := os.RemoveAll(dir); err != nil {
		log.Warn("could not remove the emptied restore directory", "dir", dir, "error", err)
	}
	log.Warn("the restore's files were taken back out: its database was refused, and the panel runs on what it had")
	return true
}

// FinishRestoredFiles lets go of what a restore replaced, once its database
// is in.
func FinishRestoredFiles(dataDir string, log *slog.Logger) {
	dir := filepath.Join(dataDir, PreviousDirName)
	if err := os.RemoveAll(dir); err != nil {
		log.Warn("could not remove what the restore replaced", "dir", dir, "error", err)
	}
}

// usesFile reports whether a staged restore puts its SQLite file, dbFile,
// in place of the panel's. An archive taken on the other engine may still
// carry one -- left behind by a move -- which is not the data: the dump
// says which engine it came from, and that is what decides.
func usesFile(staging, dbFile string, entries []string) bool {
	if dbFile == "" {
		return false
	}
	hasDB, hasExport := false, false
	var parts []string
	for _, rel := range entries {
		switch {
		case rel == dbFile:
			hasDB = true
		case rel == ExportFile:
			hasExport = true
		case isDumpPart(rel):
			parts = append(parts, rel)
		}
	}
	switch {
	case !hasDB:
		return false
	case len(parts) > 0:
		return stagedDumpDriver(staging, parts) == "sqlite"
	case hasExport:
		return dumpDriver(filepath.Join(staging, ExportFile)) == "sqlite"
	}
	return true
}

// inside reports whether path lies in dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
