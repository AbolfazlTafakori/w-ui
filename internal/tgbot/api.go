// Package tgbot is the panel's Telegram bot: the same bot the classic panel runs, for
// the same two audiences. An administrator asks it for the server's state,
// the customers, backups and links, and presses its buttons to reset
// traffic or add a customer; a customer whose Telegram id is on their plan
// asks it how much they have left and gets their links and QR codes.
//
// Talks to Telegram's HTTP API directly, by long polling, with no library:
// the four calls it needs are small, and a dependency would be most of the
// package.
package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// update is one thing Telegram reports.
type update struct {
	ID       int64          `json:"update_id"`
	Message  *message       `json:"message"`
	Callback *callbackQuery `json:"callback_query"`
}

type message struct {
	ID   int64 `json:"message_id"`
	From *user `json:"from"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
}

type user struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	From    *user    `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
}

// keyboard is an inline keyboard: rows of buttons.
type keyboard [][]button

type button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

func (k keyboard) markup() map[string]any {
	if len(k) == 0 {
		return nil
	}
	return map[string]any{"inline_keyboard": k}
}

// call posts one method and returns Telegram's result.
func (b *Bot) call(ctx context.Context, method string, params map[string]any, out any) error {
	cfg := b.config()
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint(cfg.APIServer, cfg.BotToken, method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return b.do(req, out)
}

// upload posts a file with a method that takes one.
func (b *Bot) upload(ctx context.Context, method, field, filename string, data []byte, params map[string]string) error {
	cfg := b.config()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range params {
		_ = w.WriteField(k, v)
	}
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	_ = w.Close()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint(cfg.APIServer, cfg.BotToken, method), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return b.do(req, nil)
}

func (b *Bot) do(req *http.Request, out any) error {
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("telegram: %s: %w", resp.Status, err)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram: %s", envelope.Description)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func (b *Bot) endpoint(apiServer, token, method string) string {
	base := strings.TrimRight(strings.TrimSpace(apiServer), "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return fmt.Sprintf("%s/bot%s/%s", base, url.PathEscape(token), method)
}

// send writes a message, HTML-formatted, with an optional keyboard.
func (b *Bot) send(ctx context.Context, chat int64, text string, kb keyboard) {
	for _, page := range pages(text, 3800) {
		params := map[string]any{"chat_id": chat, "text": page, "parse_mode": "HTML", "disable_web_page_preview": true}
		if m := kb.markup(); m != nil {
			params["reply_markup"] = m
		}
		if err := b.call(ctx, "sendMessage", params, nil); err != nil {
			b.log.Warn("telegram bot could not send", "error", err)
			return
		}
		kb = nil // the keyboard goes with the first page only
	}
}

// edit rewrites a message the bot sent, keyboard included.
func (b *Bot) edit(ctx context.Context, chat, msgID int64, text string, kb keyboard) {
	params := map[string]any{"chat_id": chat, "message_id": msgID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if m := kb.markup(); m != nil {
		params["reply_markup"] = m
	}
	if err := b.call(ctx, "editMessageText", params, nil); err != nil {
		// An unchanged message is refused; the answer is the same either way.
		if !strings.Contains(err.Error(), "not modified") {
			b.send(ctx, chat, text, kb)
		}
	}
}

func (b *Bot) answer(ctx context.Context, id, text string) {
	_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

func (b *Bot) sendDocument(ctx context.Context, chat int64, name string, data []byte, caption string) error {
	return b.upload(ctx, "sendDocument", "document", name, data, map[string]string{"chat_id": fmt.Sprint(chat), "caption": caption, "parse_mode": "HTML"})
}

func (b *Bot) sendPhoto(ctx context.Context, chat int64, name string, png []byte, caption string) error {
	return b.upload(ctx, "sendPhoto", "photo", name, png, map[string]string{"chat_id": fmt.Sprint(chat), "caption": caption, "parse_mode": "HTML"})
}

// pages splits a long message on blank lines the way the classic panel's bot does, so
// nothing is cut mid-line by Telegram's limit.
func pages(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var out []string
	cur := ""
	for _, block := range strings.Split(text, "\n\n") {
		if cur != "" && len(cur)+2+len(block) > limit {
			out = append(out, cur)
			cur = ""
		}
		if cur != "" {
			cur += "\n\n"
		}
		cur += block
	}
	if strings.TrimSpace(cur) != "" {
		out = append(out, cur)
	}
	return out
}
