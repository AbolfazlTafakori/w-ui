package tgbot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/sysinfo"
)

// The bot answers whoever writes to it, so what matters most is who it does
// what for: the administrators named in the settings, and each customer for
// their own plan only.

const adminID, customerID, strangerID = 1001, 2002, 3003

// telegram is a stand-in for Telegram's API: it records what the bot sends.
type telegram struct {
	mu   sync.Mutex
	sent []map[string]any // sendMessage and editMessageText parameters
	docs int
}

func (tg *telegram) handler(w http.ResponseWriter, r *http.Request) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/sendDocument"):
		tg.docs++
	case strings.HasSuffix(r.URL.Path, "/sendMessage"), strings.HasSuffix(r.URL.Path, "/editMessageText"):
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		tg.sent = append(tg.sent, p)
	}
	_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
}

// said is every text the bot sent, joined.
func (tg *telegram) said() string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	var b strings.Builder
	for _, p := range tg.sent {
		if s, ok := p["text"].(string); ok {
			b.WriteString(s + "\n")
		}
	}
	return b.String()
}

func (tg *telegram) reset() {
	tg.mu.Lock()
	tg.sent, tg.docs = nil, 0
	tg.mu.Unlock()
}

type fixture struct {
	bot      *Bot
	tg       *telegram
	db       *gorm.DB
	backups  int
	restarts int
}

func newFixture(t *testing.T) *fixture { return newFixtureOn(t, notify.PlatformTelegram) }

// newFixtureOn is the bot on a platform. Ali's plan names the customer by
// their Bale account as well as their Telegram one; sara's by Telegram alone.
func newFixtureOn(t *testing.T, platform string) *fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Client{
		{Name: "ali", Status: model.StatusActive, TelegramID: customerID, BaleID: customerID, UsedBytes: 5 << 30, QuotaBytes: 10 << 30},
		{Name: "sara", Status: model.StatusActive, TelegramID: 4004, UsedBytes: 1 << 30},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg := &telegram{}
	srv := httptest.NewServer(http.HandlerFunc(tg.handler))
	t.Cleanup(srv.Close)
	f := &fixture{tg: tg, db: db}
	f.bot = New(Deps{
		Config: func() notify.Config {
			return notify.Config{Enabled: true, BotToken: "test:token", ChatID: "999, 1001", APIServer: srv.URL, Lang: "en", Platform: platform}
		},
		Clients: service.NewClients(db, ipam.NewPools(), log),
		Sys:     func() sysinfo.Snapshot { return sysinfo.Snapshot{} },
		Backup: func(context.Context) (string, []byte, error) {
			f.backups++
			return "wui-backup.tar.gz.enc", []byte("sealed"), nil
		},
		Restart:       func(context.Context) error { f.restarts++; return nil },
		LoginFailures: func() []string { return []string{"203.0.113.9 owner"} },
		Version:       "test",
	}, log)
	return f
}

func (f *fixture) message(from int64, text string) {
	f.bot.handle(context.Background(), update{Message: &message{ID: 1, From: &user{ID: from, FirstName: "x"},
		Chat: struct {
			ID int64 `json:"id"`
		}{ID: from}, Text: text}})
}

func (f *fixture) press(from int64, data string) {
	f.bot.handle(context.Background(), update{Callback: &callbackQuery{ID: "q", From: &user{ID: from}, Data: data,
		Message: &message{ID: 2, Chat: struct {
			ID int64 `json:"id"`
		}{ID: from}}}})
}

func TestTheAdministratorsAreTheOnesTheSettingsName(t *testing.T) {
	f := newFixture(t)
	if !f.bot.isAdmin(adminID) || !f.bot.isAdmin(999) || f.bot.isAdmin(customerID) || f.bot.isAdmin(0) {
		t.Fatal("the admin list is read wrong")
	}
}

// Nobody else can use an administrator's button or command, whatever they
// send: no backup, no reset, no restart, no customer made, no report.
func TestOnlyAnAdministratorCanAct(t *testing.T) {
	f := newFixture(t)
	for _, data := range []string{"backup", "reset_all_confirm", "sorted_report", "all_clients", "ban_logs",
		"onlines", "c:del:1", "c:reset:1"} {
		f.press(strangerID, data)
		f.press(customerID, data)
	}
	for _, cmd := range []string{"/restart", "/clearall", "/inbound wg0", "/usage sara"} {
		f.message(strangerID, cmd)
	}
	if f.backups != 0 || f.restarts != 0 || f.tg.docs != 0 {
		t.Fatalf("a stranger made %d backups, %d restarts, %d uploads", f.backups, f.restarts, f.tg.docs)
	}
	var sara model.Client
	f.db.Where("name = ?", "sara").First(&sara)
	if sara.UsedBytes != 1<<30 {
		t.Fatal("a stranger reset a customer's usage")
	}
	if said := f.tg.said(); strings.Contains(said, "203.0.113.9") || strings.Contains(strings.ToLower(said), "sara") {
		t.Fatalf("a stranger was told what is the administrator's:\n%s", said)
	}

	// A question asked of an administrator in a chat is not answered by
	// whoever writes next there.
	f.bot.expect(strangerID, "add_client")
	f.message(strangerID, "made-by-a-stranger")
	var n int64
	f.db.Model(&model.Client{}).Where("name = ?", "made-by-a-stranger").Count(&n)
	if n != 0 {
		t.Fatal("a stranger made a customer")
	}
}

func TestAnAdministratorGetsWhatTheyAskFor(t *testing.T) {
	f := newFixture(t)
	f.press(adminID, "backup")
	if f.backups != 1 || f.tg.docs != 1 {
		t.Fatalf("backup: %d taken, %d sent", f.backups, f.tg.docs)
	}
	f.message(adminID, "/restart")
	if f.restarts != 1 {
		t.Fatal("restart")
	}
	f.tg.reset()
	f.press(adminID, "ban_logs")
	if !strings.Contains(f.tg.said(), "203.0.113.9") {
		t.Fatalf("ban logs: %s", f.tg.said())
	}
}

// A customer is shown their own plan, and asking for somebody else's by
// name shows them nothing of it.
func TestACustomerSeesOnlyTheirOwnPlan(t *testing.T) {
	f := newFixture(t)
	f.message(customerID, "/usage ali")
	if said := f.tg.said(); !strings.Contains(said, "ali") {
		t.Fatalf("their own: %s", said)
	}
	f.tg.reset()
	f.message(customerID, "/usage sara")
	f.press(customerID, "my_usage")
	if said := f.tg.said(); strings.Contains(strings.ToLower(said), "sara") {
		t.Fatalf("shown another customer's plan:\n%s", said)
	}
	f.tg.reset()
	f.message(strangerID, "/usage ali")
	if said := f.tg.said(); strings.Contains(said, "GB") || strings.Contains(said, "GiB") {
		t.Fatalf("a stranger was shown a plan:\n%s", said)
	}
}

// A handler that fails cannot take the bot down with it.
func TestAPanicInAHandlerDoesNotStopTheBot(t *testing.T) {
	f := newFixture(t)
	f.bot.Backup = nil
	f.press(adminID, "backup")
	f.tg.reset()
	f.message(adminID, "/status")
	if len(f.tg.sent) != 1 {
		t.Fatalf("the bot stopped answering after a panic: %d sent", len(f.tg.sent))
	}
}

// A long report is sent in pages within Telegram's limit, every line of it,
// whether it has blank lines or not, and a page never ends inside a tag.
func TestLongMessagesAreSentInPagesThatTelegramTakes(t *testing.T) {
	const limit = 3800
	var lines []string
	for i := 0; i < 600; i++ {
		lines = append(lines, "customer-"+strings.Repeat("x", i%40)+" · 4.2 GiB of 10 GiB")
	}
	for name, text := range map[string]string{
		"a list with no blank line":   strings.Join(lines, "\n"),
		"blocks":                      strings.Join(lines, "\n\n"),
		"one line longer than a page": strings.Repeat("ب", 5000),
		"inside a code block":         "<b>Refused sign-ins</b>\n<code>" + strings.Join(lines, "\n") + "</code>",
	} {
		ps := pages(text, limit)
		if len(ps) < 2 {
			t.Errorf("%s: %d pages", name, len(ps))
			continue
		}
		var all strings.Builder
		for _, p := range ps {
			if len(p) > limit {
				t.Errorf("%s: a page of %d bytes", name, len(p))
			}
			if !utf8.ValidString(p) {
				t.Errorf("%s: a page cut inside a character", name)
			}
			if open := openTags(p); len(open) != 0 {
				t.Errorf("%s: a page ends with %v open", name, open)
			}
			all.WriteString(p)
		}
		strip := func(s string) string {
			for _, tag := range []string{"<b>", "</b>", "<code>", "</code>", "\n"} {
				s = strings.ReplaceAll(s, tag, "")
			}
			return s
		}
		if strip(all.String()) != strip(text) {
			t.Errorf("%s: text was lost between pages", name)
		}
	}
	if ps := pages("short", limit); len(ps) != 1 || ps[0] != "short" {
		t.Fatalf("a short message: %v", ps)
	}
}
