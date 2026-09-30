package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The automatic backup: the database, sent to the chat as a file by the bot
// itself, on a schedule of its own.
//
// Where that schedule counts from is kept in the database, not in memory. A
// panel restarted by every update would otherwise start its "every 24 hours"
// over each time and, updated more often than that, never send one; and a
// backup that fell due while the panel was down is sent once it is back,
// rather than skipped until the next.

// Attachment is a file sent to the chat.
type Attachment struct {
	Name string
	// Body is the file. When it is also an io.Closer it is closed once the
	// upload is over, whether or not the file was sent.
	Body io.Reader
	// Size is the file's length, when known; one past TelegramFileLimit is
	// not sent, and the chat is told instead.
	Size int64
	// Caption is the line shown under the file, as plain text.
	Caption string
}

// TelegramFileLimit is the largest file a bot may send. Past it Telegram
// answers 413 and the file never arrives.
const TelegramFileLimit = 50 << 20

// ErrTooLargeForTelegram is a file past TelegramFileLimit.
var ErrTooLargeForTelegram = errors.New("larger than the 50 MB Telegram accepts from a bot")

// BackupFunc takes a backup to send: the archive, open for reading, with its
// caption in lang.
type BackupFunc func(ctx context.Context, lang string) (*Attachment, error)

// BackupStamp remembers when the automatic backup last went, and on which
// schedule. The next is due one period after it.
type BackupStamp interface {
	LastBackup(ctx context.Context) (at time.Time, schedule string, err error)
	SetLastBackup(ctx context.Context, at time.Time, schedule string) error
}

// MinBackupGap is the least time allowed between two automatic backups. Each
// one is the whole database, written to disk and uploaded; more often than
// this is load on the server and noise in the chat, and no safer.
const MinBackupGap = 10 * time.Minute

// backupCheckEvery is the longest the loop sleeps, so that a schedule changed
// on the settings page is taken up within it.
const backupCheckEvery = time.Minute

// CheckBackupSchedule refuses a schedule the automatic backup cannot keep:
// one that does not parse, one that would send the database more often than
// every MinBackupGap, and one that never comes round.
func CheckBackupSchedule(s string) error {
	sc, err := ParseSchedule(s)
	if err != nil {
		return err
	}
	tooOften := fmt.Errorf("%q is more often than a backup may be taken, once every %d minutes", s, int(MinBackupGap.Minutes()))
	if sc.every > 0 {
		if sc.every < MinBackupGap {
			return tooOften
		}
		return nil
	}
	// Any moment will do to start from. A schedule that fires at all fires
	// within a year of it; Next gives up at a year and a minute.
	from := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	prev := sc.Next(from)
	if prev.Sub(from) > 365*24*time.Hour {
		return fmt.Errorf("%q does not come round within a year", s)
	}
	// Two days of it show the shortest gap of any daily or hourly pattern.
	for end := prev.Add(48 * time.Hour); prev.Before(end); {
		next := sc.Next(prev)
		if next.Sub(prev) < MinBackupGap {
			return tooOften
		}
		prev = next
	}
	return nil
}

// backupSchedule is when the automatic backup goes; empty while it is off,
// or while the bot has nowhere to send it.
func (c Config) backupSchedule() string {
	if !c.ready() || !c.Backup {
		return ""
	}
	return strings.TrimSpace(c.BackupTime)
}

// RunBackups sends the automatic backup on its schedule until ctx ends. The
// settings are re-read every minute, so a change takes effect without a
// restart.
func (n *Notifier) RunBackups(ctx context.Context, take BackupFunc, stamp BackupStamp) {
	go func() {
		for {
			wait := n.backupStep(ctx, take, stamp, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

// backupStep sends the backup when it is due at now, and says how long to
// sleep before looking again.
func (n *Notifier) backupStep(ctx context.Context, take BackupFunc, stamp BackupStamp, now time.Time) time.Duration {
	cfg := n.Config()
	schedule := cfg.backupSchedule()
	last, was, err := stamp.LastBackup(ctx)
	if err != nil {
		n.log.Warn("could not read when the last automatic backup went", "error", err)
		return backupCheckEvery
	}
	if schedule == "" {
		// Off. What it counted from is forgotten, so that switching it on
		// again starts the count then -- not with a backup at once for all
		// the time it was off.
		if was != "" {
			n.stampBackup(ctx, stamp, time.Time{}, "")
		}
		return backupCheckEvery
	}
	sc, err := ParseSchedule(schedule)
	if err != nil {
		// Refused on save; a value from before that check is left alone.
		return backupCheckEvery
	}
	if was != schedule || last.IsZero() || last.After(now) {
		// Just switched on, or given another time: the count starts now. A
		// time ahead of now is a clock set back, and counts from now too.
		n.stampBackup(ctx, stamp, now, schedule)
		last = now
	}
	// Read in the panel's zone: kept in the database in UTC, "every day at
	// three" would otherwise be three UTC.
	due := sc.Next(last.In(now.Location()))
	if now.Before(due) {
		return min(backupCheckEvery, due.Sub(now))
	}

	// Due, or overdue by the time the panel was down. The count moves on
	// whether or not the file arrived: a failure is said in the chat, and
	// trying again every minute would only say it again.
	n.sendBackup(ctx, cfg, take)
	n.stampBackup(ctx, stamp, now, schedule)
	return min(backupCheckEvery, sc.Next(now).Sub(now))
}

func (n *Notifier) stampBackup(ctx context.Context, stamp BackupStamp, at time.Time, schedule string) {
	if err := stamp.SetLastBackup(ctx, at, schedule); err != nil {
		n.log.Warn("could not record when the automatic backup went", "error", err)
	}
}

// sendBackup takes a backup and sends it, telling the chat when it cannot:
// an admin who counts on these files must not find out they stopped on the
// day one is needed.
func (n *Notifier) sendBackup(ctx context.Context, cfg Config, take BackupFunc) {
	file, err := take(ctx, cfg.Lang)
	if err != nil {
		n.log.Warn("could not take the automatic backup", "error", err)
		_ = n.post(ctx, cfg, backupNotTaken(cfg.Lang, err))
		return
	}
	if err := n.sendDocument(ctx, cfg, file); err != nil {
		n.log.Warn("could not send the automatic backup", "file", file.Name, "error", err)
		_ = n.post(ctx, cfg, backupNotSent(cfg.Lang, file, err))
		return
	}
	n.log.Info("automatic backup sent to Telegram", "file", file.Name, "bytes", file.Size)
}

// sendDocument uploads a file to the chat.
func (n *Notifier) sendDocument(ctx context.Context, c Config, a *Attachment) error {
	if cl, ok := a.Body.(io.Closer); ok {
		defer cl.Close()
	}
	if a.Size > TelegramFileLimit {
		return ErrTooLargeForTelegram
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", c.ChatID)
	if a.Caption != "" {
		_ = mw.WriteField("caption", a.Caption)
	}
	fw, err := mw.CreateFormFile("document", a.Name)
	if err != nil {
		return fmt.Errorf("notify: build upload: %w", err)
	}
	if _, err := io.Copy(fw, a.Body); err != nil {
		return fmt.Errorf("notify: read attachment: %w", err)
	}
	mw.Close()

	endpoint := fmt.Sprintf("%s/bot%s/sendDocument", c.apiBase(), url.PathEscape(c.BotToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("notify: upload: %w", err)
	}
	defer resp.Body.Close()
	return telegramRefused(resp)
}

// telegramRefused is nil for an answer Telegram accepted, and otherwise says
// why it did not: its own description says far better than the status code
// what is wrong -- a wrong chat id and a revoked token both give 400.
func telegramRefused(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var reply struct {
		Description string `json:"description"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	if reply.Description != "" {
		return fmt.Errorf("notify: telegram refused it: %s", reply.Description)
	}
	return fmt.Errorf("notify: telegram returned %s", resp.Status)
}

// backupNotTaken is the chat's line for a backup that could not be made.
func backupNotTaken(lang string, err error) string {
	if lang == "fa" {
		return "⚠️ بکاپ خودکار گرفته نشد: " + plain(err.Error())
	}
	return "⚠️ The automatic backup could not be taken: " + plain(err.Error())
}

// backupNotSent is the chat's line for a backup that was made but not sent.
func backupNotSent(lang string, a *Attachment, err error) string {
	mb := float64(a.Size) / (1 << 20)
	if errors.Is(err, ErrTooLargeForTelegram) {
		if lang == "fa" {
			return fmt.Sprintf("⚠️ بکاپ %s (%.1f MB) از سقف ۵۰ مگابایتی تلگرام بزرگ‌تر است و فرستاده نشد. از پنل دانلودش کنید: نمای کلی ← بکاپ و بازگردانی.", plain(a.Name), mb)
		}
		return fmt.Sprintf("⚠️ The backup %s (%.1f MB) is larger than the 50 MB Telegram accepts, so it was not sent. Download it from the panel: Overview -> Backup & Restore.", plain(a.Name), mb)
	}
	if lang == "fa" {
		return fmt.Sprintf("⚠️ بکاپ %s فرستاده نشد: %s", plain(a.Name), plain(err.Error()))
	}
	return fmt.Sprintf("⚠️ The backup %s could not be sent: %s", plain(a.Name), plain(err.Error()))
}

// plain keeps text from being read as Markdown, which the chat is sent in: an
// underscore in a file name or an error would otherwise make Telegram refuse
// the whole message.
func plain(s string) string {
	return strings.NewReplacer("_", " ", "*", " ", "`", "'", "[", "(", "]", ")").Replace(s)
}
