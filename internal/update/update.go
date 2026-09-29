// Package update replaces this panel's own binary with a newer release.
//
// The shape matters more than the code. The panel managing a set of nodes never
// sends any of them a binary: it asks each one to update itself, and the node
// fetches the release from the project's own repository. That is the difference
// between "somebody who takes the managing panel can run code on every node"
// and "somebody who takes the managing panel can make nodes install an official
// release" — and it is why the update is written this way round.
//
// The fetch is then verified against a key built into this binary. TLS says the
// bytes came from GitHub; a signature says the project produced them. Those are
// different claims, and the second is the one that matters when the question is
// whether to execute the result. A build with no key refuses to update at all
// rather than installing something nobody vouched for.
package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PublicKey is the release-signing key, set at build time with
//
//	-ldflags "-X github.com/abolfazl/w-ui/internal/update.PublicKey=<base64>"
//
// Empty in a build nobody signed, and an empty key refuses every update: a
// panel that would install an unverified binary is a worse outcome than one
// that will not update.
var PublicKey = ""

// Repo is the project the release is fetched from.
const Repo = "AbolfazlTafakori/w-ui"

var (
	// ErrNoKey is a build with no signing key configured.
	ErrNoKey = errors.New("this build carries no release-signing key, so it will not install an update")

	// ErrBadSignature is a download that is not what the project published.
	ErrBadSignature = errors.New("the downloaded panel is not signed by this project")

	// ErrUpToDate is not a failure.
	ErrUpToDate = errors.New("already on the newest release")
)

// Release is what the repository says the newest version is.
type Release struct {
	Version   string `json:"version"`
	Notes     string `json:"notes,omitempty"`
	Published string `json:"published,omitempty"`

	binaryURL    string
	signatureURL string
}

// Available reports the newest release and whether it is newer than current.
func Available(ctx context.Context, current string) (*Release, bool, error) {
	rel, err := latest(ctx)
	if err != nil {
		return nil, false, err
	}
	return rel, newer(current, rel.Version), nil
}

// How long one answer from the release list stands. The overview asks on
// every visit, and GitHub allows an unsigned caller sixty questions an hour;
// a server where GitHub is blocked would otherwise wait out the timeout on
// every visit too. A failure is kept for less time than an answer, so a
// network that comes back is noticed soon.
const (
	answerFor  = 30 * time.Minute
	failureFor = 5 * time.Minute
)

// fetchLatest is latest; a test puts a stand-in for the repository here.
var fetchLatest = latest

var checked struct {
	sync.Mutex
	rel *Release
	err error
	at  time.Time
}

// Checked is Available from the last answer when it is recent enough and
// fresh is false, and from the repository otherwise -- an answer that then
// stands for the next caller. For showing; installing asks Available.
func Checked(ctx context.Context, current string, fresh bool) (*Release, bool, error) {
	checked.Lock()
	defer checked.Unlock()
	keep := answerFor
	if checked.err != nil {
		keep = failureFor
	}
	if fresh || checked.at.IsZero() || time.Since(checked.at) >= keep {
		checked.rel, checked.err = fetchLatest(ctx)
		checked.at = time.Now()
		// A check cut short because the page asking went away says nothing
		// about the repository, and is not kept.
		if checked.err != nil && ctx.Err() != nil {
			checked.at = time.Time{}
		}
	}
	if checked.err != nil {
		return nil, false, checked.err
	}
	return checked.rel, newer(current, checked.rel.Version), nil
}

// latest asks the repository what it has published.
func latest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the release list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("this project has published no releases yet")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("the release list answered %s", resp.Status)
	}

	var payload struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("the release list could not be read: %w", err)
	}

	rel := &Release{
		Version:   strings.TrimPrefix(payload.TagName, "v"),
		Notes:     payload.Body,
		Published: payload.PublishedAt,
	}

	// The asset for this machine, and the signature beside it. Matched on the
	// platform rather than on a fixed filename, so a release that also carries
	// other architectures resolves to the right one.
	want := runtime.GOOS + "-" + runtime.GOARCH
	for _, a := range payload.Assets {
		switch {
		case strings.HasSuffix(a.Name, ".sig") && strings.Contains(a.Name, want):
			rel.signatureURL = a.URL
		case strings.Contains(a.Name, want):
			rel.binaryURL = a.URL
		}
	}
	if rel.binaryURL == "" {
		return nil, fmt.Errorf("that release has no build for %s", want)
	}
	return rel, nil
}

// Apply downloads the release, checks its signature, and puts it in place.
//
// It does not restart anything. The caller ends the process once it has
// answered the request that asked for this, and the service manager brings the
// panel back on the new binary — the same arrangement a restore uses, and for
// the same reason: the running process is the one being replaced.
func Apply(ctx context.Context, rel *Release) error {
	return apply(ctx, rel, nil)
}

// errNoSignature is a release that carries a build and nothing vouching for it.
var errNoSignature = fmt.Errorf("%w: that release has no signature beside its build", ErrBadSignature)

// apply is Apply, telling report how far it has got when report is not nil.
func apply(ctx context.Context, rel *Release, report func(stage Stage, received, total int64)) error {
	if report == nil {
		report = func(Stage, int64, int64) {}
	}
	binary, err := download(ctx, rel, func(received, total int64) {
		report(StageDownloading, received, total)
	})
	if err != nil {
		return err
	}
	report(StageInstalling, 0, 0)
	return put(binary)
}

// put is install; a test puts the binary somewhere other than over itself.
var put = install

// download fetches the release's build and returns it only once the project's
// signature over it checks out. Nothing touches the disk before that.
func download(ctx context.Context, rel *Release, progress func(received, total int64)) ([]byte, error) {
	key, err := signingKey()
	if err != nil {
		return nil, err
	}
	if rel.signatureURL == "" {
		return nil, errNoSignature
	}

	// The signature first: it is small, and a release without one readable
	// is refused before tens of megabytes are fetched for nothing.
	sig, err := fetch(ctx, rel.signatureURL, 4<<10, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading the signature: %w", err)
	}
	signature, err := decodeSignature(sig)
	if err != nil {
		return nil, err
	}
	binary, err := fetch(ctx, rel.binaryURL, maxBinary, progress)
	if err != nil {
		return nil, fmt.Errorf("downloading the panel: %w", err)
	}
	// Before anything is written. A binary that fails this check has to leave
	// no trace on disk at all.
	if !ed25519.Verify(key, binary, signature) {
		return nil, ErrBadSignature
	}
	return binary, nil
}

// maxBinary bounds a download. The panel is about 25 MB; anything near this
// is not a panel.
const maxBinary = 512 << 20

// install puts binary where the running panel's own binary is.
func install(binary []byte) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not find this panel's own binary: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("could not resolve this panel's own binary: %w", err)
	}
	return installAt(self, binary)
}

// installAt writes binary beside path and moves it into place.
func installAt(path string, binary []byte) error {
	// In the same directory, so the move into place is a rename on one
	// filesystem: a copy could be interrupted halfway and leave a truncated
	// binary where the working one was.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wui-update-*")
	if err != nil {
		return fmt.Errorf("could not write beside the panel: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return fmt.Errorf("writing the new panel: %w", err)
	}
	// On disk before the rename, so a power cut cannot leave the name
	// pointing at a file whose contents never arrived.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing the new panel: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing the new panel: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("making the new panel executable: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("putting the new panel in place: %w", err)
	}
	return nil
}

// fetch reads url whole, up to limit bytes, telling progress how much has
// arrived when progress is not nil.
func fetch(ctx context.Context, url string, limit int64, progress func(received, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("answered %s", resp.Status)
	}
	total := max(resp.ContentLength, 0)
	if total > limit {
		return nil, fmt.Errorf("%d bytes is more than a panel", total)
	}
	var body io.Reader = io.LimitReader(resp.Body, limit+1)
	if progress != nil {
		progress(0, total)
		body = &counting{r: body, total: total, report: progress}
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("more than %d bytes is more than a panel", limit)
	}
	// A connection that closed early reads as a short body, not an error.
	// The signature would refuse it anyway; saying what happened is kinder.
	if total > 0 && int64(len(data)) != total {
		return nil, fmt.Errorf("the download stopped at %d of %d bytes", len(data), total)
	}
	return data, nil
}

// counting reports how much of a body has been read.
type counting struct {
	r        io.Reader
	n, total int64
	report   func(received, total int64)
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.report(c.n, c.total)
	return n, err
}

func signingKey() (ed25519.PublicKey, error) {
	if strings.TrimSpace(PublicKey) == "" {
		return nil, ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(PublicKey))
	if err != nil {
		return nil, fmt.Errorf("the release-signing key built into this panel is malformed: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the release-signing key built into this panel is %d bytes, not %d",
			len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// decodeSignature reads the signature file, which holds base64 and nothing else.
func decodeSignature(data []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: the signature file could not be read", ErrBadSignature)
	}
	if len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: the signature is %d bytes, not %d",
			ErrBadSignature, len(raw), ed25519.SignatureSize)
	}
	return raw, nil
}

// Signed reports whether this build can install an update at all, for a page
// that should say so before offering the button.
func Signed() bool {
	_, err := signingKey()
	return err == nil
}

// newer reports whether latest is a later release than current.
func newer(current, latest string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if latest == "" || current == "" {
		return false
	}
	// A development build is never told it is out of date: its version is
	// whatever the person building it chose, and offering to replace it with a
	// release would throw away what they were testing.
	if current == "dev" || strings.Contains(current, "+") {
		return false
	}
	c, cok := parseVersion(current)
	l, lok := parseVersion(latest)
	if !cok || !lok {
		// Not a version this can order; a different one is the best signal.
		return current != latest
	}
	for i := range c.n {
		if c.n[i] != l.n[i] {
			return l.n[i] > c.n[i]
		}
	}
	// The same numbers: a release is newer than a pre-release of itself.
	return c.pre && !l.pre
}

// version is X.Y.Z, and whether a pre-release tag (-rc1) followed it.
type version struct {
	n   [3]int
	pre bool
}

// parseVersion reads X.Y.Z with an optional -suffix. It is not the whole of
// semantic versioning, only as much as this project's tags use: a panel that
// is ahead of the newest release must not be offered that release, and
// 2.10.0 comes after 2.9.0.
func parseVersion(v string) (version, bool) {
	var out version
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, out.pre = v[:i], true
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out.n[i] = n
	}
	return out, true
}

// Verify checks a build against the key this panel was built with.
func Verify(binary, sig []byte) error {
	key, err := signingKey()
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
	return nil
}
