package tgbot

import (
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// The Bale bot is the same bot: what a customer and an administrator can do
// is tested above for Telegram, and holds here because it is the same code.
// What is Bale's own is how it writes, and whom it knows by which account.

// Bale has no parse_mode: the bot's HTML reaches it as the Markdown it
// renders, with no tag and no entity left in it.
func TestTheBaleBotWritesMarkdown(t *testing.T) {
	f := newFixtureOn(t, notify.PlatformBale)
	f.message(customerID, "/usage ali")
	f.tg.mu.Lock()
	sent := append([]map[string]any(nil), f.tg.sent...)
	f.tg.mu.Unlock()
	if len(sent) == 0 {
		t.Fatal("nothing sent")
	}
	for _, p := range sent {
		if _, ok := p["parse_mode"]; ok {
			t.Errorf("Bale was sent a parse_mode: %v", p["parse_mode"])
		}
		text, _ := p["text"].(string)
		for _, tag := range []string{"<b>", "</b>", "<code>", "&lt;", "&amp;"} {
			if strings.Contains(text, tag) {
				t.Errorf("%q is left in the text:\n%s", tag, text)
			}
		}
	}
	if !strings.Contains(f.tg.said(), "*") {
		t.Errorf("no bold reached Bale:\n%s", f.tg.said())
	}
}

// On Bale the bot calls itself Bale, and asks for the Bale id.
func TestTheBaleBotSaysBale(t *testing.T) {
	f := newFixtureOn(t, notify.PlatformBale)
	if got := f.bot.t("getID"); !strings.Contains(got, "Bale") || strings.Contains(got, "Telegram") {
		t.Errorf("getID on Bale: %q", got)
	}
	f.bot.Config = func() notify.Config { return notify.Config{Platform: notify.PlatformBale, Lang: "fa"} }
	if got := f.bot.t("askTgID"); !strings.Contains(got, "بله") || strings.Contains(got, "تلگرام") {
		t.Errorf("askTgID on Bale in Persian: %q", got)
	}
	tg := newFixture(t)
	if got := tg.bot.t("getID"); !strings.Contains(got, "Telegram") {
		t.Errorf("the Telegram bot changed its words: %q", got)
	}
}

// A customer is known on Bale by the Bale id on their plan, and setting it
// from the Bale bot sets that one, not the Telegram id.
func TestTheBaleBotKnowsCustomersByTheirBaleID(t *testing.T) {
	f := newFixtureOn(t, notify.PlatformBale)
	f.message(customerID, "/usage ali")
	if !strings.Contains(f.tg.said(), "ali") {
		t.Fatalf("a customer with a Bale id was not answered:\n%s", f.tg.said())
	}

	var sara model.Client
	f.db.Where("name = ?", "sara").First(&sara)
	f.tg.reset()
	f.message(4004, "/usage") // sara's Telegram id, but no Bale id on her plan
	if strings.Contains(strings.ToLower(f.tg.said()), "sara") {
		t.Fatalf("a Telegram id was taken for a Bale one:\n%s", f.tg.said())
	}

	f.bot.setField(t.Context(), adminID, sara.ID, "tgid", "777")
	f.db.First(&sara, sara.ID)
	if sara.BaleID != 777 || sara.TelegramID != 4004 {
		t.Fatalf("set from Bale: bale id %d, telegram id %d", sara.BaleID, sara.TelegramID)
	}
}

func TestBaleTextHasNoHTML(t *testing.T) {
	got := baleText("<b>W-UI</b> on <code>vpn&amp;co</code>\n<pre>a &lt; b</pre>")
	want := "*W-UI* on `vpn&co`\n```\na < b\n```"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
