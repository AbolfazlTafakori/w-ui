package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A backup past Telegram's limit is refused by Telegram with a bare 413, and
// the admin chat never learns why its backups stopped arriving. It is not
// sent, and the chat is told what to do instead.
func TestABackupTooLargeForTelegramIsNotSentButSaid(t *testing.T) {
	var uploads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendDocument") {
			uploads.Add(1)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	n := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := Config{Enabled: true, BotToken: "t", ChatID: "1", APIServer: srv.URL}
	big := &Attachment{Name: "wui-backup-20260929-120000.tar.gz", Body: strings.NewReader("x"), Size: TelegramFileLimit + 1}

	err := n.sendDocument(context.Background(), cfg, big)
	if !errors.Is(err, ErrTooLargeForTelegram) {
		t.Fatalf("an oversize backup gave %v, want ErrTooLargeForTelegram", err)
	}
	if uploads.Load() != 0 {
		t.Error("an oversize backup was uploaded anyway")
	}

	for _, lang := range []string{"en", "fa"} {
		msg := backupNotSent(lang, big, err)
		if !strings.Contains(msg, "50") || strings.ContainsAny(msg, "_*`[") {
			t.Errorf("%s: %q does not say the limit, or would break Telegram's Markdown", lang, msg)
		}
	}

	small := &Attachment{Name: "b.tar.gz", Body: strings.NewReader("x"), Size: 1}
	if err := n.sendDocument(context.Background(), cfg, small); err != nil {
		t.Fatalf("a small backup: %v", err)
	}
	if uploads.Load() != 1 {
		t.Error("a small backup was not uploaded")
	}
}
