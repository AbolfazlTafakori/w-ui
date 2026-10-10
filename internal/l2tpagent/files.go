package l2tpagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// maxState bounds what is read from the panel: sixty-five thousand accounts
// at a couple of hundred bytes each, with room to spare.
const maxState = 32 << 20

// rootFiles is the panel's L2TP directory, reached only through an os.Root.
//
// The directory is the panel user's, so it can put anything in it -- a link
// to /etc/shadow named state.json, a link named report.json pointing at a
// file root should never write. An os.Root opened on the data directory, and
// from it on the L2TP directory, refuses every name that leads out of it, so
// none of that reaches anything else: root reads, writes and deletes inside
// the panel's own directory and nowhere else.
type rootFiles struct {
	root *os.Root
}

// OpenFiles opens a tunnel's directory under dataDir: l2tp, or pptp.
// dataDir's parent belongs to root, so the data directory itself cannot be
// swapped; the tunnel's directory inside it is opened through the data
// directory's root, so it cannot be a link to somewhere else either.
func OpenFiles(dataDir, protocol string) (Files, func(), error) {
	data, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the panel's data directory: %w", err)
	}
	// Never created here: a directory root made would be one the panel's
	// user could not write its state into. No directory is no tunnel.
	dir := path.Base(l2tpconf.PathsFor("", protocol).Dir)
	root, err := data.OpenRoot(dir)
	data.Close()
	if errors.Is(err, fs.ErrNotExist) {
		// Looked for again on every pass: the agent runs for whichever
		// tunnel exists, and the panel may make the other's directory while
		// it does.
		w := &waitingFiles{dataDir: dataDir, protocol: protocol}
		return w, w.close, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("opening the %s directory: %w", protocol, err)
	}
	return &rootFiles{root: root}, func() { root.Close() }, nil
}

// waitingFiles is a tunnel directory that did not exist when the agent
// started: no tunnel until it does, and then the directory itself.
type waitingFiles struct {
	dataDir, protocol string
	open              Files
	done              func()
}

func (w *waitingFiles) files() Files {
	if w.open == nil {
		if f, done, err := OpenFiles(w.dataDir, w.protocol); err == nil {
			if _, still := f.(*waitingFiles); !still {
				w.open, w.done = f, done
			} else {
				done()
			}
		}
	}
	if w.open == nil {
		return noFiles{}
	}
	return w.open
}

func (w *waitingFiles) close() {
	if w.done != nil {
		w.done()
	}
}

func (w *waitingFiles) ReadState() ([]byte, bool, error)    { return w.files().ReadState() }
func (w *waitingFiles) WriteReport(r l2tpconf.Report) error { return w.files().WriteReport(r) }
func (w *waitingFiles) RemoveReport() error                 { return w.files().RemoveReport() }
func (w *waitingFiles) TakeKicks() ([]uint, error)          { return w.files().TakeKicks() }

// noFiles is a tunnel with no directory: no tunnel, nothing to report.
type noFiles struct{}

func (noFiles) ReadState() ([]byte, bool, error)  { return nil, false, nil }
func (noFiles) WriteReport(l2tpconf.Report) error { return nil }
func (noFiles) RemoveReport() error               { return nil }
func (noFiles) TakeKicks() ([]uint, error)        { return nil, nil }

const (
	stateName  = "state.json"
	reportName = "report.json"
	kickName   = "kick"
)

func (f *rootFiles) ReadState() ([]byte, bool, error) {
	info, err := f.root.Lstat(stateName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	// A regular file and nothing else: not a link, not a pipe that would
	// hold the read open for good.
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", stateName)
	}
	if info.Size() > maxState {
		return nil, false, fmt.Errorf("%s is %d bytes, more than any tunnel needs", stateName, info.Size())
	}
	raw, err := f.root.ReadFile(stateName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func (f *rootFiles) WriteReport(r l2tpconf.Report) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := reportName + ".tmp"
	// Removed first: whatever the panel's user left under that name, it is
	// replaced by a file root creates, not written through.
	if err := f.root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return f.root.Rename(tmp, reportName)
}

func (f *rootFiles) RemoveReport() error {
	if err := f.root.Remove(reportName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (f *rootFiles) TakeKicks() ([]uint, error) {
	entries, err := fs.ReadDir(f.root.FS(), kickName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []uint
	for _, e := range entries {
		name := e.Name()
		_ = f.root.Remove(path.Join(kickName, name))
		if id, ok := l2tpconf.ParseKickName(name); ok {
			out = append(out, id)
		}
	}
	return out, nil
}
