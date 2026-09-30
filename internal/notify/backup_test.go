package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram answers as the Bot API does and remembers what it was sent.
type fakeTelegram struct {
	mu       sync.Mutex
	files    []string // the caption of each document
	messages []string
}

func (f *fakeTelegram) start(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendDocument"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("an upload that is not a form: %v", err)
			}
			f.files = append(f.files, r.FormValue("caption"))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			body, _ := io.ReadAll(r.Body)
			f.messages = append(f.messages, string(body))
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakeTelegram) counts() (files, messages int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files), len(f.messages)
}

// memStamp is the BackupStamp the database keeps, in memory.
type memStamp struct {
	at       time.Time
	schedule string
}

func (m *memStamp) LastBackup(context.Context) (time.Time, string, error) {
	return m.at, m.schedule, nil
}

func (m *memStamp) SetLastBackup(_ context.Context, at time.Time, schedule string) error {
	// In UTC, as the database gives it back.
	m.at, m.schedule = at.UTC(), schedule
	return nil
}

// closeCounter is a backup file that knows whether it was closed.
type closeCounter struct {
	io.Reader
	closed *int
}

func (c closeCounter) Close() error { *c.closed++; return nil }

type backupRig struct {
	n      *Notifier
	tg     *fakeTelegram
	stamp  *memStamp
	taken  int
	closed int
	fail   error
	size   int64
}

func newBackupRig(t *testing.T, schedule string) *backupRig {
	r := &backupRig{tg: &fakeTelegram{}, stamp: &memStamp{}, size: 3}
	r.n = New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.n.SetConfig(Config{Enabled: true, BotToken: "t", ChatID: "1", Lang: "en", APIServer: r.tg.start(t),
		Backup: true, BackupTime: schedule})
	return r
}

func (r *backupRig) take(ctx context.Context, lang string) (*Attachment, error) {
	r.taken++
	if r.fail != nil {
		return nil, r.fail
	}
	return &Attachment{Name: "wui-backup.tar.gz", Body: closeCounter{strings.NewReader("abc"), &r.closed},
		Size: r.size, Caption: "W-UI backup " + lang}, nil
}

func (r *backupRig) at(now time.Time) time.Duration {
	return r.n.backupStep(context.Background(), r.take, r.stamp, now)
}

func (r *backupRig) sent(t *testing.T, want int, when string) {
	t.Helper()
	if got, _ := r.tg.counts(); got != want {
		t.Fatalf("%s: %d backups sent, want %d", when, got, want)
	}
}

// tehran is a zone well away from UTC, as a panel's usually is: a time of day
// read in the wrong one is hours out.
var tehran = time.FixedZone("IRST", 3*3600+1800)

func day(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, tehran) }

// Set to go every day at three, the bot sends the file at three -- not
// before, not twice, and again the next day.
func TestTheAutomaticBackupGoesAtItsTime(t *testing.T) {
	r := newBackupRig(t, "0 0 3 * * *")

	if wait := r.at(day(29, 22, 0)); wait <= 0 || wait > backupCheckEvery {
		t.Errorf("switched on, it sleeps %s", wait)
	}
	r.at(day(30, 2, 59))
	r.sent(t, 0, "before three")

	r.at(day(30, 3, 0))
	r.sent(t, 1, "at three")
	if r.tg.files[0] != "W-UI backup en" {
		t.Errorf("the file went with the caption %q", r.tg.files[0])
	}
	if r.closed != 1 {
		t.Errorf("the archive was closed %d times after the upload, want once", r.closed)
	}

	r.at(day(30, 3, 1))
	r.at(day(30, 23, 59))
	r.sent(t, 1, "later the same day")
	r.at(day(31, 3, 0))
	r.sent(t, 2, "at three the next day")
}

// Where the schedule counts from outlives the panel. Every six hours is every
// six hours across a restart -- before, an update every few hours meant a
// backup never came -- and one that fell due while the panel was down goes
// as soon as it is back, once.
func TestTheScheduleOutlivesARestart(t *testing.T) {
	r := newBackupRig(t, "@every 6h")
	r.at(day(30, 0, 0))

	// A restart: a new notifier, the same database.
	restarted := newBackupRig(t, "@every 6h")
	restarted.stamp = r.stamp
	restarted.at(day(30, 5, 0))
	restarted.sent(t, 0, "five hours in, after a restart")
	restarted.at(day(30, 6, 0))
	restarted.sent(t, 1, "six hours in, after a restart")

	// Down from before the next one was due until well after.
	back := newBackupRig(t, "@every 6h")
	back.stamp = restarted.stamp
	back.at(day(30, 20, 0))
	back.sent(t, 1, "back up after missing one")
	back.at(day(30, 20, 1))
	back.sent(t, 1, "a minute after the missed one went")
}

// Switched on, or given another time, the count starts then: no backup at
// once for all the time it was off, or for a time that no longer applies.
func TestSwitchingOnOrRetimingDoesNotSendAtOnce(t *testing.T) {
	r := newBackupRig(t, "0 0 3 * * *")
	r.stamp.at, r.stamp.schedule = day(20, 3, 0).UTC(), "" // last went ten days ago, then was switched off
	r.at(day(30, 10, 0))
	r.sent(t, 0, "switched on after ten days off")

	r.at(day(30, 11, 0))
	cfg := r.n.Config()
	cfg.BackupTime = "0 0 4 * * *"
	r.n.SetConfig(cfg)
	r.at(day(30, 11, 1))
	r.sent(t, 0, "given another time")
	r.at(day(31, 3, 0))
	r.sent(t, 0, "at the old time")
	r.at(day(31, 4, 0))
	r.sent(t, 1, "at the new time")
}

// Off -- the switch, or the bot itself -- nothing goes, and what the schedule
// counted from is forgotten.
func TestOffSendsNothing(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"backup off": func(c *Config) { c.Backup = false },
		"bot off":    func(c *Config) { c.Enabled = false },
		"no time":    func(c *Config) { c.BackupTime = "" },
	} {
		r := newBackupRig(t, "@every 1h")
		r.at(day(30, 0, 0))
		cfg := r.n.Config()
		change(&cfg)
		r.n.SetConfig(cfg)
		r.at(day(30, 5, 0))
		r.sent(t, 0, name)
		if r.taken != 0 {
			t.Errorf("%s: a backup was taken", name)
		}
		if r.stamp.schedule != "" || !r.stamp.at.IsZero() {
			t.Errorf("%s: the stamp was kept: %+v", name, r.stamp)
		}
	}
}

// A backup that cannot be made, or cannot be sent, is said in the chat -- and
// not said again every minute until the next one is due.
func TestAFailedBackupIsSaidOnce(t *testing.T) {
	r := newBackupRig(t, "0 0 3 * * *")
	r.fail = errors.New("disk full")
	r.at(day(29, 22, 0))
	r.at(day(30, 3, 0))
	r.at(day(30, 3, 1))
	r.at(day(30, 3, 2))
	if files, msgs := r.tg.counts(); files != 0 || msgs != 1 {
		t.Fatalf("a failing backup: %d files and %d messages, want 0 and 1", files, msgs)
	}
	if !strings.Contains(r.tg.messages[0], "disk full") {
		t.Errorf("the chat was not told why: %s", r.tg.messages[0])
	}

	big := newBackupRig(t, "0 0 3 * * *")
	big.size = TelegramFileLimit + 1
	big.at(day(29, 22, 0))
	big.at(day(30, 3, 0))
	if files, msgs := big.tg.counts(); files != 0 || msgs != 1 {
		t.Fatalf("an oversize backup: %d files and %d messages, want 0 and 1", files, msgs)
	}
	if big.closed != 1 {
		t.Errorf("an archive too large to send was left open (closed %d times)", big.closed)
	}
}

// Refused on save: a schedule that does not parse, one more often than a
// backup may go, and one that never comes round.
func TestCheckBackupSchedule(t *testing.T) {
	for s, ok := range map[string]bool{
		"@daily":         true,
		"@hourly":        true,
		"@weekly":        true,
		"@every 6h":      true,
		"@every 10m":     true,
		"0 30 3 * * *":   true,
		"0 3 * * 1":      true,
		"*/10 * * * *":   true,
		"0 0 1 1 *":      true,
		"@every 5m":      false,
		"*/5 * * * *":    false,
		"* * * * *":      false,
		"0,5 3 * * *":    false,
		"0 0 30 2 *":     false,
		"at three":       false,
		"0 0 25 * * * *": false,
		"0 0 3 * * 7":    false,
	} {
		if err := CheckBackupSchedule(s); (err == nil) != ok {
			t.Errorf("CheckBackupSchedule(%q) = %v, want ok %v", s, err, ok)
		}
	}
}
