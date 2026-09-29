package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A release served locally, signed with a key this test holds, and the
// binary Start installs caught instead of written over the test itself.
type fakeRelease struct {
	rel       *Release
	installed chan []byte
	gate      chan struct{} // closed to let the binary's body finish
	payload   []byte
}

func serveRelease(t *testing.T, signedBy ed25519.PrivateKey) *fakeRelease {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	withKey(t, pub)
	if signedBy == nil {
		signedBy = priv
	}

	f := &fakeRelease{
		installed: make(chan []byte, 1),
		gate:      make(chan struct{}),
		payload:   []byte(strings.Repeat("w", 64<<10)),
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(signedBy, f.payload))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sig":
			w.Write([]byte(sig + "\n"))
		case "/bin":
			w.Header().Set("Content-Length", strconv.Itoa(len(f.payload)))
			half := len(f.payload) / 2
			w.Write(f.payload[:half])
			w.(http.Flusher).Flush()
			<-f.gate
			w.Write(f.payload[half:])
		}
	}))
	t.Cleanup(srv.Close)

	saved := put
	put = func(b []byte) error { f.installed <- b; return nil }
	t.Cleanup(func() {
		put = saved
		job.Lock()
		job.p = Progress{}
		job.Unlock()
	})
	f.rel = &Release{Version: "9.9.9", binaryURL: srv.URL + "/bin", signatureURL: srv.URL + "/sig"}
	return f
}

// waitFor polls Status until ok says so.
func waitFor(t *testing.T, what string, ok func(Progress) bool) Progress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := Status(); ok(p) {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never %s; last %+v", what, Status())
	return Progress{}
}

func TestAnInstallIsFollowedToTheRestart(t *testing.T) {
	f := serveRelease(t, nil)
	var (
		mu       sync.Mutex
		finished []error
		done     = make(chan struct{})
	)
	err := Start(f.rel, "2.3.0", func(err error) {
		mu.Lock()
		finished = append(finished, err)
		mu.Unlock()
		close(done)
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Halfway through the download, the page is told how far.
	p := waitFor(t, "reported half the download", func(p Progress) bool {
		return p.Stage == StageDownloading && p.Received == int64(len(f.payload)/2)
	})
	if p.Total != int64(len(f.payload)) || p.From != "2.3.0" || p.To != "9.9.9" {
		t.Errorf("mid-download progress = %+v", p)
	}
	// A second install meanwhile is refused, not run beside the first.
	if err := Start(f.rel, "2.3.0", nil); !errors.Is(err, ErrBusy) {
		t.Errorf("a second Start during an install gave %v, want ErrBusy", err)
	}

	close(f.gate)
	<-done
	if got := <-f.installed; string(got) != string(f.payload) {
		t.Error("what was installed is not what was signed")
	}
	if p := Status(); p.Stage != StageRestarting || p.Error != "" {
		t.Errorf("after the install, Status = %+v, want restarting", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(finished) != 1 || finished[0] != nil {
		t.Errorf("finished was called with %v, want once with nil", finished)
	}
}

func TestAnUnsignedDownloadFailsAndInstallsNothing(t *testing.T) {
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := serveRelease(t, stranger)
	close(f.gate)
	got := make(chan error, 1)
	if err := Start(f.rel, "2.3.0", func(err error) { got <- err }); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := <-got; !errors.Is(err, ErrBadSignature) {
		t.Fatalf("finished with %v, want a signature refusal", err)
	}
	select {
	case <-f.installed:
		t.Fatal("a download signed by somebody else was installed")
	default:
	}
	p := Status()
	if p.Stage != StageFailed || p.Error == "" {
		t.Errorf("Status = %+v, want failed with the reason", p)
	}
	// A failed install does not block the next attempt.
	again := make(chan error, 1)
	if err := Start(f.rel, "2.3.0", func(err error) { again <- err }); err != nil {
		t.Fatalf("an install that failed kept the next from starting: %v", err)
	}
	<-again
}

func TestWhatIsKnownIsRefusedBeforeStarting(t *testing.T) {
	f := serveRelease(t, nil)
	close(f.gate)
	noSig := *f.rel
	noSig.signatureURL = ""
	if err := Start(&noSig, "2.3.0", nil); !errors.Is(err, ErrBadSignature) {
		t.Errorf("a release with no signature gave %v", err)
	}
	withKey(t, nil)
	if err := Start(f.rel, "2.3.0", nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("a build with no key gave %v", err)
	}
	if p := Status(); p.Stage != "" {
		t.Errorf("a refused Start left Status %+v", p)
	}
}

func TestADownloadCutShortIsSaidSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("only a little"))
	}))
	defer srv.Close()
	_, err := fetch(t.Context(), srv.URL, maxBinary, nil)
	if err == nil {
		t.Fatal("a body shorter than its length was accepted")
	}
}
