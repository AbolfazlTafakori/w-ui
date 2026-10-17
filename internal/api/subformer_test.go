package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/service"
)

// A link a customer was handed keeps working when the service moves: the
// panel's own port answers it as the service's does, and so does a path the
// service had before. Found on a live panel, where links handed out while
// the service was on the panel's port, or on another, answered 404.

// get fetches as a customer's app does. Not a proxy app: the customer here
// has an L2TP file alone, which a proxy app is told it has nothing for.
func get(router http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", "WireGuard/1.1.0")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestThePanelsPortAnswersALinkWithTheServiceOnItsOwn(t *testing.T) {
	s, _ := revealServer(t)
	s.subMiss = newSubMisses()
	for k, v := range map[string]string{"sub.enabled": "true", "sub.path": "/subscribe/", "sub.port": "44600"} {
		if err := database.PutSetting(dbOf(s), k, v); err != nil {
			t.Fatal(err)
		}
	}
	panel := s.SubscriptionRouter(http.NotFoundHandler())
	if rec := get(panel, "/subscribe/"+revealToken); rec.Code != http.StatusOK {
		t.Fatalf("a customer's link on the panel's port: %d %s", rec.Code, rec.Body.String())
	}
	// And still nothing for a token that is not one.
	if rec := get(panel, "/subscribe/not-a-token-at-all"); rec.Code != http.StatusNotFound {
		t.Fatalf("a made-up link: %d", rec.Code)
	}
}

func TestAPathTheServiceHadBeforeStillAnswers(t *testing.T) {
	s, _ := revealServer(t)
	s.subMiss = newSubMisses()
	for k, v := range map[string]string{"sub.enabled": "true", "sub.path": "/sub/"} {
		if err := database.PutSetting(dbOf(s), k, v); err != nil {
			t.Fatal(err)
		}
	}
	ctx := t.Context()
	cfg, err := s.subs.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Path = "/feed/" // moved, as an operator would on the settings page
	if _, err := s.subs.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	router := s.SubscriptionRouter(http.NotFoundHandler())
	for _, path := range []string{"/feed/", "/sub/"} {
		if rec := get(router, path+revealToken); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	// Closed by the operator: the old path answers no more.
	cfg, _ = s.subs.Settings(ctx)
	cfg.Former = nil
	if _, err := s.subs.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if rec := get(router, "/sub/"+revealToken); rec.Code != http.StatusNotFound {
		t.Fatalf("a closed path still answers: %d", rec.Code)
	}
}

// A page or file that cannot be made for a reason of this server's is said
// to be so -- answered 503, and logged -- not passed off as a link that
// does not exist.
func TestAServersFailureOnAPageIsNotANotFound(t *testing.T) {
	var logged strings.Builder
	s := &Server{log: slog.New(slog.NewTextHandler(&logged, nil))}
	for _, c := range []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: no such subscription", service.ErrNotFound), http.StatusNotFound},
		{fmt.Errorf("%w: no devices", service.ErrInvalid), http.StatusNotFound},
		{errors.New("database is locked"), http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		s.subFailed(rec, httptest.NewRequest(http.MethodGet, "/sub/tok", nil), c.err)
		if rec.Code != c.want {
			t.Errorf("%v: %d, want %d", c.err, rec.Code, c.want)
		}
	}
	if !strings.Contains(logged.String(), "database is locked") || strings.Contains(logged.String(), "tok") {
		t.Fatalf("logged %q: the failure, and never the token", logged.String())
	}
}
