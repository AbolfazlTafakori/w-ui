package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Attachment is a file sent along with a message.
type Attachment struct {
	Name string
	Body io.Reader
	// Size is the file's length, when known; one past TelegramFileLimit is
	// not sent, and the chat is told instead.
	Size int64
}

// TelegramFileLimit is the largest file a bot may send. Past it Telegram
// answers 413 and the file never arrives.
const TelegramFileLimit = 50 << 20

// ErrTooLargeForTelegram is a file past TelegramFileLimit.
var ErrTooLargeForTelegram = errors.New("larger than the 50 MB Telegram accepts from a bot")

// ReportFunc builds the periodic report: the text, and the database archive
// when the settings ask for it. Called on the schedule, never concurrently.
type ReportFunc func(ctx context.Context, lang string, withBackup bool) (text string, file *Attachment, err error)

// RunReports sends the periodic report on the configured schedule until ctx
// ends. The schedule is re-read every few minutes, so a change on the
// settings page takes effect without a restart.
func (n *Notifier) RunReports(ctx context.Context, build ReportFunc) {
	go func() {
		var due time.Time
		lastRunTime := ""
		for {
			cfg := n.Config()
			if cfg.RunTime != lastRunTime || due.IsZero() {
				lastRunTime = cfg.RunTime
				due = time.Time{}
				if sc, err := ParseSchedule(cfg.RunTime); err == nil && cfg.RunTime != "" {
					due = sc.Next(time.Now())
				}
			}
			wait := 5 * time.Minute
			if !due.IsZero() && time.Until(due) < wait {
				wait = time.Until(due)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if due.IsZero() || time.Now().Before(due) {
				continue
			}
			due = time.Time{} // recomputed at the top
			cfg = n.Config()
			if !cfg.ready() {
				continue
			}
			text, file, err := build(ctx, cfg.Lang, cfg.Backup)
			if err != nil {
				n.log.Warn("could not build the periodic report", "error", err)
				continue
			}
			if err := n.post(ctx, cfg, text); err != nil {
				n.log.Warn("could not send the periodic report", "error", err)
			}
			if file != nil {
				if err := n.sendDocument(ctx, cfg, file); err != nil {
					n.log.Warn("could not send the backup with the report", "error", err)
					// Said in the chat as well: an admin who counts on these
					// files otherwise finds out the day one is needed.
					_ = n.post(ctx, cfg, backupNotSent(cfg.Lang, file, err))
				}
			}
		}
	}()
}

// sendDocument uploads a file to the chat.
func (n *Notifier) sendDocument(ctx context.Context, c Config, a *Attachment) error {
	if a.Size > TelegramFileLimit {
		return ErrTooLargeForTelegram
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", c.ChatID)
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notify: telegram returned %s", resp.Status)
	}
	return nil
}

// backupNotSent is the chat's line for a backup that did not go with the report.
func backupNotSent(lang string, a *Attachment, err error) string {
	mb := float64(a.Size) / (1 << 20)
	if errors.Is(err, ErrTooLargeForTelegram) {
		if lang == "fa" {
			return fmt.Sprintf("بکاپ %s (%.1f MB) از سقف ۵۰ مگابایتی تلگرام بزرگ‌تر است و فرستاده نشد. از پنل دانلودش کنید: نمای کلی ← بکاپ و بازگردانی.", plain(a.Name), mb)
		}
		return fmt.Sprintf("The backup %s (%.1f MB) is larger than the 50 MB Telegram accepts, so it was not sent. Download it from the panel: Overview -> Backup & Restore.", plain(a.Name), mb)
	}
	if lang == "fa" {
		return fmt.Sprintf("بکاپ %s فرستاده نشد: %s", plain(a.Name), plain(err.Error()))
	}
	return fmt.Sprintf("The backup %s could not be sent: %s", plain(a.Name), plain(err.Error()))
}

// plain keeps text from being read as Markdown, which the chat is sent in: an
// underscore in a file name or an error would otherwise make Telegram refuse
// the whole message.
func plain(s string) string {
	return strings.NewReplacer("_", " ", "*", " ", "`", "'", "[", "(", "]", ")").Replace(s)
}
