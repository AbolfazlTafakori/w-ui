// Command release holds the checks the release workflow runs before it
// publishes anything:
//
//	release ci-passed -repo OWNER/NAME -sha COMMIT
//
// waits for the CI workflow to finish on main for exactly that commit -- the
// one a pushed tag points to -- and succeeds only if it finished green. The
// release workflow builds and publishes from a tag alone, and every panel's
// update button installs the newest release, so a tag pushed before CI is
// green would ship whatever CI has not looked at yet. This is the step that
// waits for it, and refuses when it is red.
//
// It only reads: GETs to the GitHub API, with the workflow's own token. Run
// it by hand to see what a release of a commit would be told:
//
//	GITHUB_TOKEN=... go run ./tools/release ci-passed -repo AbolfazlTafakori/w-ui -sha $(git rev-parse HEAD)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "ci-passed" {
		fmt.Fprintln(os.Stderr, "usage: release ci-passed -repo OWNER/NAME -sha COMMIT")
		os.Exit(2)
	}
	if err := ciPassedCommand(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func ciPassedCommand(args []string) error {
	fs := flag.NewFlagSet("ci-passed", flag.ContinueOnError)
	c := Check{Client: http.DefaultClient, Token: os.Getenv("GITHUB_TOKEN"), Log: os.Stdout}
	fs.StringVar(&c.Repo, "repo", "", "owner/name")
	fs.StringVar(&c.SHA, "sha", "", "the commit the tag points to")
	fs.StringVar(&c.Workflow, "workflow", "ci.yml", "the workflow that must have passed")
	fs.StringVar(&c.Branch, "branch", "main", "the branch it must have passed on")
	fs.StringVar(&c.API, "api", "https://api.github.com", "the GitHub API")
	fs.DurationVar(&c.Every, "every", 30*time.Second, "how often to look again while CI is still running")
	timeout := fs.Duration("timeout", 30*time.Minute, "how long to wait for CI to finish")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if c.Repo == "" || c.SHA == "" {
		return errors.New("ci-passed needs -repo and -sha")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	run, err := c.Wait(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("CI passed on %s for %s: %s\n", c.Branch, c.SHA, run.URL)
	return nil
}

// Check is the question asked: did Workflow finish green on Branch for SHA?
type Check struct {
	Client   *http.Client
	API      string
	Token    string
	Repo     string
	Workflow string
	Branch   string
	SHA      string
	// Every is how long to wait before looking again while the run is still
	// queued or going.
	Every time.Duration
	// Log, when set, is told what is being waited for.
	Log io.Writer
}

// Run is one run of a workflow, as the API describes it.
type Run struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
	CreatedAt  string `json:"created_at"`
}

// Wait returns the run that makes the commit releasable.
//
// The newest run of the workflow for that commit on that branch decides: a
// run that was green and then re-run red is red. While it is still queued or
// going, Wait looks again every c.Every until ctx ends. It fails at once when
// there is no run at all, when the run finished anything but green, and when
// GitHub cannot be asked; and when ctx ends with the run still going.
func (c Check) Wait(ctx context.Context) (*Run, error) {
	var last *Run
	for {
		run, err := c.newest(ctx)
		if err != nil {
			// The wait running out in the middle of a question is the wait
			// running out, not GitHub failing: said as such, with what was
			// last seen.
			if ctx.Err() != nil && last != nil {
				return nil, c.ranOut(last)
			}
			return nil, err
		}
		last = run
		switch {
		case run == nil:
			return nil, fmt.Errorf("%s has no %s run on %s: push the commit to %s and wait for it to pass before tagging",
				short(c.SHA), c.Workflow, c.Branch, c.Branch)
		case run.Status == "completed" && run.Conclusion == "success":
			return run, nil
		case run.Status == "completed":
			return nil, fmt.Errorf("%s on %s finished %s (%s): nothing is released from a commit CI did not pass",
				c.Workflow, short(c.SHA), run.Conclusion, run.URL)
		}
		if c.Log != nil {
			fmt.Fprintf(c.Log, "%s on %s is %s (%s); looking again in %s\n", c.Workflow, short(c.SHA), run.Status, run.URL, c.Every)
		}
		select {
		case <-ctx.Done():
			return nil, c.ranOut(run)
		case <-time.After(c.Every):
		}
	}
}

// ranOut is the refusal for a wait that ended with the run still going.
func (c Check) ranOut(run *Run) error {
	return fmt.Errorf("%s on %s was still %s when the wait ran out (%s): run the release again once it is green",
		c.Workflow, short(c.SHA), run.Status, run.URL)
}

// newest is the newest run of the workflow for the commit on the branch, or
// nil when there is none.
func (c Check) newest(ctx context.Context) (*Run, error) {
	q := url.Values{"head_sha": {c.SHA}, "branch": {c.Branch}, "per_page": {"50"}}
	endpoint := fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs?%s",
		strings.TrimRight(c.API, "/"), c.Repo, url.PathEscape(c.Workflow), q.Encode())
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking GitHub about CI: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asking GitHub about CI: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var page struct {
		Runs []Run `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	var newest *Run
	for i := range page.Runs {
		r := &page.Runs[i]
		// The API filters by these already; checked again, because a
		// release must not rest on a filter that was ignored.
		if r.HeadSHA != c.SHA || r.HeadBranch != c.Branch {
			continue
		}
		if newest == nil || r.CreatedAt > newest.CreatedAt {
			newest = r
		}
	}
	return newest, nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
