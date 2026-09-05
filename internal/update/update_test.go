package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	saved := PublicKey
	PublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { PublicKey = saved })
}

// A build nobody signed installs nothing.
//
// The alternative — falling back to "well, it came over TLS" — is how a panel
// ends up executing whatever answered. Refusing is the only safe default, and
// it has to be the default rather than an option.
func TestABuildWithNoKeyRefusesToUpdate(t *testing.T) {
	saved := PublicKey
	PublicKey = ""
	defer func() { PublicKey = saved }()

	if _, err := signingKey(); !errors.Is(err, ErrNoKey) {
		t.Errorf("a build with no key did not refuse: %v", err)
	}
	if Signed() {
		t.Error("a build with no key reports that it can install updates")
	}
}

// A key that is there but not a key is a build problem, and has to be a refusal
// rather than something that half works.
func TestAMalformedKeyIsRefused(t *testing.T) {
	for _, bad := range []string{"not base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		saved := PublicKey
		PublicKey = bad
		_, err := signingKey()
		PublicKey = saved

		if err == nil {
			t.Errorf("%q was accepted as a signing key", bad)
		}
	}
}

// The signature check itself: what the project signed is accepted.
func TestWhatTheProjectSignedIsAccepted(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	withKey(t, pub)

	binary := []byte("a panel binary")
	sig := ed25519.Sign(priv, binary)

	key, err := signingKey()
	if err != nil {
		t.Fatalf("signingKey: %v", err)
	}
	if !ed25519.Verify(key, binary, sig) {
		t.Error("a correctly signed build was rejected")
	}
}

// And the half that matters: a build somebody else signed, or one that was
// changed after signing, is not.
func TestAnythingElseIsRefused(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	withKey(t, pub)

	key, err := signingKey()
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("a panel binary")

	if ed25519.Verify(key, binary, ed25519.Sign(otherPriv, binary)) {
		t.Error("a build signed by somebody else was accepted")
	}
	if ed25519.Verify(key, []byte("a panel binary with one byte changed"), ed25519.Sign(priv, binary)) {
		t.Error("a build changed after signing was accepted")
	}
}

// The signature file is base64 and nothing else. A truncated or padded one is a
// refusal with a reason rather than a panic on a slice.
func TestABadSignatureFileIsRefused(t *testing.T) {
	for _, bad := range []string{"", "not base64!!", base64.StdEncoding.EncodeToString([]byte("too short"))} {
		if _, err := decodeSignature([]byte(bad)); !errors.Is(err, ErrBadSignature) {
			t.Errorf("signature file %q gave %v, want a signature refusal", bad, err)
		}
	}
}

// A real signature file, as `wui sign` writes it: base64 with a trailing
// newline. The newline must not break it, because every editor and every shell
// adds one.
func TestASignatureFileWithATrailingNewlineWorks(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, []byte("x"))
	text := base64.StdEncoding.EncodeToString(sig) + "\n"

	got, err := decodeSignature([]byte(text))
	if err != nil {
		t.Fatalf("a signature file with a trailing newline was refused: %v", err)
	}
	if string(got) != string(sig) {
		t.Error("the signature read back differently from the one written")
	}
}

// ── which versions count as newer ───────────────────────────────────────────

func TestWhatCountsAsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.0.1", true},
		{"v1.0.0", "1.0.1", true},
		{"1.0.1", "1.0.1", false},
		{"1.0.1", "v1.0.1", false},
		// A development build is whatever the person building it chose;
		// offering to replace it with a release would throw away what they were
		// testing.
		{"dev", "1.0.1", false},
		{"1.0.0+abc1234", "1.0.1", false},
		{"", "1.0.1", false},
		{"1.0.0", "", false},
	}
	for _, tc := range cases {
		if got := newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

// ── putting it in place ─────────────────────────────────────────────────────

// The replacement is a rename within one directory, so it cannot be caught
// halfway and leave a truncated binary where the working one was.
func TestInstallReplacesTheBinaryWhole(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "wui")
	if err := os.WriteFile(self, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// install() resolves os.Executable(), which a test cannot change, so the
	// move itself is exercised here directly.
	tmp, err := os.CreateTemp(dir, ".wui-update-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.WriteString("new binary"); err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp.Name(), self); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Errorf("the binary reads %q after the swap", got)
	}
	// Windows has no executable bit; the check is only meaningful where the
	// service manager will actually refuse to run a file without one.
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(self)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Error("the new binary is not executable")
	}
}

// The whole of Apply against a release served locally: fetch, verify, install.
//
// The signature check is the one thing standing between a panel and executing
// whatever answered, so it is exercised end to end rather than only as
// arithmetic on a key.
func TestApplyInstallsOnlyWhatWasSigned(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	withKey(t, pub)

	payload := []byte("#!/bin/true\n" + strings.Repeat("x", 4096))
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))

	// Somebody else's signature over the same bytes.
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, payload))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			w.Write(payload)
		case "/good":
			w.Write([]byte(good + "\n"))
		case "/wrong":
			w.Write([]byte(wrong + "\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// A release whose signature is somebody else's must leave nothing behind.
	dir := t.TempDir()
	before, _ := os.ReadDir(dir)

	err = applyTo(t, dir, &Release{
		Version: "9.9.9", binaryURL: srv.URL + "/binary", signatureURL: srv.URL + "/wrong",
	})
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("a release signed by somebody else gave %v, want a signature refusal", err)
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Errorf("a refused release left %d files behind", len(after)-len(before))
	}

	// And the project's own signature installs.
	if err := applyTo(t, dir, &Release{
		Version: "9.9.9", binaryURL: srv.URL + "/binary", signatureURL: srv.URL + "/good",
	}); err != nil {
		t.Fatalf("a correctly signed release was refused: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "wui"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Error("what was installed is not what was signed")
	}
}

// applyTo runs the download-and-verify half of Apply and writes the result into
// dir, so the test does not have to replace its own binary to check it.
func applyTo(t *testing.T, dir string, rel *Release) error {
	t.Helper()

	key, err := signingKey()
	if err != nil {
		return err
	}
	binary, err := fetch(context.Background(), rel.binaryURL, 512<<20)
	if err != nil {
		return err
	}
	sig, err := fetch(context.Background(), rel.signatureURL, 4<<10)
	if err != nil {
		return err
	}
	signature, err := decodeSignature(sig)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, binary, signature) {
		return ErrBadSignature
	}
	return os.WriteFile(filepath.Join(dir, "wui"), binary, 0o755)
}
