package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// Panel settings live in the database rather than in the environment.
//
// The process-level configuration — where to listen, where the data lives — is
// deliberately not editable here. The panel runs unprivileged with a read-only
// filesystem apart from its own data directory, so it cannot write the file
// systemd reads; a form that appeared to change those and silently did not would
// be worse than no form. Those are shown read-only and changed with `w-ui`.
//
// What is editable is what the panel itself consults while running: how long a
// session lasts, and the values a new customer starts from.

// Setting keys. They are namespaced so an operator reading the table can tell
// what a row belongs to.
const (
	keySessionHours    = "panel.sessionHours"
	keyDefaultLocale   = "panel.defaultLocale"
	keyDefQuotaBytes   = "client.defaultQuotaBytes"
	keyDefExpiryDays   = "client.defaultExpiryDays"
	keyDefDeviceLimit  = "client.defaultDeviceLimit"
	keyDefRateBits     = "client.defaultRateBitsPerSec"
	keyDefResetCycle   = "client.defaultResetCycle"
	keyDefInterfaceID  = "client.defaultInterfaceId"
	keyNotifyEnabled   = "notify.enabled"
	keyNotifyToken     = "notify.botToken"
	keyNotifyChat      = "notify.chatId"
	keyNotifyKinds     = "notify.kinds"
	keyBackupEvery     = "backup.everyHours"
	keyBackupKeep      = "backup.keep"
	maxSessionHours    = 24 * 30
	maxDeviceLimit     = 64
	maxExpiryDays      = 365 * 10
	defaultSessionHrs  = 12
	defaultDeviceLimit = 1
)

// PanelSettings is everything the settings page can change.
type PanelSettings struct {
	SessionHours  int    `json:"sessionHours"`
	DefaultLocale string `json:"defaultLocale"`

	DefaultQuotaBytes     uint64 `json:"defaultQuotaBytes"`
	DefaultExpiryDays     int    `json:"defaultExpiryDays"`
	DefaultDeviceLimit    int    `json:"defaultDeviceLimit"`
	DefaultRateBitsPerSec uint64 `json:"defaultRateBitsPerSec"`
	DefaultResetCycle     string `json:"defaultResetCycle"`
	DefaultInterfaceID    uint   `json:"defaultInterfaceId"`

	NotifyEnabled bool     `json:"notifyEnabled"`
	NotifyChatID  string   `json:"notifyChatId"`
	NotifyKinds   []string `json:"notifyKinds"`
	// NotifyBotToken is write-only. It is returned as a placeholder so the page
	// can show that one is set without handing it back on every page load.
	NotifyBotToken string `json:"notifyBotToken"`

	BackupEveryHours int `json:"backupEveryHours"`
	BackupKeep       int `json:"backupKeep"`
}

// TokenPlaceholder is what the API returns in place of a stored bot token.
// Saving it back means "leave the token alone".
const TokenPlaceholder = "********"

// Settings reads and writes the panel's own configuration.
type Settings struct {
	db     *gorm.DB
	locale string

	// cache holds the last saved values so the hot paths that consult them —
	// every sign-in, every client form — do not query on each call.
	mu     sync.RWMutex
	cache  *PanelSettings
	loaded bool
}

// NewSettings builds the service. The locale is the process default, used when
// nothing has been saved.
func NewSettings(db *gorm.DB, locale string) *Settings {
	return &Settings{db: db, locale: locale}
}

// Defaults are the values a fresh install behaves as if it had.
//
// They are exposed to the frontend so a value equal to its default can be
// labelled as such, which is how an operator tells "I chose this" from "this is
// simply what it ships as".
func (s *Settings) Defaults() PanelSettings {
	return PanelSettings{
		SessionHours:       defaultSessionHrs,
		DefaultLocale:      s.locale,
		DefaultDeviceLimit: defaultDeviceLimit,
		DefaultResetCycle:  string(model.ResetNone),
		NotifyKinds:        []string{},
		BackupEveryHours:   24,
		BackupKeep:         7,
	}
}

// Get returns the effective settings.
func (s *Settings) Get(ctx context.Context) (PanelSettings, error) {
	s.mu.RLock()
	if s.loaded && s.cache != nil {
		out := *s.cache
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	out := s.Defaults()
	db := s.db.WithContext(ctx)

	var rows []model.Setting
	if err := db.Find(&rows).Error; err != nil {
		return out, fmt.Errorf("service: read settings: %w", err)
	}
	stored := make(map[string]string, len(rows))
	for _, r := range rows {
		stored[r.Key] = r.Value
	}

	// A stored value that will not parse is ignored rather than fatal. One bad
	// row must not stop the panel from starting.
	out.SessionHours = intOr(stored[keySessionHours], out.SessionHours)
	if v := strings.TrimSpace(stored[keyDefaultLocale]); v != "" {
		out.DefaultLocale = v
	}
	out.DefaultQuotaBytes = uintOr(stored[keyDefQuotaBytes], out.DefaultQuotaBytes)
	out.DefaultExpiryDays = intOr(stored[keyDefExpiryDays], out.DefaultExpiryDays)
	out.DefaultDeviceLimit = intOr(stored[keyDefDeviceLimit], out.DefaultDeviceLimit)
	out.DefaultRateBitsPerSec = uintOr(stored[keyDefRateBits], out.DefaultRateBitsPerSec)
	if v := strings.TrimSpace(stored[keyDefResetCycle]); v != "" {
		out.DefaultResetCycle = v
	}
	out.DefaultInterfaceID = uint(uintOr(stored[keyDefInterfaceID], uint64(out.DefaultInterfaceID)))

	out.NotifyEnabled = stored[keyNotifyEnabled] == "true"
	out.NotifyChatID = stored[keyNotifyChat]
	out.NotifyBotToken = stored[keyNotifyToken]
	if v := strings.TrimSpace(stored[keyNotifyKinds]); v != "" {
		out.NotifyKinds = strings.Split(v, ",")
	}
	out.BackupEveryHours = intOr(stored[keyBackupEvery], out.BackupEveryHours)
	out.BackupKeep = intOr(stored[keyBackupKeep], out.BackupKeep)

	s.mu.Lock()
	s.cache, s.loaded = &out, true
	s.mu.Unlock()
	return out, nil
}

// Save validates and stores the settings.
func (s *Settings) Save(ctx context.Context, in PanelSettings) (PanelSettings, error) {
	if err := s.validate(&in); err != nil {
		return PanelSettings{}, err
	}

	db := s.db.WithContext(ctx)
	values := map[string]string{
		keySessionHours:   strconv.Itoa(in.SessionHours),
		keyDefaultLocale:  in.DefaultLocale,
		keyDefQuotaBytes:  strconv.FormatUint(in.DefaultQuotaBytes, 10),
		keyDefExpiryDays:  strconv.Itoa(in.DefaultExpiryDays),
		keyDefDeviceLimit: strconv.Itoa(in.DefaultDeviceLimit),
		keyDefRateBits:    strconv.FormatUint(in.DefaultRateBitsPerSec, 10),
		keyDefResetCycle:  in.DefaultResetCycle,
		keyDefInterfaceID: strconv.FormatUint(uint64(in.DefaultInterfaceID), 10),
		keyNotifyEnabled:  strconv.FormatBool(in.NotifyEnabled),
		keyNotifyChat:     in.NotifyChatID,
		keyNotifyKinds:    strings.Join(in.NotifyKinds, ","),
		keyBackupEvery:    strconv.Itoa(in.BackupEveryHours),
		keyBackupKeep:     strconv.Itoa(in.BackupKeep),
	}

	// The placeholder means the operator did not retype the token, so the
	// stored one stays. Writing the placeholder itself would silently break
	// notifications the next time they saved any unrelated setting.
	if in.NotifyBotToken != TokenPlaceholder {
		values[keyNotifyToken] = in.NotifyBotToken
	}

	// One transaction: a half-saved settings page would leave the panel in a
	// state the operator never chose.
	err := db.Transaction(func(tx *gorm.DB) error {
		for k, v := range values {
			if err := database.PutSetting(tx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return PanelSettings{}, fmt.Errorf("service: save settings: %w", err)
	}

	s.mu.Lock()
	s.cache, s.loaded = &in, true
	s.mu.Unlock()
	return in, nil
}

func (s *Settings) validate(in *PanelSettings) error {
	if in.SessionHours <= 0 || in.SessionHours > maxSessionHours {
		return fmt.Errorf("%w: session length must be between 1 and %d hours",
			ErrInvalid, maxSessionHours)
	}
	switch in.DefaultLocale {
	case "en", "fa":
	default:
		return fmt.Errorf("%w: unknown language %q", ErrInvalid, in.DefaultLocale)
	}
	if in.DefaultDeviceLimit < 1 || in.DefaultDeviceLimit > maxDeviceLimit {
		return fmt.Errorf("%w: device limit must be between 1 and %d",
			ErrInvalid, maxDeviceLimit)
	}
	if in.DefaultExpiryDays < 0 || in.DefaultExpiryDays > maxExpiryDays {
		return fmt.Errorf("%w: expiry must be between 0 and %d days", ErrInvalid, maxExpiryDays)
	}
	switch model.ResetCycle(in.DefaultResetCycle) {
	case model.ResetNone, model.ResetDaily, model.ResetWeekly, model.ResetMonthly:
	default:
		return fmt.Errorf("%w: unknown reset cycle %q", ErrInvalid, in.DefaultResetCycle)
	}
	if in.BackupEveryHours < 0 || in.BackupEveryHours > 24*30 {
		return fmt.Errorf("%w: backup interval must be between 0 and %d hours",
			ErrInvalid, 24*30)
	}
	if in.BackupKeep < 0 || in.BackupKeep > 365 {
		return fmt.Errorf("%w: keep between 0 and 365 backups", ErrInvalid)
	}
	if in.NotifyEnabled && strings.TrimSpace(in.NotifyChatID) == "" {
		return fmt.Errorf("%w: notifications need a chat id", ErrInvalid)
	}
	return nil
}

// Notify projects the stored settings into what the notifier takes.
func (s *Settings) Notify(ctx context.Context) notify.Config {
	got, err := s.Get(ctx)
	if err != nil {
		return notify.Config{}
	}
	kinds := make(map[string]bool, len(got.NotifyKinds))
	for _, k := range got.NotifyKinds {
		if k = strings.TrimSpace(k); k != "" {
			kinds[k] = true
		}
	}
	return notify.Config{
		Enabled:  got.NotifyEnabled,
		BotToken: got.NotifyBotToken,
		ChatID:   got.NotifyChatID,
		Kinds:    kinds,
	}
}

// SessionTTLHours is the hot path used on every sign-in.
func (s *Settings) SessionTTLHours(ctx context.Context) int {
	got, err := s.Get(ctx)
	if err != nil || got.SessionHours <= 0 {
		return defaultSessionHrs
	}
	return got.SessionHours
}

func intOr(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return n
}

func uintOr(raw string, fallback uint64) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return n
}
