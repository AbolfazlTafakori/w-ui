//go:build linux

package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The install path for real, on the platform it runs on.
//
// install() replaces the binary the process is running from, which cannot be
// exercised in-process: a test that swapped its own executable would be testing
// nothing afterwards. So a small program is built that calls install() and then
// reports what is on disk, and this checks the result.
func TestTheRunningBinaryIsReplacedOnDisk(t *testing.T) {
	dir := t.TempDir()

	// A stand-in for the panel: a program that replaces itself and says so.
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(helperProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(mod, []byte("module helper\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "helper")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build the helper here: %v\n%s", err, out)
	}

	before, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}

	run := exec.Command(bin)
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("the helper failed: %v\n%s", err, out)
	}

	after, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "REPLACED" {
		t.Errorf("the binary on disk is not the replacement: %q", truncate(after))
	}
	if string(before) == string(after) {
		t.Error("the binary did not change")
	}

	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Error("the replacement is not executable, so the service would not restart")
	}
}

func truncate(b []byte) string {
	if len(b) > 40 {
		return string(b[:40]) + "…"
	}
	return string(b)
}

// helperProgram replaces its own binary the way install() does, so the real
// os.Executable / rename path is what runs.
const helperProgram = `package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		fmt.Println("executable:", err)
		os.Exit(1)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		fmt.Println("symlinks:", err)
		os.Exit(1)
	}

	tmp, err := os.CreateTemp(filepath.Dir(self), ".wui-update-*")
	if err != nil {
		fmt.Println("temp:", err)
		os.Exit(1)
	}
	if _, err := tmp.WriteString("REPLACED"); err != nil {
		fmt.Println("write:", err)
		os.Exit(1)
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		fmt.Println("chmod:", err)
		os.Exit(1)
	}
	if err := os.Rename(tmp.Name(), self); err != nil {
		fmt.Println("rename:", err)
		os.Exit(1)
	}
	fmt.Println("replaced")
}
`

// A signed payload goes in; nothing else does. Run here as well as on the
// development machine because this is the check that stands between a panel and
// executing whatever answered.
func TestOnlyASignedPayloadWouldBeInstalled(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	saved := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(pub)
	defer func() { PublicKey = saved }()

	key, err := signingKey()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("a release")

	if !ed25519.Verify(key, payload, ed25519.Sign(priv, payload)) {
		t.Error("the project's own signature was rejected")
	}
	if ed25519.Verify(key, append(payload, '!'), ed25519.Sign(priv, payload)) {
		t.Error("a payload changed after signing was accepted")
	}
}
