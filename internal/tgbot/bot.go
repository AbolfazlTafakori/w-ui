package tgbot

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/sysinfo"
)

// Deps is what the bot needs from the rest of the panel, as functions and
// services rather than the panel itself, so it can be tested with stubs.
type Deps struct {
	Config   func() notify.Config
	Clients  *service.Clients
	Subs     *service.Subscriptions
	Ifaces   *service.Interfaces
	Settings *service.Settings
	Sys      func() sysinfo.Snapshot
	// Backup takes a backup and returns its name and bytes.
	Backup func(ctx context.Context) (string, []byte, error)
	// Restart reopens every tunnel on this server.
	Restart func(ctx context.Context) error
	// LoginFailures are the recent refused sign-ins, newest first.
	LoginFailures func() []string
	Version       string
}

// Bot answers on the chat the settings name, and any customer whose
// Telegram id is on their plan.
type Bot struct {
	Deps
	log  *slog.Logger
	http *http.Client

	mu      sync.Mutex
	offset  int64
	pending map[int64]string // chat -> what the next message is for
}

func New(d Deps, log *slog.Logger) *Bot {
	return &Bot{Deps: d, log: log, http: &http.Client{Timeout: 45 * time.Second}, pending: map[int64]string{}}
}

func (b *Bot) config() notify.Config { return b.Config() }

// Run polls for updates until ctx ends. With the bot switched off or
// unconfigured it sleeps and looks again, so turning it on in the settings
// takes effect without a restart.
func (b *Bot) Run(ctx context.Context) {
	for {
		cfg := b.config()
		if !cfg.Enabled || cfg.BotToken == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			continue
		}
		var updates []update
		err := b.call(ctx, "getUpdates", map[string]any{"offset": b.offset, "timeout": 30, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.log.Warn("telegram bot could not poll", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			b.offset = u.ID + 1
			b.handle(ctx, u)
		}
	}
}

// admins are the chat ids named in the settings, comma-separated.
func (b *Bot) isAdmin(id int64) bool {
	for _, part := range strings.Split(b.config().ChatID, ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && n == id {
			return true
		}
	}
	return false
}

func (b *Bot) handle(ctx context.Context, u update) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("telegram bot handler panicked", "panic", r)
		}
	}()
	switch {
	case u.Callback != nil && u.Callback.Message != nil:
		b.onCallback(ctx, u.Callback)
	case u.Message != nil && u.Message.From != nil:
		b.onMessage(ctx, u.Message)
	}
}

func (b *Bot) onMessage(ctx context.Context, m *message) {
	chat := m.Chat.ID
	admin := b.isAdmin(m.From.ID)
	text := strings.TrimSpace(m.Text)

	b.mu.Lock()
	want, waiting := b.pending[chat]
	delete(b.pending, chat)
	b.mu.Unlock()

	if strings.HasPrefix(text, "/") {
		b.onCommand(ctx, m, admin)
		return
	}
	if waiting {
		b.onReply(ctx, chat, m.From, admin, want, text)
		return
	}
	b.sendMenu(ctx, chat, b.t("pleaseChoose"), admin)
}

func (b *Bot) onCommand(ctx context.Context, m *message, admin bool) {
	chat := m.Chat.ID
	parts := strings.Fields(m.Text)
	cmd := strings.TrimPrefix(strings.SplitN(parts[0], "@", 2)[0], "/")
	args := parts[1:]

	switch cmd {
	case "help":
		b.sendMenu(ctx, chat, b.t("help")+"\n\n"+b.t("pleaseChoose"), admin)
	case "start":
		msg := fmt.Sprintf(b.t("start"), html.EscapeString(m.From.FirstName))
		if admin {
			msg += "\n" + fmt.Sprintf(b.t("welcome"), html.EscapeString(b.Sys().Host.Hostname))
		}
		b.sendMenu(ctx, chat, msg+"\n\n"+b.t("pleaseChoose"), admin)
	case "status":
		b.send(ctx, chat, b.t("statusOK"), nil)
	case "id":
		b.send(ctx, chat, fmt.Sprintf(b.t("getID"), m.From.ID), nil)
	case "usage":
		if len(args) == 0 {
			b.send(ctx, chat, b.t("usageHint"), nil)
			return
		}
		if admin {
			b.showClient(ctx, chat, args[0], 0)
		} else {
			b.showOwnUsage(ctx, chat, m.From.ID, args[0])
		}
	case "inbound":
		if !admin || len(args) == 0 {
			b.send(ctx, chat, b.t("unknown"), nil)
			return
		}
		b.showInbound(ctx, chat, args[0])
	case "restart":
		if !admin {
			b.send(ctx, chat, b.t("unknown"), nil)
			return
		}
		if err := b.Restart(ctx); err != nil {
			b.send(ctx, chat, fmt.Sprintf(b.t("restartFailed"), html.EscapeString(err.Error())), nil)
		} else {
			b.send(ctx, chat, b.t("restartOK"), nil)
		}
	case "clearall":
		if !admin {
			b.send(ctx, chat, b.t("unknown"), nil)
			return
		}
		b.send(ctx, chat, b.t("areYouSure"), keyboard{
			{{Text: b.t("btnCancelReset"), Data: "reset_all_cancel"}},
			{{Text: b.t("btnConfirmReset"), Data: "reset_all_confirm"}},
		})
	default:
		b.send(ctx, chat, b.t("unknown"), nil)
	}
}

// sendMenu is their SendAnswer: the text with the admin's or the
// customer's keyboard under it.
func (b *Bot) sendMenu(ctx context.Context, chat int64, text string, admin bool) {
	if admin {
		b.send(ctx, chat, text, keyboard{
			{{Text: b.t("btnSortedReport"), Data: "sorted_report"}},
			{{Text: b.t("btnServerUsage"), Data: "server_usage"}, {Text: b.t("btnResetAll"), Data: "reset_all"}},
			{{Text: b.t("btnBackup"), Data: "backup"}, {Text: b.t("btnBanLogs"), Data: "ban_logs"}},
			{{Text: b.t("btnInbounds"), Data: "inbounds"}, {Text: b.t("btnDepleteSoon"), Data: "deplete_soon"}},
			{{Text: b.t("btnCommands"), Data: "commands"}, {Text: b.t("btnOnlines"), Data: "onlines"}},
			{{Text: b.t("btnAllClients"), Data: "all_clients"}, {Text: b.t("btnAddClient"), Data: "add_client"}},
			{{Text: b.t("btnSubLinks"), Data: "ask:sub"}, {Text: b.t("btnIndividual"), Data: "ask:files"}, {Text: b.t("btnQR"), Data: "ask:qr"}},
		})
		return
	}
	b.send(ctx, chat, text, keyboard{
		{{Text: b.t("btnClientUsage"), Data: "my_usage"}, {Text: b.t("btnCommands"), Data: "client_commands"}},
		{{Text: b.t("btnSubLinks"), Data: "my:sub"}, {Text: b.t("btnIndividual"), Data: "my:files"}},
		{{Text: b.t("btnQR"), Data: "my:qr"}},
	})
}

func (b *Bot) onCallback(ctx context.Context, q *callbackQuery) {
	chat := q.Message.Chat.ID
	admin := b.isAdmin(q.From.ID)
	data := q.Data
	b.answer(ctx, q.ID, "")

	// Customer buttons.
	switch {
	case data == "my_usage":
		b.showOwnUsage(ctx, chat, q.From.ID, "")
		return
	case data == "client_commands":
		b.send(ctx, chat, b.t("clientCommands"), nil)
		return
	case strings.HasPrefix(data, "my:"):
		clients, _ := b.Clients.ByTelegramID(ctx, q.From.ID)
		if len(clients) == 0 {
			b.send(ctx, chat, b.t("noResult"), nil)
			return
		}
		for i := range clients {
			b.sendLinks(ctx, chat, &clients[i], strings.TrimPrefix(data, "my:"))
		}
		return
	}
	if !admin {
		b.send(ctx, chat, b.t("unknown"), nil)
		return
	}

	// Administrator buttons.
	switch {
	case data == "sorted_report":
		b.send(ctx, chat, b.sortedReport(ctx), nil)
	case data == "server_usage":
		b.send(ctx, chat, b.serverUsage(ctx), nil)
	case data == "reset_all":
		b.send(ctx, chat, b.t("areYouSure"), keyboard{
			{{Text: b.t("btnCancelReset"), Data: "reset_all_cancel"}},
			{{Text: b.t("btnConfirmReset"), Data: "reset_all_confirm"}},
		})
	case data == "reset_all_confirm":
		n, err := b.Clients.ResetAllTraffic(ctx)
		if err != nil {
			b.edit(ctx, chat, q.Message.ID, html.EscapeString(err.Error()), nil)
		} else {
			b.edit(ctx, chat, q.Message.ID, fmt.Sprintf(b.t("resetAllDone"), n), nil)
		}
	case data == "reset_all_cancel":
		b.edit(ctx, chat, q.Message.ID, b.t("cancelled"), nil)
	case data == "backup":
		name, data, err := b.Backup(ctx)
		if err != nil {
			b.send(ctx, chat, html.EscapeString(err.Error()), nil)
			return
		}
		if err := b.sendDocument(ctx, chat, name, data, b.t("backupCaption")); err != nil {
			b.send(ctx, chat, html.EscapeString(err.Error()), nil)
		}
	case data == "ban_logs":
		lines := b.LoginFailures()
		if len(lines) == 0 {
			b.send(ctx, chat, b.t("noBanLogs"), nil)
			return
		}
		b.send(ctx, chat, "<b>"+b.t("banLogsTitle")+"</b>\n<code>"+html.EscapeString(strings.Join(lines, "\n"))+"</code>", nil)
	case data == "inbounds":
		b.send(ctx, chat, b.inboundUsages(ctx), nil)
	case data == "deplete_soon":
		b.send(ctx, chat, b.depleting(ctx), nil)
	case data == "commands":
		b.send(ctx, chat, b.t("adminCommands"), nil)
	case data == "onlines":
		b.send(ctx, chat, b.onlines(ctx), nil)
	case data == "all_clients":
		b.send(ctx, chat, b.allClients(ctx), nil)
	case data == "add_client":
		b.expect(chat, "add_client")
		b.send(ctx, chat, b.t("askNewName"), keyboard{{{Text: b.t("btnCancel"), Data: "cancel"}}})
	case strings.HasPrefix(data, "ask:"):
		b.expect(chat, data)
		b.send(ctx, chat, b.t("askEmail"), keyboard{{{Text: b.t("btnCancel"), Data: "cancel"}}})
	case data == "cancel":
		b.mu.Lock()
		delete(b.pending, chat)
		b.mu.Unlock()
		b.edit(ctx, chat, q.Message.ID, b.t("cancelled"), nil)
	case strings.HasPrefix(data, "c:"):
		b.onClientAction(ctx, chat, q, strings.TrimPrefix(data, "c:"))
	default:
		b.send(ctx, chat, b.t("unknown"), nil)
	}
}

func (b *Bot) expect(chat int64, what string) {
	b.mu.Lock()
	b.pending[chat] = what
	b.mu.Unlock()
}

// onReply is the next message after a question the bot asked.
func (b *Bot) onReply(ctx context.Context, chat int64, from *user, admin bool, want, text string) {
	if !admin {
		b.sendMenu(ctx, chat, b.t("pleaseChoose"), false)
		return
	}
	switch {
	case want == "add_client":
		b.addClient(ctx, chat, text)
	case strings.HasPrefix(want, "ask:"):
		c, err := b.Clients.ByName(ctx, text)
		if err != nil {
			b.send(ctx, chat, b.t("noResult"), nil)
			return
		}
		b.sendLinks(ctx, chat, c, strings.TrimPrefix(want, "ask:"))
	case strings.HasPrefix(want, "set:"):
		// "set:<field>:<id>" -- a value for a customer's field.
		f := strings.Split(want, ":")
		if len(f) != 3 {
			return
		}
		id, _ := strconv.ParseUint(f[2], 10, 64)
		b.setField(ctx, chat, uint(id), f[1], text)
	}
}

// ── the per-customer card, with its buttons, as their searchClient ──

func (b *Bot) showClient(ctx context.Context, chat int64, name string, editID int64) {
	c, err := b.Clients.ByName(ctx, name)
	if err != nil {
		b.send(ctx, chat, b.t("noResult"), nil)
		return
	}
	text := b.clientInfo(c)
	kb := keyboard{
		{{Text: b.t("btnRefresh"), Data: "c:refresh:" + itoa(c.ID)}, {Text: b.t("btnIPLog"), Data: "c:ips:" + itoa(c.ID)}},
		{{Text: b.t("btnResetTraffic"), Data: "c:reset:" + itoa(c.ID)}, {Text: b.t("btnLimitTraffic"), Data: "c:limit:" + itoa(c.ID)}},
		{{Text: b.t("btnResetExpiry"), Data: "c:expiry:" + itoa(c.ID)}, {Text: b.t("btnIPLimit"), Data: "c:devices:" + itoa(c.ID)}},
		{{Text: b.t("btnSetTgUser"), Data: "c:tgid:" + itoa(c.ID)}, {Text: b.t("btnToggleEnable"), Data: "c:toggle:" + itoa(c.ID)}},
		{{Text: b.t("btnSubLinks"), Data: "c:sub:" + itoa(c.ID)}, {Text: b.t("btnIndividual"), Data: "c:files:" + itoa(c.ID)}, {Text: b.t("btnQR"), Data: "c:qr:" + itoa(c.ID)}},
	}
	if editID != 0 {
		b.edit(ctx, chat, editID, text, kb)
	} else {
		b.send(ctx, chat, text, kb)
	}
}

func (b *Bot) onClientAction(ctx context.Context, chat int64, q *callbackQuery, rest string) {
	f := strings.SplitN(rest, ":", 3)
	if len(f) < 2 {
		return
	}
	action := f[0]
	id, _ := strconv.ParseUint(f[1], 10, 64)
	c, err := b.Clients.Get(ctx, uint(id))
	if err != nil {
		b.send(ctx, chat, b.t("noResult"), nil)
		return
	}
	switch action {
	case "refresh":
		b.showClient(ctx, chat, c.Name, q.Message.ID)
	case "ips":
		b.send(ctx, chat, b.deviceLog(c), nil)
	case "reset":
		b.send(ctx, chat, fmt.Sprintf(b.t("confirmResetFor"), html.EscapeString(c.Name)), keyboard{
			{{Text: b.t("btnCancel"), Data: "cancel"}}, {{Text: b.t("btnConfirmReset"), Data: "c:reset_yes:" + itoa(c.ID)}},
		})
	case "reset_yes":
		if _, err := b.Clients.ResetTraffic(ctx, c.ID); err != nil {
			b.edit(ctx, chat, q.Message.ID, html.EscapeString(err.Error()), nil)
			return
		}
		b.edit(ctx, chat, q.Message.ID, b.t("resetDone"), nil)
		b.showClient(ctx, chat, c.Name, 0)
	case "limit":
		b.pickNumber(ctx, chat, c.ID, "quota", b.t("askQuotaGB"), []string{"0", "5", "10", "20", "30", "50", "100", "200", "500", "1000"})
	case "expiry":
		b.pickNumber(ctx, chat, c.ID, "expiry", b.t("askExpiryDays"), []string{"0", "7", "10", "14", "20", "30", "60", "90", "180", "365"})
	case "devices":
		b.pickNumber(ctx, chat, c.ID, "devices", b.t("askDeviceLimit"), []string{"1", "2", "3", "4", "5", "6", "8", "10"})
	case "tgid":
		b.expect(chat, "set:tgid:"+itoa(c.ID))
		b.send(ctx, chat, fmt.Sprintf(b.t("askTgID"), html.EscapeString(c.Name)), keyboard{
			{{Text: b.t("btnRemoveTgID"), Data: "c:tgid_clear:" + itoa(c.ID)}}, {{Text: b.t("btnCancel"), Data: "cancel"}},
		})
	case "tgid_clear":
		b.setField(ctx, chat, c.ID, "tgid", "0")
	case "toggle":
		on := c.Status == model.StatusDisabled
		act := service.BulkDisable
		if on {
			act = service.BulkEnable
		}
		if _, err := b.Clients.Bulk(ctx, act, []uint{c.ID}); err != nil {
			b.send(ctx, chat, html.EscapeString(err.Error()), nil)
			return
		}
		b.showClient(ctx, chat, c.Name, q.Message.ID)
	case "sub", "files", "qr":
		b.sendLinks(ctx, chat, c, action)
	default:
		switch {
		case strings.HasPrefix(action, "val_") && len(f) == 3:
			// "c:val_<field>:<id>:<value>": a value pressed on the keypad.
			b.setField(ctx, chat, c.ID, strings.TrimPrefix(action, "val_"), f[2])
		case strings.HasPrefix(action, "custom_"):
			// "c:custom_<field>:<id>": the value comes as the next message.
			b.expect(chat, "set:"+strings.TrimPrefix(action, "custom_")+":"+itoa(c.ID))
			b.send(ctx, chat, b.t("askCustom"), keyboard{{{Text: b.t("btnCancel"), Data: "cancel"}}})
		}
	}
}

// pickNumber offers their numeric keypad: a row of values, and "custom".
func (b *Bot) pickNumber(ctx context.Context, chat int64, id uint, field, prompt string, values []string) {
	var kb keyboard
	var row []button
	for i, v := range values {
		row = append(row, button{Text: v, Data: "c:val_" + field + ":" + itoa(id) + ":" + v})
		if (i+1)%5 == 0 {
			kb = append(kb, row)
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	kb = append(kb, []button{{Text: b.t("btnCustom"), Data: "c:custom_" + field + ":" + itoa(id)}, {Text: b.t("btnCancel"), Data: "cancel"}})
	b.send(ctx, chat, prompt, kb)
}

// setField writes one field of a customer from a typed or pressed value.
func (b *Bot) setField(ctx context.Context, chat int64, id uint, field, value string) {
	value = strings.TrimSpace(value)
	in := service.UpdateInput{}
	switch field {
	case "quota":
		gb, err := strconv.ParseFloat(value, 64)
		if err != nil || gb < 0 {
			b.send(ctx, chat, b.t("badNumber"), nil)
			return
		}
		q := uint64(gb * (1 << 30))
		in.QuotaBytes = &q
	case "expiry":
		days, err := strconv.Atoi(value)
		if err != nil || days < 0 {
			b.send(ctx, chat, b.t("badNumber"), nil)
			return
		}
		var t *time.Time
		if days > 0 {
			at := time.Now().Add(time.Duration(days) * 24 * time.Hour)
			t = &at
		}
		in.ExpiresAt = &t
	case "devices":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			b.send(ctx, chat, b.t("badNumber"), nil)
			return
		}
		in.DeviceLimit = &n
	case "tgid":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			b.send(ctx, chat, b.t("badNumber"), nil)
			return
		}
		in.TelegramID = &n
	default:
		return
	}
	c, err := b.Clients.Update(ctx, id, in)
	if err != nil {
		b.send(ctx, chat, html.EscapeString(err.Error()), nil)
		return
	}
	b.send(ctx, chat, b.t("saved"), nil)
	b.showClient(ctx, chat, c.Name, 0)
}

// addClient makes a customer from a name with the panel's defaults.
func (b *Bot) addClient(ctx context.Context, chat int64, name string) {
	ifaces, err := b.Ifaces.List(ctx)
	if err != nil || len(ifaces) == 0 {
		b.send(ctx, chat, b.t("noInbounds"), nil)
		return
	}
	var ids []uint
	for _, i := range ifaces {
		if i.Enabled {
			ids = append(ids, i.ID)
		}
	}
	if len(ids) == 0 {
		ids = []uint{ifaces[0].ID}
	}
	in := service.CreateInput{Name: strings.TrimSpace(name), InterfaceIDs: ids, DeviceLimit: 1}
	if b.Settings != nil {
		if ps, err := b.Settings.Get(ctx); err == nil {
			in.QuotaBytes = ps.DefaultQuotaBytes
			in.DeviceLimit = ps.DefaultDeviceLimit
			if ps.DefaultExpiryDays > 0 {
				at := time.Now().Add(time.Duration(ps.DefaultExpiryDays) * 24 * time.Hour)
				in.ExpiresAt = &at
			}
		}
	}
	c, err := b.Clients.Create(ctx, in)
	if err != nil {
		b.send(ctx, chat, html.EscapeString(err.Error()), nil)
		return
	}
	b.send(ctx, chat, fmt.Sprintf(b.t("clientAdded"), html.EscapeString(c.Name)), nil)
	b.showClient(ctx, chat, c.Name, 0)
}

// ── what a customer is told about themselves ──

func (b *Bot) showOwnUsage(ctx context.Context, chat, tgID int64, name string) {
	clients, err := b.Clients.ByTelegramID(ctx, tgID)
	if err != nil || len(clients) == 0 {
		b.send(ctx, chat, b.t("notLinked"), nil)
		return
	}
	for i := range clients {
		if name != "" && !strings.EqualFold(clients[i].Name, name) {
			continue
		}
		b.send(ctx, chat, b.clientInfo(&clients[i]), keyboard{{{Text: b.t("btnRefresh"), Data: "my_usage"}}})
	}
}

// sendLinks hands over a customer's subscription link, files, or codes.
func (b *Bot) sendLinks(ctx context.Context, chat int64, c *model.Client, what string) {
	switch what {
	case "sub":
		token, err := b.Subs.EnsureToken(ctx, c.ID)
		if err != nil {
			b.send(ctx, chat, html.EscapeString(err.Error()), nil)
			return
		}
		link, err := b.Subs.LinkFor(ctx, token, "")
		if err != nil {
			b.send(ctx, chat, html.EscapeString(err.Error()), nil)
			return
		}
		b.send(ctx, chat, fmt.Sprintf("<b>%s</b>\n<code>%s</code>", html.EscapeString(c.Name), html.EscapeString(link)), nil)
	case "files", "qr":
		if len(c.Accounts) == 0 {
			b.send(ctx, chat, b.t("noDevices"), nil)
			return
		}
		for _, acc := range c.Accounts {
			profiles, err := b.Clients.Profiles(ctx, acc.ID)
			if err != nil {
				continue
			}
			for _, p := range profiles {
				caption := html.EscapeString(c.Name + " — " + acc.DeviceName)
				if p.HostName != "" {
					caption += " — " + html.EscapeString(p.HostName)
				}
				if what == "files" || c.Protocol != model.ProtocolWireGuard {
					if err := b.sendDocument(ctx, chat, p.Filename, []byte(p.Body), caption); err != nil {
						b.send(ctx, chat, html.EscapeString(err.Error()), nil)
					}
					continue
				}
				png, err := qrcode.Encode(p.Body, qrcode.Low, 512)
				if err != nil {
					continue
				}
				if err := b.sendPhoto(ctx, chat, strings.TrimSuffix(p.Filename, ".conf")+".png", png, caption); err != nil {
					b.send(ctx, chat, html.EscapeString(err.Error()), nil)
				}
			}
		}
	}
}

// ── the texts ──

func (b *Bot) clientInfo(c *model.Client) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>%s</b>\n", html.EscapeString(c.Name))
	status := b.t("active")
	switch c.Status {
	case model.StatusDisabled:
		status = b.t("disabled")
	case model.StatusExpired:
		status = b.t("expired")
	case model.StatusExhausted:
		status = b.t("exhausted")
	}
	fmt.Fprintf(&sb, "%s: %s\n", b.t("status"), status)
	fmt.Fprintf(&sb, "%s: ↑ %s / ↓ %s\n", b.t("traffic"), human(c.UpBytes), human(c.DownBytes))
	total := "∞"
	if c.QuotaBytes > 0 {
		total = human(c.QuotaBytes)
	}
	fmt.Fprintf(&sb, "%s: %s / %s\n", b.t("usage"), human(c.UsedBytes), total)
	if c.QuotaBytes > 0 {
		left := uint64(0)
		if c.QuotaBytes > c.UsedBytes {
			left = c.QuotaBytes - c.UsedBytes
		}
		fmt.Fprintf(&sb, "%s: %s\n", b.t("remaining"), human(left))
	}
	if c.ExpiresAt == nil {
		fmt.Fprintf(&sb, "%s: ∞\n", b.t("expiry"))
	} else {
		fmt.Fprintf(&sb, "%s: %s (%s)\n", b.t("expiry"), c.ExpiresAt.Local().Format("2006-01-02 15:04"), untilText(*c.ExpiresAt))
	}
	fmt.Fprintf(&sb, "%s: %d / %d\n", b.t("devices"), len(c.Accounts), c.DeviceLimit)
	var last *time.Time
	for _, a := range c.Accounts {
		if a.LastHandshake != nil && (last == nil || a.LastHandshake.After(*last)) {
			last = a.LastHandshake
		}
	}
	if last != nil {
		fmt.Fprintf(&sb, "%s: %s\n", b.t("lastOnline"), last.Local().Format("2006-01-02 15:04"))
	}
	if c.Group != "" {
		fmt.Fprintf(&sb, "%s: %s\n", b.t("group"), html.EscapeString(c.Group))
	}
	if c.TelegramID != 0 {
		fmt.Fprintf(&sb, "%s: <code>%d</code>\n", b.t("tgID"), c.TelegramID)
	}
	if c.Note != "" {
		fmt.Fprintf(&sb, "%s: %s\n", b.t("note"), html.EscapeString(c.Note))
	}
	return sb.String()
}

func (b *Bot) deviceLog(c *model.Client) string {
	if len(c.Accounts) == 0 {
		return b.t("noDevices")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>%s</b>\n", html.EscapeString(c.Name))
	for _, a := range c.Accounts {
		seen := "-"
		if a.LastHandshake != nil {
			seen = a.LastHandshake.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&sb, "• %s <code>%s</code> ← <code>%s</code> (%s)\n", html.EscapeString(a.DeviceName), a.IP, html.EscapeString(a.LastEndpoint), seen)
	}
	return sb.String()
}

// serverUsage is their sendServerUsage.
func (b *Bot) serverUsage(ctx context.Context) string {
	s := b.Sys()
	ov, _ := b.Clients.Overview(ctx)
	var sb strings.Builder
	fmt.Fprintf(&sb, "🖥 %s: <code>%s</code>\n", b.t("hostname"), html.EscapeString(s.Host.Hostname))
	fmt.Fprintf(&sb, "📦 %s: <code>%s</code>\n", b.t("version"), html.EscapeString(b.Version))
	fmt.Fprintf(&sb, "⏱ %s: %s\n", b.t("uptime"), (time.Duration(s.Host.UptimeSec) * time.Second).Truncate(time.Minute))
	fmt.Fprintf(&sb, "🔥 %s: %.1f%% (%d)\n", b.t("cpu"), s.CPU.Percent, s.CPU.Cores)
	fmt.Fprintf(&sb, "💾 %s: %s / %s\n", b.t("memory"), human(s.Memory.Used), human(s.Memory.Total))
	fmt.Fprintf(&sb, "💿 %s: %s / %s\n", b.t("disk"), human(s.Disk.Used), human(s.Disk.Total))
	fmt.Fprintf(&sb, "🔌 TCP/UDP: %d / %d\n", s.Network.TCPConns, s.Network.UDPConns)
	fmt.Fprintf(&sb, "↕️ %s: ↑ %s / ↓ %s\n", b.t("traffic"), human(s.Network.BytesSent), human(s.Network.BytesRecv))
	if ov != nil {
		fmt.Fprintf(&sb, "👥 %s: %d, %s: %d\n", b.t("clients"), ov.Clients, b.t("online"), ov.Online)
	}
	return sb.String()
}

func (b *Bot) inboundUsages(ctx context.Context) string {
	ifaces, err := b.Ifaces.List(ctx)
	if err != nil || len(ifaces) == 0 {
		return b.t("noInbounds")
	}
	loads, _ := b.Ifaces.Loads(ctx)
	var sb strings.Builder
	for _, i := range ifaces {
		l := loads[i.ID]
		fmt.Fprintf(&sb, "<b>%s</b> (%s:%d)\n%s: %d · %s: %d · %s: ↑ %s / ↓ %s\n\n",
			html.EscapeString(i.Name), i.Protocol, i.ListenPort,
			b.t("clients"), l.Clients, b.t("online"), l.Online, b.t("traffic"), human(l.UpBytes), human(l.DownBytes))
	}
	return sb.String()
}

func (b *Bot) showInbound(ctx context.Context, chat int64, name string) {
	ifaces, err := b.Ifaces.List(ctx)
	if err != nil {
		b.send(ctx, chat, html.EscapeString(err.Error()), nil)
		return
	}
	loads, _ := b.Ifaces.Loads(ctx)
	for _, i := range ifaces {
		if strings.EqualFold(i.Name, name) {
			l := loads[i.ID]
			b.send(ctx, chat, fmt.Sprintf("<b>%s</b> (%s:%d)\n%s: %d · %s: %d\n%s: ↑ %s / ↓ %s",
				html.EscapeString(i.Name), i.Protocol, i.ListenPort, b.t("clients"), l.Clients, b.t("online"), l.Online,
				b.t("traffic"), human(l.UpBytes), human(l.DownBytes)), nil)
			return
		}
	}
	b.send(ctx, chat, b.t("noResult"), nil)
}

func (b *Bot) list(ctx context.Context, f service.ListFilter) []model.Client {
	f.PerPage = 500
	page, err := b.Clients.List(ctx, f)
	if err != nil || page == nil {
		return nil
	}
	return page.Items
}

func (b *Bot) allClients(ctx context.Context) string {
	items := b.list(ctx, service.ListFilter{Sort: "name"})
	if len(items) == 0 {
		return b.t("noResult")
	}
	var sb strings.Builder
	for _, c := range items {
		total := "∞"
		if c.QuotaBytes > 0 {
			total = human(c.QuotaBytes)
		}
		fmt.Fprintf(&sb, "• <b>%s</b> — %s / %s\n", html.EscapeString(c.Name), human(c.UsedBytes), total)
	}
	return sb.String()
}

func (b *Bot) onlines(ctx context.Context) string {
	items := b.list(ctx, service.ListFilter{Sort: "online"})
	var sb strings.Builder
	n := 0
	for _, c := range items {
		on := false
		for _, a := range c.Accounts {
			if a.LastHandshake != nil && time.Since(*a.LastHandshake) < time.Duration(service.OnlineWithin.Load())*time.Second {
				on = true
			}
		}
		if on {
			n++
			fmt.Fprintf(&sb, "• %s\n", html.EscapeString(c.Name))
		}
	}
	if n == 0 {
		return b.t("noOnlines")
	}
	return fmt.Sprintf("<b>%s: %d</b>\n", b.t("online"), n) + sb.String()
}

func (b *Bot) depleting(ctx context.Context) string {
	items := b.list(ctx, service.ListFilter{Buckets: []string{"depleting"}})
	items = append(items, b.list(ctx, service.ListFilter{Buckets: []string{"expiring"}})...)
	seen := map[uint]bool{}
	var sb strings.Builder
	for _, c := range items {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		left := "∞"
		if c.QuotaBytes > 0 && c.QuotaBytes > c.UsedBytes {
			left = human(c.QuotaBytes - c.UsedBytes)
		}
		exp := "∞"
		if c.ExpiresAt != nil {
			exp = untilText(*c.ExpiresAt)
		}
		fmt.Fprintf(&sb, "• <b>%s</b> — %s: %s, %s: %s\n", html.EscapeString(c.Name), b.t("remaining"), left, b.t("expiry"), exp)
	}
	if sb.Len() == 0 {
		return b.t("noDepleting")
	}
	return sb.String()
}

// sortedReport is their sorted traffic usage report: every customer,
// heaviest first.
func (b *Bot) sortedReport(ctx context.Context) string {
	items := b.list(ctx, service.ListFilter{Sort: "traffic"})
	if len(items) == 0 {
		return b.t("noResult")
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UsedBytes > items[j].UsedBytes })
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>%s</b>\n", b.t("sortedReportTitle"))
	for i, c := range items {
		fmt.Fprintf(&sb, "%d. %s — %s\n", i+1, html.EscapeString(c.Name), human(c.UsedBytes))
	}
	return sb.String()
}

func itoa(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func human(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func untilText(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
