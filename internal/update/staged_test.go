package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A data directory with a release staged in it, as the panel leaves one, and
// an "installed" binary the helper would replace -- a file in a temporary
// directory, never the test itself.
type stagedCase struct {
	dataDir, installed string
	payload            []byte
	priv               ed25519.PrivateKey
}

func stageRelease(t *testing.T, stagedVersion string) *stagedCase {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	withKey(t, pub)

	c := &stagedCase{
		dataDir:   t.TempDir(),
		installed: filepath.Join(t.TempDir(), "wui"),
		payload:   []byte("new panel " + stagedVersion + strings.Repeat(".", 1024)),
		priv:      priv,
	}
	if err := os.WriteFile(c.installed, []byte("old panel"), 0o755); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(c.dataDir, StageDirName)
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	c.write(t, stagedBinary, c.payload)
	c.write(t, stagedSignature, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, c.payload))+"\n"))
	c.write(t, requestFile, []byte(stagedVersion+"\n"))

	savedPath, savedVersion := installedPath, versionOf
	installedPath = func() (string, error) { return c.installed, nil }
	versionOf = func(string) (string, error) { return stagedVersion, nil }
	t.Cleanup(func() { installedPath, versionOf = savedPath, savedVersion })
	return c
}

func (c *stagedCase) write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(c.dataDir, StageDirName, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (c *stagedCase) result(t *testing.T) helperResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.dataDir, StageDirName, resultFile))
	if err != nil {
		t.Fatalf("the helper left no result: %v", err)
	}
	var r helperResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("the result is not JSON: %v", err)
	}
	return r
}

func (c *stagedCase) installedIs(t *testing.T, want string) {
	t.Helper()
	got, err := os.ReadFile(c.installed)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("the installed binary is %.20q, want %.20q", got, want)
	}
}

func TestTheHelperInstallsASignedNewerRelease(t *testing.T) {
	c := stageRelease(t, "v2.3.2")
	got, err := ApplyStaged(c.dataDir, "v2.3.1")
	if err != nil || got != "v2.3.2" {
		t.Fatalf("ApplyStaged = %q, %v", got, err)
	}
	c.installedIs(t, string(c.payload))
	if r := c.result(t); !r.OK || r.Version != "v2.3.2" {
		t.Errorf("result = %+v, want ok with the version", r)
	}
	// The request is taken, so the path unit does not run again; the staged
	// release is gone.
	for _, name := range []string{requestFile, stagedBinary, stagedSignature} {
		if fileExists(filepath.Join(c.dataDir, StageDirName, name)) {
			t.Errorf("%s was left behind", name)
		}
	}
}

func TestTheHelperRefusesAStepBack(t *testing.T) {
	for _, staged := range []string{"v2.3.1", "v2.2.9"} {
		c := stageRelease(t, staged)
		if _, err := ApplyStaged(c.dataDir, "v2.3.1"); err == nil {
			t.Errorf("%s over v2.3.1 was installed", staged)
		}
		c.installedIs(t, "old panel")
		if r := c.result(t); r.OK || r.Error == "" {
			t.Errorf("%s: result = %+v, want the refusal", staged, r)
		}
	}
}

func TestTheHelperRefusesWhatTheProjectDidNotSign(t *testing.T) {
	c := stageRelease(t, "v2.3.2")
	// The panel's user swaps the binary after the panel checked it.
	c.write(t, stagedBinary, []byte("something else entirely"))
	_, err := ApplyStaged(c.dataDir, "v2.3.1")
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a swapped binary gave %v, want a signature refusal", err)
	}
	c.installedIs(t, "old panel")
}

func TestTheHelperFollowsNoLinkOutOfTheDataDirectory(t *testing.T) {
	c := stageRelease(t, "v2.3.2")
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("root's own file"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(c.dataDir, StageDirName, stagedBinary)
	os.Remove(link)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if _, err := ApplyStaged(c.dataDir, "v2.3.1"); err == nil {
		t.Fatal("a link out of the data directory was read")
	}
	c.installedIs(t, "old panel")
	if got, _ := os.ReadFile(outside); string(got) != "root's own file" {
		t.Error("the file the link pointed at was changed")
	}
}

func TestTheHelperRefusesSomethingThatIsNotAFile(t *testing.T) {
	c := stageRelease(t, "v2.3.2")
	p := filepath.Join(c.dataDir, StageDirName, stagedBinary)
	os.Remove(p)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyStaged(c.dataDir, "v2.3.1"); err == nil {
		t.Fatal("a directory in place of the release was accepted")
	}
	c.installedIs(t, "old panel")
}

// The panel's half: leave the release, wait for the helper's answer.
func TestTheHandOverWaitsForTheHelper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer helperResult
	}{
		{"installed", helperResult{OK: true, Version: "v2.3.2"}},
		{"refused", helperResult{Error: "v2.3.1 is not newer than the installed v2.3.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := StageDir
			StageDir = filepath.Join(t.TempDir(), StageDirName)
			t.Cleanup(func() { StageDir = saved })

			// A stand-in helper: answers once the request is there, and
			// checks the release arrived whole before it.
			go func() {
				for !fileExists(filepath.Join(StageDir, requestFile)) {
					time.Sleep(10 * time.Millisecond)
				}
				if b, _ := os.ReadFile(filepath.Join(StageDir, stagedBinary)); string(b) != "binary" {
					return
				}
				raw, _ := json.Marshal(tc.answer)
				writeAtomic(filepath.Join(StageDir, resultFile), raw)
			}()

			err := handOver(context.Background(), []byte("binary"), []byte("sig"), "v2.3.2")
			if tc.answer.OK && err != nil {
				t.Errorf("an install the helper made was reported as %v", err)
			}
			if !tc.answer.OK && (err == nil || err.Error() != tc.answer.Error) {
				t.Errorf("the helper's refusal came back as %v", err)
			}
		})
	}
}

func TestAPanelThatCannotInstallSaysSo(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	withKey(t, pub)
	savedW, savedUnit, savedDir := writable, HelperUnit, StageDir
	t.Cleanup(func() { writable, HelperUnit, StageDir = savedW, savedUnit, savedDir })

	StageDir = filepath.Join(t.TempDir(), StageDirName)
	writable = func() bool { return false }
	HelperUnit = filepath.Join(t.TempDir(), "absent.path")
	if err := CanInstall(); !errors.Is(err, ErrCannotInstall) {
		t.Errorf("no write access and no helper gave %v, want ErrCannotInstall", err)
	}
	if err := Start(&Release{Version: "9.9.9", signatureURL: "x"}, "1.0.0", nil); !errors.Is(err, ErrCannotInstall) {
		t.Errorf("Start gave %v, want ErrCannotInstall before anything was fetched", err)
	}

	HelperUnit = filepath.Join(t.TempDir(), "wui-update.path")
	os.WriteFile(HelperUnit, nil, 0o644)
	if err := CanInstall(); err != nil {
		t.Errorf("with the helper installed CanInstall gave %v", err)
	}
	if m := mode(); m != installHelper {
		t.Errorf("mode = %v, want the helper", m)
	}
	writable = func() bool { return true }
	if m := mode(); m != installDirect {
		t.Errorf("mode = %v, want direct when the panel can write its binary", m)
	}
}
