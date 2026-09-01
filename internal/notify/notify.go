// Package notify sends the operator a message when something happens that they
// would otherwise only find out about from a customer.
//
// The panel is not watched. A quota running out at three in the morning, a
// certificate expiring, an interface that stopped coming up — all of these are
// invisible until someone complains. This turns them into a message.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Event is something worth telling the operator about.
type Event struct {
	Kind  Kind
	Title string
	Body  string
	// At is when it happened, not when it is sent.
	At time.Time
}

// Kind groups events so an operator can choose which ones reach them.
type Kind string

const (
	KindExhausted Kind = "exhausted" // a customer used up their allowance
	KindExpired   Kind = "expired"   // a customer's time ran out
	KindExpiring  Kind = "expiring"  // a customer is close to expiring
	KindSharing   Kind = "sharing"   // one credential in several places at once
	KindLogin     Kind = "login"     // someone signed in to the panel
	KindPanel     Kind = "panel"     // the panel started, stopped, or degraded
	KindBackup    Kind = "backup"    // a backup was taken
)

// AllKinds is every event, in the order the settings page lists them.
func AllKinds() []Kind {
	return []Kind{KindExhausted, KindExpired, KindExpiring, KindSharing,
		KindLogin, KindPanel, KindBackup}
}

// Config is what the settings page stores.
type Config struct {
	Enabled  bool            `json:"enabled"`
	BotToken string          `json:"botToken"`
	ChatID   string          `json:"chatId"`
	Kinds    map[string]bool `json:"kinds"`
}

// Wants reports whether this kind should be sent.
func (c Config) Wants(k Kind) bool {
	if !c.Enabled || c.BotToken == "" || c.ChatID == "" {
		return false
	}
	if len(c.Kinds) == 0 {
		return true // nothing chosen yet means everything, not nothing
	}
	return c.Kinds[string(k)]
}

const (
	// queueSize bounds how many messages may wait. Telegram is slow and
	// sometimes unreachable; the panel must not slow down or grow without
	// limit because of it.
	queueSize = 128

	// sendTimeout bounds one delivery attempt.
	sendTimeout = 15 * time.Second

	// minInterval keeps the panel under Telegram's per-chat rate limit, which
	// is roughly one message a second.
	minInterval = 1200 * time.Millisecond
)

// Notifier delivers events without ever making the caller wait.
type Notifier struct {
	log    *slog.Logger
	client *http.Client

	mu  sync.RWMutex
	cfg Config

	queue chan Event

	// dropped counts events discarded because the queue was full, so the
	// silence is at least visible in the logs rather than total.
	droppedMu sync.Mutex
	dropped   uint64
}

// New builds a notifier. It does nothing until Start is called.
func New(log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	return &Notifier{
		log:    log,
		client: &http.Client{Timeout: sendTimeout},
		queue:  make(chan Event, queueSize),
	}
}

// SetConfig replaces the delivery settings. Safe to call while running.
func (n *Notifier) SetConfig(c Config) {
	n.mu.Lock()
	n.cfg = c
	n.mu.Unlock()
}

// Config returns the current settings.
func (n *Notifier) Config() Config {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.cfg
}

// Send queues an event. It never blocks and never returns an error: a
// notification failing must not fail the thing being notified about.
func (n *Notifier) Send(e Event) {
	if !n.Config().Wants(e.Kind) {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}

	select {
	case n.queue <- e:
	default:
		// Full. Dropping the newest is the right way round: the older ones
		// are already in order and closer to being delivered.
		n.droppedMu.Lock()
		n.dropped++
		count := n.dropped
		n.droppedMu.Unlock()
		if count == 1 || count%50 == 0 {
			n.log.Warn("notifications are being dropped; the queue is full", "dropped", count)
		}
	}
}

// Start delivers queued events until the context ends.
func (n *Notifier) Start(ctx context.Context) {
	go func() {
		// One at a time and paced, because the limit is per chat and a burst
		// gets the whole bot throttled rather than the one message delayed.
		ticker := time.NewTicker(minInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case e := <-n.queue:
				if err := n.deliver(ctx, e); err != nil {
					n.log.Warn("could not deliver a notification",
						"kind", e.Kind, "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}
	}()
}

// Test sends one message immediately and reports what happened, so the settings
// page can tell an operator whether their token works instead of leaving them
// to wonder.
func (n *Notifier) Test(ctx context.Context, c Config) error {
	if c.BotToken == "" || c.ChatID == "" {
		return fmt.Errorf("notify: a bot token and a chat id are both required")
	}
	return n.post(ctx, c, "*W-UI*\nNotifications are working.")
}

func (n *Notifier) deliver(ctx context.Context, e Event) error {
	return n.post(ctx, n.Config(), format(e))
}

// format renders an event for Telegram's markdown.
func format(e Event) string {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(escape(e.Title))
	b.WriteString("*\n")
	if e.Body != "" {
		b.WriteString(escape(e.Body))
		b.WriteString("\n")
	}
	b.WriteString("_")
	b.WriteString(e.At.Format("2006-01-02 15:04:05 MST"))
	b.WriteString("_")
	return b.String()
}

// escape neutralises the characters Telegram's legacy markdown treats as
// formatting. A customer named with an underscore would otherwise either break
// the message or silently italicise half of it.
func escape(s string) string {
	return strings.NewReplacer("_", " ", "*", " ", "`", "'", "[", "(", "]", ")").Replace(s)
}

func (n *Notifier) post(ctx context.Context, c Config, text string) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"chat_id":                  c.ChatID,
		"text":                     text,
		"parse_mode":               "Markdown",
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("notify: encode message: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage",
		url.PathEscape(c.BotToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var reply struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&reply)
		// Telegram's own description says what is wrong far better than the
		// status code — a wrong chat id and a revoked token both give 400.
		if reply.Description != "" {
			return fmt.Errorf("notify: telegram refused it: %s", reply.Description)
		}
		return fmt.Errorf("notify: telegram returned %s", resp.Status)
	}
	return nil
}
