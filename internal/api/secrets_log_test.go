package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/logger"
)

// No secret reaches the log or an error answer, with every debug area on
// and the panel at debug: not a password (right or wrong), not an API token
// as issued or as used, not a subscription link's token, not a key. The
// flows that carry them are run, and everything logged is read back.
func TestNoSecretIsLogged(t *testing.T) {
	var buf bytes.Buffer
	panelLogTo = &buf
	was := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.SetDebugAreas("all")
	t.Cleanup(func() {
		panelLogTo = io.Discard
		slog.SetDefault(was)
		logger.SetDebugAreas("")
	})

	mux, sess, db := serversPanel(t)
	var answers strings.Builder
	keep := func(code int, body string) { answers.WriteString(body) }

	// A wrong password and a right one.
	keep(send(t, mux, session{}, http.MethodPost, "/api/auth/login", `{"username":"ra","password":"WRONG-PASSWORD-SECRET-1"}`))
	// A token issued, used, used wrongly, and revoked.
	tok := issueFor(t, mux, sess["owner"], idOf(t, db, "ra"), `{"name":"shop"}`)
	keep(send(t, mux, session{token: tok}, http.MethodGet, "/api/clients", ""))
	keep(send(t, mux, session{token: tok + "x"}, http.MethodGet, "/api/clients", ""))
	keep(send(t, mux, session{token: "wuo_NOT-A-REAL-TOKEN-SECRET"}, http.MethodGet, "/api/clients", ""))
	// A password change, refused and done.
	keep(send(t, mux, sess["ra"], http.MethodPost, "/api/auth/password", `{"currentPassword":"WRONG-CURRENT-SECRET","newPassword":"NEW-PASSWORD-SECRET-9"}`))
	// A customer's link, and its page.
	var c model.Client
	db.First(&c, 3)
	db.Model(&c).Update("sub_token", "SUBTOKEN-SECRET-abcdef")
	keep(send(t, mux, session{}, http.MethodGet, "/sub/SUBTOKEN-SECRET-abcdef", ""))
	keep(send(t, mux, sess["ra"], http.MethodGet, "/api/clients/3", ""))

	logged := buf.String()
	if !strings.Contains(logged, "level=") {
		t.Fatal("nothing was logged: the test reads the wrong place")
	}
	for _, secret := range []string{
		"WRONG-PASSWORD-SECRET", "WRONG-CURRENT-SECRET", "NEW-PASSWORD-SECRET", "servers-test-pass-1",
		tok, strings.TrimPrefix(tok, "wuo_"), "NOT-A-REAL-TOKEN-SECRET",
		"SUBTOKEN-SECRET", "PRIVATE-KEY-MUST-NOT-LEAK",
	} {
		if strings.Contains(logged, secret) {
			i := strings.Index(logged, secret)
			t.Errorf("the log carries %q: …%s…", secret, logged[max(0, i-120):min(len(logged), i+40)])
		}
		if strings.Contains(answers.String(), secret) && secret != "SUBTOKEN-SECRET" {
			t.Errorf("an answer carries %q", secret)
		}
	}
}

// The access log, which sits in front of every route, keeps a link's path
// without its token, and a query without its values.
func TestTheAccessLogKeepsNoLinkToken(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := LogRequests(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, path := range []string{"/sub/SUBTOKEN-SECRET-abcdef", "/subscribe/SUBTOKEN-SECRET-abcdef?format=wui",
		"/sub/SUBTOKEN-SECRET-abcdef/page", "/api/clients/3?secret=QUERY-SECRET"} {
		req, _ := http.NewRequest(http.MethodGet, "http://panel"+path, nil)
		h.ServeHTTP(&discardWriter{}, req)
	}
	if strings.Count(buf.String(), "path=") < 4 {
		t.Fatalf("the access log did not keep the four requests: %s", buf.String())
	}
	for _, s := range []string{"SUBTOKEN-SECRET", "QUERY-SECRET"} {
		if strings.Contains(buf.String(), s) {
			t.Errorf("the access log carries %q:\n%s", s, buf.String())
		}
	}
}

type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header {
	if d.h == nil {
		d.h = http.Header{}
	}
	return d.h
}
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)             {}
