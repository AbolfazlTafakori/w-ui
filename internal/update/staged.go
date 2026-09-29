package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Installing through the root helper.
//
// The installer runs the panel as an unprivileged user, in a sandbox where
// everything outside its own data is read-only -- including the binary it runs
// from. That is right for a process facing the internet, and it means the panel
// cannot replace itself. Writing beside the binary failed, and the page showed
// "internal error" for every update ever tried from a real server.
//
// So the panel does the half it can: it downloads the release and checks the
// signature, into a directory of its own, and leaves a request there. A
// systemd path unit sees the request and runs the installed binary -- the one
// root put in place, with the signing key built into it -- as root, with
// apply-update. That checks everything again, from its own copy of the bytes
// rather than the file the panel could still change, refuses anything that is
// not a newer release, puts it in place and restarts the panel. Root only ever
// runs a build this project signed.

// StageDir is where the panel leaves a downloaded release for the helper; set
// at startup to "update" inside the data directory.
var StageDir string

// HelperUnit is the systemd path unit the installer writes. A panel whose
// server has it can hand an update over; one without it cannot install, and
// says how to put that right.
var HelperUnit = "/etc/systemd/system/wui-update.path"

// The files in StageDir.
const (
	stagedBinary    = "wui"
	stagedSignature = "wui.sig"
	requestFile     = "request"
	resultFile      = "result"
)

// helperTimeout is how long the panel waits for the helper's answer. It reads
// a file already on disk, checks it, and moves it; a minute is generous.
const helperTimeout = 2 * time.Minute

// ErrCannotInstall is a panel that can neither write its own binary nor hand
// an update to the helper.
var ErrCannotInstall = errors.New("this panel runs without permission to replace its own binary, " +
	"and the update helper is not installed on this server. Run the update script once on the " +
	"server as root; from then on updates install from here: " +
	"bash <(curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/update.sh)")

// How an update gets into place on this server.
type installMode int

const (
	installNone   installMode = iota
	installDirect             // the panel can write its own binary
	installHelper             // the root helper does it
)

// mode decides how this panel installs, without changing anything.
func mode() installMode {
	if writable() {
		return installDirect
	}
	if StageDir != "" && fileExists(HelperUnit) {
		return installHelper
	}
	return installNone
}

// CanInstall reports whether this panel can install an update at all, and if
// not, why -- for a page to say before offering the button.
func CanInstall() error {
	if _, err := signingKey(); err != nil {
		return err
	}
	if mode() == installNone {
		return ErrCannotInstall
	}
	return nil
}

// writable is canWriteBesideSelf; a test decides it.
var writable = canWriteBesideSelf

// canWriteBesideSelf is whether a file can be created next to the running
// binary, which is what replacing it takes.
func canWriteBesideSelf() bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return false
	}
	f, err := os.CreateTemp(filepath.Dir(self), ".wui-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// helperResult is what apply-update leaves for the panel.
type helperResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handOver leaves a checked release for the helper and waits for its answer.
func handOver(ctx context.Context, binary, sig []byte, version string) error {
	if err := os.MkdirAll(StageDir, 0o700); err != nil {
		return fmt.Errorf("could not prepare the update directory: %w", err)
	}
	// Whatever an earlier attempt left is not this one's answer.
	os.Remove(filepath.Join(StageDir, resultFile))
	if err := writeAtomic(filepath.Join(StageDir, stagedBinary), binary); err != nil {
		return fmt.Errorf("could not leave the release for the helper: %w", err)
	}
	if err := writeAtomic(filepath.Join(StageDir, stagedSignature), sig); err != nil {
		return fmt.Errorf("could not leave the release for the helper: %w", err)
	}
	// Last, and whole: its appearing is what starts the helper.
	if err := writeAtomic(filepath.Join(StageDir, requestFile), []byte(version+"\n")); err != nil {
		return fmt.Errorf("could not ask the helper: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("the update helper did not answer. Check it on the server: " +
				"systemctl status wui-update.path wui-update.service")
		case <-tick.C:
		}
		raw, err := os.ReadFile(filepath.Join(StageDir, resultFile))
		if err != nil {
			continue
		}
		var res helperResult
		if err := json.Unmarshal(raw, &res); err != nil {
			continue // being written
		}
		if !res.OK {
			return errors.New(res.Error)
		}
		return nil
	}
}

// writeAtomic writes a file whole under a temporary name and moves it into
// place, so nobody reading it sees half.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ── the root side ──────────────────────────────────────────────────────────

// ApplyStaged is apply-update: run as root by the helper unit, with dataDir the
// panel's data directory and current the version of the binary running it. It
// installs what the panel left in dataDir/update if it is signed and newer,
// and records the outcome where the panel reads it. The returned version is
// what was installed.
//
// The panel's user owns that directory and can change anything in it at any
// moment, so it is trusted for nothing but bytes. Every file operation goes
// through an os.Root on the data directory -- whose own parent root owns, so it
// cannot be swapped -- which refuses a link that leads out of it: a link
// planted in place of a staged file cannot make root read, write or delete
// anything elsewhere. What is checked is read once into memory, and what is
// written and run afterwards is root's own copy of it.
func ApplyStaged(dataDir, current string) (installed string, err error) {
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return "", fmt.Errorf("opening the panel's data directory: %w", err)
	}
	defer root.Close()
	defer func() { recordResult(root, installed, err) }()

	stage := func(name string) string { return filepath.Join(StageDirName, name) }
	// Taken first, so the path unit is not started again for the same
	// request whatever happens next.
	root.Remove(stage(requestFile))
	defer root.Remove(stage(stagedBinary))
	defer root.Remove(stage(stagedSignature))

	binary, err := readAtMost(root, stage(stagedBinary), maxBinary)
	if err != nil {
		return "", fmt.Errorf("reading the downloaded release: %w", err)
	}
	sig, err := readAtMost(root, stage(stagedSignature), 4<<10)
	if err != nil {
		return "", fmt.Errorf("reading its signature: %w", err)
	}
	if err := Verify(binary, sig); err != nil {
		return "", err
	}

	self, err := installedPath()
	if err != nil {
		return "", err
	}
	return replaceIfNewer(self, binary, current)
}

// installedPath is the binary running, which is the one to replace; a test
// points it somewhere else.
var installedPath = func() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not find the installed panel: %w", err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", fmt.Errorf("could not resolve the installed panel: %w", err)
	}
	return self, nil
}

// versionOf asks a build its version; a test answers for it.
var versionOf = func(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", fmt.Errorf("the new panel did not start: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// StageDirName is the staging directory's name inside the data directory.
const StageDirName = "update"

// replaceIfNewer writes binary beside path, asks it its version, and moves it
// over path only if that is a later release than current. path's directory
// belongs to root, so nothing here races anybody.
func replaceIfNewer(path string, binary []byte, current string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wui-update-*")
	if err != nil {
		return "", fmt.Errorf("could not write beside the panel: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing the new panel: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", fmt.Errorf("making the new panel executable: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("writing the new panel: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("writing the new panel: %w", err)
	}

	// Signed, so asking it is safe; asked because a signed older release is
	// still a step back, and installing one would undo whatever it fixed.
	next, err := versionOf(name)
	if err != nil {
		return "", err
	}
	if !newer(current, next) {
		return "", fmt.Errorf("%s is not newer than the installed %s; nothing was installed", next, current)
	}

	if err := os.Rename(name, path); err != nil {
		return "", fmt.Errorf("putting the new panel in place: %w", err)
	}
	return next, nil
}

// readAtMost reads a regular file inside root whole, refusing one past limit.
// Anything else -- a pipe, which would block the read forever, a device, a
// directory -- is refused before it is opened.
func readAtMost(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a file", filepath.Base(name))
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is larger than a panel", filepath.Base(name))
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than a panel", filepath.Base(name))
	}
	return data, nil
}

// recordResult leaves the outcome for the panel: written under a fresh name
// that cannot already exist, then renamed over the result, so a link put in
// its place is replaced rather than followed.
func recordResult(root *os.Root, version string, err error) {
	res := helperResult{OK: err == nil, Version: version}
	if err != nil {
		res.Error = err.Error()
	}
	raw, _ := json.Marshal(res)
	tmp := filepath.Join(StageDirName, fmt.Sprintf(".result-%d", time.Now().UnixNano()))
	f, ferr := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if ferr != nil {
		return
	}
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		root.Remove(tmp)
		return
	}
	if root.Rename(tmp, filepath.Join(StageDirName, resultFile)) != nil {
		root.Remove(tmp)
	}
}
