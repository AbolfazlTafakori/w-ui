package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

// fakeGitHub answers the runs endpoint: the n-th question gets answers[n],
// the last answer from then on. It remembers how often, and what, it was
// asked.
type fakeGitHub struct {
	mu      sync.Mutex
	answers [][]Run
	calls   int
	asked   *http.Request
}

func (f *fakeGitHub) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asked = r.Clone(context.Background())
		if r.URL.Path != "/repos/o/n/actions/workflows/ci.yml/runs" {
			http.NotFound(w, r)
			return
		}
		i := min(f.calls, len(f.answers)-1)
		f.calls++
		var runs []Run
		if i >= 0 {
			runs = f.answers[i]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(runs), "workflow_runs": runs})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeGitHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func run(sha, branch, status, conclusion, created string) Run {
	return Run{HeadSHA: sha, HeadBranch: branch, Status: status, Conclusion: conclusion, CreatedAt: created, URL: "https://ci/" + created}
}

// check asks srv, looking again every few milliseconds, for at most wait.
func check(t *testing.T, srv *httptest.Server, wait time.Duration) (*Run, error) {
	t.Helper()
	c := Check{Client: srv.Client(), API: srv.URL, Repo: "o/n", Workflow: "ci.yml", Branch: "main", SHA: sha, Every: 5 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	done := make(chan struct{})
	var got *Run
	var err error
	go func() { got, err = c.Wait(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(wait + 5*time.Second):
		t.Fatal("the check did not stop when its time ran out")
	}
	return got, err
}

// What is decided at once: a green run lets the release through; a red one,
// no run, one on another branch or for another commit refuse it.
func TestWhatIsDecidedAtOnce(t *testing.T) {
	for name, c := range map[string]struct {
		runs []Run
		ok   bool
		want string
	}{
		"green":                   {[]Run{run(sha, "main", "completed", "success", "2026-10-01T10:00:00Z")}, true, ""},
		"red":                     {[]Run{run(sha, "main", "completed", "failure", "2026-10-01T10:00:00Z")}, false, "finished failure"},
		"cancelled":               {[]Run{run(sha, "main", "completed", "cancelled", "2026-10-01T10:00:00Z")}, false, "finished cancelled"},
		"never run":               {nil, false, "has no ci.yml run on main"},
		"green on another branch": {[]Run{run(sha, "upgrade-safety", "completed", "success", "2026-10-01T10:00:00Z")}, false, "has no ci.yml run on main"},
		"green on another commit": {[]Run{run("ffff", "main", "completed", "success", "2026-10-01T10:00:00Z")}, false, "has no ci.yml run on main"},
		"green, then re-run red": {[]Run{
			run(sha, "main", "completed", "success", "2026-10-01T10:00:00Z"),
			run(sha, "main", "completed", "failure", "2026-10-01T11:00:00Z"),
		}, false, "finished failure"},
		"red, then re-run green": {[]Run{
			run(sha, "main", "completed", "failure", "2026-10-01T10:00:00Z"),
			run(sha, "main", "completed", "success", "2026-10-01T11:00:00Z"),
		}, true, ""},
	} {
		f := &fakeGitHub{answers: [][]Run{c.runs}}
		got, err := check(t, f.start(t), time.Second)
		if c.ok {
			if err != nil || got == nil {
				t.Errorf("%s: refused: %v", name, err)
			}
		} else if err == nil {
			t.Errorf("%s: let through", name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want it to say %q", name, err, c.want)
		}
		if f.count() != 1 {
			t.Errorf("%s: asked %d times; a run that is decided is asked about once", name, f.count())
		}
	}
}

// A tag pushed while CI is still going waits for it: green when it ends
// green, refused when it ends red.
func TestARunningCIIsWaitedFor(t *testing.T) {
	running := []Run{run(sha, "main", "in_progress", "", "2026-10-01T10:00:00Z")}
	queued := []Run{run(sha, "main", "queued", "", "2026-10-01T10:00:00Z")}

	f := &fakeGitHub{answers: [][]Run{queued, running, running,
		{run(sha, "main", "completed", "success", "2026-10-01T10:00:00Z")}}}
	if _, err := check(t, f.start(t), 5*time.Second); err != nil {
		t.Errorf("in progress, then green: %v", err)
	}
	if f.count() != 4 {
		t.Errorf("asked %d times, want 4: queued, running, running, green", f.count())
	}

	red := &fakeGitHub{answers: [][]Run{running, {run(sha, "main", "completed", "failure", "2026-10-01T10:00:00Z")}}}
	if _, err := check(t, red.start(t), 5*time.Second); err == nil || !strings.Contains(err.Error(), "finished failure") {
		t.Errorf("in progress, then red: %v", err)
	}
}

// A run that does not finish in time is a refusal that says what to do.
func TestARunThatDoesNotFinishTimesOut(t *testing.T) {
	f := &fakeGitHub{answers: [][]Run{{run(sha, "main", "in_progress", "", "2026-10-01T10:00:00Z")}}}
	start := time.Now()
	_, err := check(t, f.start(t), 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "still in_progress when the wait ran out") {
		t.Fatalf("a run going past the wait: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the wait took %s, well past its limit", time.Since(start))
	}
	if f.count() < 2 {
		t.Errorf("asked %d times; a running CI is looked at again", f.count())
	}

	// It ends when the wait ends, not at the next look: with looks two
	// seconds apart and a wait of a tenth of one, the refusal comes at once.
	long := &fakeGitHub{answers: [][]Run{{run(sha, "main", "in_progress", "", "2026-10-01T10:00:00Z")}}}
	lsrv := long.start(t)
	lc := Check{Client: lsrv.Client(), API: lsrv.URL, Repo: "o/n", Workflow: "ci.yml", Branch: "main", SHA: sha, Every: 2 * time.Second}
	lctx, lcancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer lcancel()
	began := time.Now()
	if _, err := lc.Wait(lctx); err == nil || time.Since(began) > time.Second {
		t.Errorf("a wait of 100ms with looks 2s apart ended after %s: %v", time.Since(began), err)
	}

	// The wait running out while a question is still on its way is the same
	// refusal, not a network error: GitHub answers the first question at
	// once and the second only after the wait has ended.
	calls := 0
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{run(sha, "main", "in_progress", "", "x")}})
	}))
	defer slow.Close()
	_, err = check(t, slow, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "still in_progress when the wait ran out") {
		t.Errorf("the wait running out mid-question: %v", err)
	}
}

// The question is asked about that commit on that branch, with the
// workflow's token; and an API that cannot be asked is a refusal at once,
// not a pass and not a wait.
func TestTheQuestionAndAnUnreachableAPI(t *testing.T) {
	f := &fakeGitHub{answers: [][]Run{{run(sha, "main", "completed", "success", "x")}}}
	srv := f.start(t)
	c := Check{Client: srv.Client(), API: srv.URL, Token: "tok", Repo: "o/n", Workflow: "ci.yml", Branch: "main", SHA: sha, Every: time.Millisecond}
	if _, err := c.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.asked.URL.Query().Get("head_sha") != sha || f.asked.URL.Query().Get("branch") != "main" {
		t.Errorf("asked %s", f.asked.URL)
	}
	if f.asked.Header.Get("Authorization") != "Bearer tok" {
		t.Errorf("the token was not sent: %q", f.asked.Header.Get("Authorization"))
	}

	calls := 0
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer down.Close()
	c = Check{Client: down.Client(), API: down.URL, Repo: "o/n", Workflow: "ci.yml", Branch: "main", SHA: sha, Every: time.Millisecond}
	if _, err := c.Wait(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("an API that refused: %v", err)
	}
	if calls != 1 {
		t.Errorf("an API that refused was asked %d times", calls)
	}
}

// release.yml itself: nothing is built or published until the CI check
// passes, and a release without the signing key fails instead of shipping
// unsigned -- a release panels then refuse to install from, with only a
// warning nobody reads to say so.
func TestTheReleaseWorkflowWaitsForCIAndRequiresTheKey(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	// The jobs, by name: each starts at a two-space "name:" line and runs to
	// the next.
	jobs := map[string]string{}
	name := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			name = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if name != "" {
			jobs[name] += line + "\n"
		}
	}
	job := func(n string) string {
		j, ok := jobs[n]
		if !ok {
			t.Fatalf("release.yml has no %s job", n)
		}
		return j
	}

	gate := job("ci-passed")
	if !strings.Contains(gate, "go run ./tools/release ci-passed") || !strings.Contains(gate, "${{ github.sha }}") {
		t.Errorf("the ci-passed job does not ask about the tagged commit:\n%s", gate)
	}
	if !strings.Contains(gate, "actions: read") {
		t.Error("the ci-passed job cannot read the workflow runs without actions: read")
	}
	release := job("release")
	if !strings.Contains(release, "needs: [ci-passed]") {
		t.Errorf("the release job does not wait for ci-passed:\n%.400s", release)
	}
	sign := release[strings.Index(release, "- name: Sign"):]
	sign = sign[:strings.Index(sign[1:], "- name:")+1]
	if strings.Contains(sign, "exit 0") || !strings.Contains(sign, "exit 1") {
		t.Errorf("a missing signing key does not fail the release:\n%s", sign)
	}
}
