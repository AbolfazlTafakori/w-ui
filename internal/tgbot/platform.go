package tgbot

import (
	"context"
	"html"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// The same bot runs on Bale, the Iranian messenger whose bot API is
// Telegram's with small differences: one Bot for each, each with its own
// settings. What a Bale bot does differently is all here:
//
//   - its API's address (notify.BaleAPI unless the settings name another);
//   - its text: Bale has no parse_mode and renders Markdown, so the HTML the
//     bot writes is turned into that;
//   - its customers: a plan names its Bale account apart from its Telegram one;
//   - its name in its own words: "Telegram" in a message is "Bale" there.

// bale reports whether this bot is the Bale one.
func (b *Bot) bale() bool { return b.config().Platform == notify.PlatformBale }

// name is this bot's platform, for the log.
func (b *Bot) name() string { return b.config().Name() }

// ownClients are the plans a chat user is on, by the account this platform
// knows them by.
func (b *Bot) ownClients(ctx context.Context, userID int64) ([]model.Client, error) {
	if b.bale() {
		return b.Clients.ByBaleID(ctx, userID)
	}
	return b.Clients.ByTelegramID(ctx, userID)
}

// accountID is the customer's account on this bot's platform.
func (b *Bot) accountID(c *model.Client) int64 {
	if b.bale() {
		return c.BaleID
	}
	return c.TelegramID
}

// inPlatformWords names the platform a message is read on.
var inPlatformWords = strings.NewReplacer("Telegram", "Bale", "تلگرام", "بله")

// forPlatform is a message as this bot's platform takes it.
func (b *Bot) forPlatform(text string) string {
	if !b.bale() {
		return text
	}
	return baleText(text)
}

// htmlToMarkdown are the tags the bot writes, as Telegram's Markdown writes
// them; notify.BaleMarkdown then spaces the markers as Bale needs.
var htmlToMarkdown = strings.NewReplacer(
	"<b>", "*", "</b>", "*",
	"<i>", "_", "</i>", "_",
	"<code>", "`", "</code>", "`",
	"<pre>", "```\n", "</pre>", "\n```",
)

// baleText turns the bot's HTML into Bale's Markdown.
func baleText(s string) string {
	return notify.BaleMarkdown(html.UnescapeString(htmlToMarkdown.Replace(s)))
}
