//go:build linux

package logger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeJournal puts a journalctl on PATH that prints lines as journald would:
// one JSON object each, the panel's own text line as MESSAGE. stderr is what
// the real one says on stderr, for the case where it may not read.
func fakeJournal(t *testing.T, messages []string, stderr string) {
	t.Helper()
	dir := t.TempDir()
	var out strings.Builder
	for i, m := range messages {
		row, _ := json.Marshal(map[string]string{
			"MESSAGE":              m,
			"PRIORITY":             "6",
			"__REALTIME_TIMESTAMP": fmt.Sprint(time.Date(2026, 9, 5, 5, 0, i, 0, time.UTC).UnixMicro()),
		})
		out.Write(row)
		out.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "lines"), []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + filepath.Join(dir, "lines") + "\n"
	if stderr != "" {
		script += "echo '" + stderr + "' >&2\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// panelLine is a line exactly as the panel's own handler writes it.
func panelLine(msg string, args ...any) string {
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Info(msg, args...)
	return strings.TrimSpace(b.String())
}

// The lines asked for come back apart, fields and all; the rest are passed
// over, and so is a line that only mentions the message.
func TestTheJournalIsScannedForTheLinesAskedFor(t *testing.T) {
	fakeJournal(t, []string{
		panelLine("connection limit reached; device held off",
			"client", "Ali / Home", "device", "Phone", "on", "here", "limit", 1, "until", "5:21AM"),
		panelLine("device connected", "device", "Ali / Home / Phone", "from", "203.0.113.7"),
		panelLine("rate limits are being applied again"),
		panelLine("request failed", "error", "device connected"),
		"a line systemd wrote about device connected",
	}, "")
	var got []Entry
	err := ScanJournal(context.Background(), time.Now().Add(-time.Hour),
		[]string{"connection limit reached; device held off", "device connected"},
		func(e Entry) { got = append(got, e) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries %+v, want the hold and the connection", len(got), got)
	}
	hold, conn := got[0], got[1]
	if hold.Fields["client"] != "Ali / Home" || hold.Fields["device"] != "Phone" || hold.Fields["limit"] != "1" || hold.Fields["on"] != "here" {
		t.Fatalf("hold fields %+v", hold.Fields)
	}
	if conn.Fields["device"] != "Ali / Home / Phone" || conn.Fields["from"] != "203.0.113.7" {
		t.Fatalf("connection fields %+v", conn.Fields)
	}
	if hold.Time.IsZero() || conn.Time.Before(hold.Time) {
		t.Fatalf("times %v %v, want each line's own, oldest first", hold.Time, conn.Time)
	}
}

// Not allowed to read the journal is said as that, not as an empty log.
func TestAJournalThatMayNotBeReadSaysSo(t *testing.T) {
	fakeJournal(t, nil, "Hint: You are currently not seeing messages from other users and the system. insufficient permissions")
	err := ScanJournal(context.Background(), time.Now(), []string{"x"}, func(Entry) {})
	if !errors.Is(err, ErrJournalDenied) {
		t.Fatalf("err %v, want ErrJournalDenied", err)
	}
}
