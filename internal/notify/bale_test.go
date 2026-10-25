package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Bale renders bold and italic only with a space outside each marker; what
// is written for Telegram is given one where it has none.
func TestBaleMarkdownPadsTheMarkers(t *testing.T) {
	for in, want := range map[string]string{
		"*W-UI 3.0*\nClients: 4":       "*W-UI 3.0*\nClients: 4",
		"Status:*active*now":           "Status: *active* now",
		"a *b* c":                      "a *b* c",
		"_2026-10-25 10:00:00 UTC_":    "_2026-10-25 10:00:00 UTC_",
		"at_time_here":                 "at_time_here",
		"🔒 *رمزنگاری‌شده*":             "🔒 *رمزنگاری‌شده*",
		"no markers at all, plain one": "no markers at all, plain one",
	} {
		if got := BaleMarkdown(in); got != want {
			t.Errorf("BaleMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}

// A message to Bale goes to its own API, with no parse_mode, which it does
// not take, and its text in the Markdown it renders.
func TestBaleMessageGoesToBalesAPI(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	n := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := Config{Enabled: true, BotToken: "123:abc", ChatID: "42", Platform: PlatformBale, APIServer: srv.URL}
	if err := n.post(context.Background(), c, "*Title*\nbody"); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:abc/sendMessage" {
		t.Errorf("sent to %q", path)
	}
	if _, ok := body["parse_mode"]; ok {
		t.Errorf("Bale was sent a parse_mode: %v", body["parse_mode"])
	}
	if body["text"] != "*Title*\nbody" {
		t.Errorf("text %q", body["text"])
	}

	if (Config{Platform: PlatformBale}).apiBase() != BaleAPI {
		t.Error("Bale's own API is not the default")
	}
	if (Config{}).apiBase() != "https://api.telegram.org" {
		t.Error("Telegram's default changed")
	}
}

// An event raised on the panel's notifier reaches the Bale chat as well, on
// its own settings: one that has not chosen the kind does not get it.
func TestEventsReachBaleToo(t *testing.T) {
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg, bale := New(log), New(log)
	tg.SetConfig(Config{Enabled: true, BotToken: "tg", ChatID: "1", APIServer: srv.URL, Kinds: map[string]bool{"login": true}})
	bale.SetConfig(Config{Enabled: true, BotToken: "bale", ChatID: "2", APIServer: srv.URL, Platform: PlatformBale, Kinds: map[string]bool{"login": true}})
	tg.Also(bale)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tg.Start(ctx)
	bale.Start(ctx)

	tg.Send(Event{Kind: KindLogin, Title: "Signed in"})
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case p := <-got:
			seen[strings.Split(p, "/")[1]] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("only %v received the event", seen)
		}
	}
	if !seen["bottg"] || !seen["botbale"] {
		t.Errorf("received by %v", seen)
	}

	tg.Send(Event{Kind: KindBackup, Title: "not chosen by either"})
	select {
	case p := <-got:
		t.Errorf("a kind nobody chose was sent to %s", p)
	case <-time.After(1500 * time.Millisecond):
	}
}
