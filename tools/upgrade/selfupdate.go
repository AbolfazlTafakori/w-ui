package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

// selfUpdateCommand presses the panel's update button, as an operator does on
// the overview page, and follows it until the panel is back on the release it
// went to: the download and signature check by the panel, the hand-over to
// the root helper (wui-update.path, wui apply-update), and the restart.
func selfUpdateCommand(args []string) error {
	fs := flag.NewFlagSet("selfupdate", flag.ContinueOnError)
	url := fs.String("url", "", "the panel, with its URL path")
	user := fs.String("user", "", "administrator")
	pass := fs.String("pass", "", "password")
	want := fs.String("want", "", "the version the panel should come back on")
	within := fs.Duration("within", 5*time.Minute, "how long the update may take")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" || *want == "" {
		return errors.New("selfupdate needs -url and -want")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *within)
	defer cancel()
	c, err := upgradecheck.Login(ctx, *url, *user, *pass)
	if err != nil {
		return err
	}
	var avail map[string]any
	if err := c.Do(ctx, http.MethodGet, "api/system/update", nil, &avail); err != nil {
		return fmt.Errorf("asking what is available: %w", err)
	}
	fmt.Printf("the panel sees: %v\n", avail)
	if err := c.Do(ctx, http.MethodPost, "api/system/update", nil, nil); err != nil {
		return fmt.Errorf("starting the update: %w", err)
	}

	last := ""
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("the panel was not back on %s within %s (last: %s)", *want, *within, last)
		case <-time.After(time.Second):
		}
		// The panel restarts on the way, and its session with it; a new
		// sign-in is what an operator's page does too.
		var p struct {
			Stage   string `json:"stage"`
			Error   string `json:"error"`
			Current string `json:"current"`
		}
		if err := c.Do(ctx, http.MethodGet, "api/system/update/progress", nil, &p); err != nil {
			if again, lerr := upgradecheck.Login(ctx, *url, *user, *pass); lerr == nil {
				c = again
			}
			continue
		}
		if s := p.Stage + " " + p.Current; s != last {
			fmt.Printf("  %s\n", s)
			last = s
		}
		if p.Stage == "failed" {
			return fmt.Errorf("the update failed: %s", p.Error)
		}
		var sys struct {
			Version string `json:"version"`
		}
		if err := c.Do(ctx, http.MethodGet, "api/system", nil, &sys); err == nil && sys.Version == *want {
			fmt.Printf("the panel is back on %s\n", sys.Version)
			return nil
		}
	}
}
