package service

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/notify"
)

// Panel settings live in the database rather than in the environment.
//
// The environment still says where to listen and where the data lives, and it
// still wins on a fresh install; what an operator saves here is read once at
// start and laid over it, which is why the page says to restart the panel to
// apply. Everything else -- how long a session lasts, what a new customer
// starts from, where to send a notification -- is consulted live.

// Setting keys. They are namespaced so an operator reading the table can tell
// what a row belongs to.
const (
	keyWebListen      = "panel.listen"
	keyWebDomain      = "panel.domain"
	keyWebPort        = "panel.port"
	keyWebBasePath    = "panel.basePath"
	keySessionHours   = "panel.sessionHours" // the old unit, read for migration
	keySessionMinutes = "panel.sessionMinutes"
	keyTrustedProxies = "panel.trustedProxies"
	keyPanelOutbound  = "panel.outbound"
	keyPageSize       = "panel.pageSize"
	keyDefaultLocale  = "panel.defaultLocale"
	keyWebCertFile    = "panel.certFile"
	keyWebKeyFile     = "panel.keyFile"
	keyTimeLocation   = "panel.timeLocation"
	keyDatepicker     = "panel.datepicker"
	keyExpireDiff     = "panel.expireDiffDays"
	keyTrafficDiff    = "panel.trafficDiffGB"
	keyInformEnable   = "panel.informEnable"
	keyInformURI      = "panel.informURI"

	keyDefQuotaBytes  = "client.defaultQuotaBytes"
	keyDefExpiryDays  = "client.defaultExpiryDays"
	keyDefDeviceLimit = "client.defaultDeviceLimit"
	keyDefRateBits    = "client.defaultRateBitsPerSec"
	keyDefResetCycle  = "client.defaultResetCycle"
	keyDefInterfaceID = "client.defaultInterfaceId"

	keyNotifyEnabled   = "notify.enabled"
	keyNotifyToken     = "notify.botToken"
	keyNotifyChat      = "notify.chatId"
	keyNotifyKinds     = "notify.kinds"
	keyNotifyLang      = "notify.lang"
	keyNotifyAPIServer = "notify.apiServer"
	keyNotifyRunTime   = "notify.runTime"
	keyNotifyBackup    = "notify.backup"
	keyNotifyCPU       = "notify.cpuThreshold"
	keyNotifyMemory    = "notify.memoryThreshold"
	keyNotifyOutDown   = "notify.outboundDownThreshold"

	keyBackupEvery = "backup.everyHours"
	keyBackupKeep  = "backup.keep"

	keyMailEnabled    = "mail.enabled"
	keyMailHost       = "mail.host"
	keyMailPort       = "mail.port"
	keyMailUsername   = "mail.username"
	keyMailPassword   = "mail.password"
	keyMailFrom       = "mail.from"
	keyMailFromName   = "mail.fromName"
	keyMailTo         = "mail.to"
	keyMailEncryption = "mail.encryption"
	keyMailKinds      = "mail.kinds"

	maxSessionMinutes  = 60 * 24 * 365
	maxDeviceLimit     = 64
	maxExpiryDays      = 365 * 10
	defaultSessionMins = 12 * 60
	defaultDeviceLimit = 1
	defaultPageSize    = 25
)

// PanelSettings is everything the settings page can change.
type PanelSettings struct {
	// Where the panel answers. Applied at the next start; empty leaves the
	// environment's value alone.
	WebListen   string `json:"webListen"`
	WebDomain   string `json:"webDomain"`
	WebPort     int    `json:"webPort"`
	WebBasePath string `json:"webBasePath"`
	WebCertFile string `json:"webCertFile"`
	WebKeyFile  string `json:"webKeyFile"`
	// TrustedProxyCIDRs is who may set forwarded headers, comma separated.
	TrustedProxyCIDRs string `json:"trustedProxyCIDRs"`

	// SessionMaxAge is how long a sign-in lasts, in minutes.
	SessionMaxAge int    `json:"sessionMaxAge"`
	DefaultLocale string `json:"defaultLocale"`
	// PanelOutbound is the outbound the panel's own traffic leaves through;
	// empty is direct.
	PanelOutbound string `json:"panelOutbound"`
	// PageSize is how many rows a table page holds; 0 shows everything.
	PageSize int `json:"pageSize"`

	// TimeLocation is the zone scheduled work runs in; Datepicker the
	// calendar dates are shown in.
	TimeLocation string `json:"timeLocation"`
	Datepicker   string `json:"datepicker"`

	// ExpireDiff and TrafficDiff are the thresholds -- days and GB left --
	// at which a customer counts as running out.
	ExpireDiff  int `json:"expireDiff"`
	TrafficDiff int `json:"trafficDiff"`

	// External traffic informing: each collection is POSTed to the URI.
	ExternalTrafficInformEnable bool   `json:"externalTrafficInformEnable"`
	ExternalTrafficInformURI    string `json:"externalTrafficInformURI"`

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
	NotifyLang     string `json:"notifyLang"`
	// NotifyAPIServer replaces api.telegram.org; empty is Telegram's own.
	NotifyAPIServer string `json:"notifyAPIServer"`
	// NotifyRunTime is when the periodic report goes: a crontab line, or one
	// of @hourly, @daily, @weekly, @monthly, @every 30m.
	NotifyRunTime string `json:"notifyRunTime"`
	// NotifyBackup attaches the database to the report.
	NotifyBackup bool `json:"notifyBackup"`
	// Thresholds, in percent, for the system and outbound events.
	NotifyCPUThreshold          int `json:"notifyCPUThreshold"`
	NotifyMemoryThreshold       int `json:"notifyMemoryThreshold"`
	NotifyOutboundDownThreshold int `json:"notifyOutboundDownThreshold"`

	BackupEveryHours int `json:"backupEveryHours"`
	BackupKeep       int `json:"backupKeep"`

	// Mail is the second way an event can reach an operator, and it carries
	// its own list of what to send: the point of a second channel is that it
	// can be set to carry less.
	MailEnabled    bool     `json:"mailEnabled"`
	MailHost       string   `json:"mailHost"`
	MailPort       int      `json:"mailPort"`
	MailUsername   string   `json:"mailUsername"`
	MailFrom       string   `json:"mailFrom"`
	MailFromName   string   `json:"mailFromName"`
	MailTo         string   `json:"mailTo"`
	MailEncryption string   `json:"mailEncryption"`
	MailKinds      []string `json:"mailKinds"`
	// MailPassword is write-only, like the bot token: returned as a
	// placeholder so the page can show one is set without handing it back on
	// every page load.
	MailPassword string `json:"mailPassword"`
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
		SessionMaxAge:               defaultSessionMins,
		DefaultLocale:               s.locale,
		PageSize:                    defaultPageSize,
		Datepicker:                  "gregorian",
		ExpireDiff:                  3,
		TrafficDiff:                 1,
		DefaultDeviceLimit:          defaultDeviceLimit,
		DefaultResetCycle:           string(model.ResetNone),
		NotifyKinds:                 []string{},
		NotifyLang:                  "en",
		NotifyRunTime:               "@daily",
		NotifyCPUThreshold:          80,
		NotifyMemoryThreshold:       80,
		NotifyOutboundDownThreshold: 50,
		BackupEveryHours:            24,
		BackupKeep:                  7,
		MailPort:                    587,
		MailEncryption:              string(notify.EncryptionStartTLS),
		MailKinds:                   []string{},
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
	str := func(key string, dst *string) {
		if v, ok := stored[key]; ok {
			*dst = v
		}
	}
	list := func(key string, dst *[]string) {
		if v := strings.TrimSpace(stored[key]); v != "" {
			*dst = strings.Split(v, ",")
		}
	}

	// A stored value that will not parse is ignored rather than fatal. One bad
	// row must not stop the panel from starting.
	str(keyWebListen, &out.WebListen)
	str(keyWebDomain, &out.WebDomain)
	out.WebPort = intOr(stored[keyWebPort], out.WebPort)
	str(keyWebBasePath, &out.WebBasePath)
	str(keyWebCertFile, &out.WebCertFile)
	str(keyWebKeyFile, &out.WebKeyFile)
	str(keyTrustedProxies, &out.TrustedProxyCIDRs)
	// Minutes, or the hours a panel from before this unit had saved.
	if _, ok := stored[keySessionMinutes]; ok {
		out.SessionMaxAge = intOr(stored[keySessionMinutes], out.SessionMaxAge)
	} else if h := intOr(stored[keySessionHours], 0); h > 0 {
		out.SessionMaxAge = h * 60
	}
	if v := strings.TrimSpace(stored[keyDefaultLocale]); v != "" {
		out.DefaultLocale = v
	}
	str(keyPanelOutbound, &out.PanelOutbound)
	out.PageSize = intOr(stored[keyPageSize], out.PageSize)
	str(keyTimeLocation, &out.TimeLocation)
	if v := strings.TrimSpace(stored[keyDatepicker]); v != "" {
		out.Datepicker = v
	}
	out.ExpireDiff = intOr(stored[keyExpireDiff], out.ExpireDiff)
	out.TrafficDiff = intOr(stored[keyTrafficDiff], out.TrafficDiff)
	out.ExternalTrafficInformEnable = stored[keyInformEnable] == "true"
	str(keyInformURI, &out.ExternalTrafficInformURI)

	out.DefaultQuotaBytes = uintOr(stored[keyDefQuotaBytes], out.DefaultQuotaBytes)
	out.DefaultExpiryDays = intOr(stored[keyDefExpiryDays], out.DefaultExpiryDays)
	out.DefaultDeviceLimit = intOr(stored[keyDefDeviceLimit], out.DefaultDeviceLimit)
	out.DefaultRateBitsPerSec = uintOr(stored[keyDefRateBits], out.DefaultRateBitsPerSec)
	if v := strings.TrimSpace(stored[keyDefResetCycle]); v != "" {
		out.DefaultResetCycle = v
	}
	out.DefaultInterfaceID = uint(uintOr(stored[keyDefInterfaceID], uint64(out.DefaultInterfaceID)))

	out.NotifyEnabled = stored[keyNotifyEnabled] == "true"
	str(keyNotifyChat, &out.NotifyChatID)
	str(keyNotifyToken, &out.NotifyBotToken)
	list(keyNotifyKinds, &out.NotifyKinds)
	if v := strings.TrimSpace(stored[keyNotifyLang]); v != "" {
		out.NotifyLang = v
	}
	str(keyNotifyAPIServer, &out.NotifyAPIServer)
	if v := strings.TrimSpace(stored[keyNotifyRunTime]); v != "" {
		out.NotifyRunTime = v
	}
	out.NotifyBackup = stored[keyNotifyBackup] == "true"
	out.NotifyCPUThreshold = intOr(stored[keyNotifyCPU], out.NotifyCPUThreshold)
	out.NotifyMemoryThreshold = intOr(stored[keyNotifyMemory], out.NotifyMemoryThreshold)
	out.NotifyOutboundDownThreshold = intOr(stored[keyNotifyOutDown], out.NotifyOutboundDownThreshold)

	out.BackupEveryHours = intOr(stored[keyBackupEvery], out.BackupEveryHours)
	out.BackupKeep = intOr(stored[keyBackupKeep], out.BackupKeep)

	out.MailEnabled = stored[keyMailEnabled] == "true"
	str(keyMailHost, &out.MailHost)
	out.MailPort = intOr(stored[keyMailPort], out.MailPort)
	str(keyMailUsername, &out.MailUsername)
	str(keyMailPassword, &out.MailPassword)
	str(keyMailFrom, &out.MailFrom)
	str(keyMailFromName, &out.MailFromName)
	str(keyMailTo, &out.MailTo)
	if v := strings.TrimSpace(stored[keyMailEncryption]); v != "" {
		out.MailEncryption = v
	}
	list(keyMailKinds, &out.MailKinds)

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
		keyWebListen:       strings.TrimSpace(in.WebListen),
		keyWebDomain:       strings.TrimSpace(in.WebDomain),
		keyWebPort:         strconv.Itoa(in.WebPort),
		keyWebBasePath:     in.WebBasePath,
		keyWebCertFile:     strings.TrimSpace(in.WebCertFile),
		keyWebKeyFile:      strings.TrimSpace(in.WebKeyFile),
		keyTrustedProxies:  in.TrustedProxyCIDRs,
		keySessionMinutes:  strconv.Itoa(in.SessionMaxAge),
		keyDefaultLocale:   in.DefaultLocale,
		keyPanelOutbound:   strings.TrimSpace(in.PanelOutbound),
		keyPageSize:        strconv.Itoa(in.PageSize),
		keyTimeLocation:    strings.TrimSpace(in.TimeLocation),
		keyDatepicker:      in.Datepicker,
		keyExpireDiff:      strconv.Itoa(in.ExpireDiff),
		keyTrafficDiff:     strconv.Itoa(in.TrafficDiff),
		keyInformEnable:    strconv.FormatBool(in.ExternalTrafficInformEnable),
		keyInformURI:       strings.TrimSpace(in.ExternalTrafficInformURI),
		keyDefQuotaBytes:   strconv.FormatUint(in.DefaultQuotaBytes, 10),
		keyDefExpiryDays:   strconv.Itoa(in.DefaultExpiryDays),
		keyDefDeviceLimit:  strconv.Itoa(in.DefaultDeviceLimit),
		keyDefRateBits:     strconv.FormatUint(in.DefaultRateBitsPerSec, 10),
		keyDefResetCycle:   in.DefaultResetCycle,
		keyDefInterfaceID:  strconv.FormatUint(uint64(in.DefaultInterfaceID), 10),
		keyNotifyEnabled:   strconv.FormatBool(in.NotifyEnabled),
		keyNotifyChat:      in.NotifyChatID,
		keyNotifyKinds:     strings.Join(in.NotifyKinds, ","),
		keyNotifyLang:      in.NotifyLang,
		keyNotifyAPIServer: strings.TrimSpace(in.NotifyAPIServer),
		keyNotifyRunTime:   strings.TrimSpace(in.NotifyRunTime),
		keyNotifyBackup:    strconv.FormatBool(in.NotifyBackup),
		keyNotifyCPU:       strconv.Itoa(in.NotifyCPUThreshold),
		keyNotifyMemory:    strconv.Itoa(in.NotifyMemoryThreshold),
		keyNotifyOutDown:   strconv.Itoa(in.NotifyOutboundDownThreshold),
		keyBackupEvery:     strconv.Itoa(in.BackupEveryHours),
		keyBackupKeep:      strconv.Itoa(in.BackupKeep),
		keyMailEnabled:     strconv.FormatBool(in.MailEnabled),
		keyMailHost:        in.MailHost,
		keyMailPort:        strconv.Itoa(in.MailPort),
		keyMailUsername:    in.MailUsername,
		keyMailFrom:        in.MailFrom,
		keyMailFromName:    in.MailFromName,
		keyMailTo:          in.MailTo,
		keyMailEncryption:  in.MailEncryption,
		keyMailKinds:       strings.Join(in.MailKinds, ","),
	}

	// The placeholder means the operator did not retype the token, so the
	// stored one stays. Writing the placeholder itself would silently break
	// notifications the next time they saved any unrelated setting.
	cur, _ := s.Get(ctx)
	if in.NotifyBotToken != TokenPlaceholder {
		values[keyNotifyToken] = in.NotifyBotToken
	} else {
		in.NotifyBotToken = cur.NotifyBotToken
	}
	if in.MailPassword != TokenPlaceholder {
		values[keyMailPassword] = in.MailPassword
	} else {
		in.MailPassword = cur.MailPassword
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
	if in.SessionMaxAge <= 0 || in.SessionMaxAge > maxSessionMinutes {
		return fmt.Errorf("%w: session duration must be between 1 and %d minutes",
			ErrInvalid, maxSessionMinutes)
	}
	if in.WebPort < 0 || in.WebPort > 65535 {
		return fmt.Errorf("%w: panel port %d is out of range", ErrInvalid, in.WebPort)
	}
	if v := strings.TrimSpace(in.WebListen); v != "" {
		if _, err := netip.ParseAddr(v); err != nil {
			return fmt.Errorf("%w: listen IP %q is not an address", ErrInvalid, v)
		}
	}
	in.WebBasePath = normalizeBasePath(in.WebBasePath)
	if inner := strings.Trim(in.WebBasePath, "/"); strings.Contains(inner, "/") {
		return fmt.Errorf("%w: the URI path is one segment, like /panel/", ErrInvalid)
	}
	for _, e := range strings.Split(in.TrustedProxyCIDRs, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, err := netip.ParsePrefix(e); err != nil {
			if _, err := netip.ParseAddr(e); err != nil {
				return fmt.Errorf("%w: trusted proxy %q is not an address or a CIDR", ErrInvalid, e)
			}
		}
	}
	switch in.DefaultLocale {
	case "en", "fa":
	default:
		return fmt.Errorf("%w: unknown language %q", ErrInvalid, in.DefaultLocale)
	}
	if in.PageSize < 0 || in.PageSize > 1000 {
		return fmt.Errorf("%w: page size must be between 0 and 1000", ErrInvalid)
	}
	if v := strings.TrimSpace(in.TimeLocation); v != "" {
		if _, err := time.LoadLocation(v); err != nil {
			return fmt.Errorf("%w: unknown time zone %q", ErrInvalid, v)
		}
	}
	switch in.Datepicker {
	case "", "gregorian", "jalalian":
	default:
		return fmt.Errorf("%w: unknown calendar %q", ErrInvalid, in.Datepicker)
	}
	if in.ExpireDiff < 0 || in.TrafficDiff < 0 {
		return fmt.Errorf("%w: notification thresholds cannot be negative", ErrInvalid)
	}
	if in.ExternalTrafficInformEnable {
		u := strings.TrimSpace(in.ExternalTrafficInformURI)
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("%w: the external traffic URI must begin with http:// or https://", ErrInvalid)
		}
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
	switch notify.Encryption(in.MailEncryption) {
	case "", notify.EncryptionStartTLS, notify.EncryptionTLS, notify.EncryptionNone:
	default:
		return fmt.Errorf("%w: unknown mail encryption %q", ErrInvalid, in.MailEncryption)
	}
	if in.MailPort < 0 || in.MailPort > 65535 {
		return fmt.Errorf("%w: mail port %d is out of range", ErrInvalid, in.MailPort)
	}
	// Checked when it is switched on rather than when it is filled in, so a
	// half-completed form can still be saved and come back to.
	if in.MailEnabled {
		switch {
		case strings.TrimSpace(in.MailHost) == "":
			return fmt.Errorf("%w: a mail server is required to send email", ErrInvalid)
		case strings.TrimSpace(in.MailFrom) == "" && strings.TrimSpace(in.MailUsername) == "":
			return fmt.Errorf("%w: a from address is required to send email", ErrInvalid)
		case strings.TrimSpace(in.MailTo) == "":
			return fmt.Errorf("%w: at least one recipient is required to send email", ErrInvalid)
		}
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
	switch in.NotifyLang {
	case "", "en", "fa":
	default:
		return fmt.Errorf("%w: unknown bot language %q", ErrInvalid, in.NotifyLang)
	}
	if v := strings.TrimSpace(in.NotifyAPIServer); v != "" &&
		!strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		return fmt.Errorf("%w: the Telegram API server must begin with http:// or https://", ErrInvalid)
	}
	if v := strings.TrimSpace(in.NotifyRunTime); v != "" {
		if _, err := notify.ParseSchedule(v); err != nil {
			return fmt.Errorf("%w: notification time: %v", ErrInvalid, err)
		}
	}
	for _, p := range []int{in.NotifyCPUThreshold, in.NotifyMemoryThreshold, in.NotifyOutboundDownThreshold} {
		if p < 0 || p > 100 {
			return fmt.Errorf("%w: a threshold is a percentage, 0 to 100", ErrInvalid)
		}
	}
	return nil
}

// normalizeBasePath makes a path begin and end with a slash, the way the classic panel
// keeps its URI path. Empty stays empty, which means the environment's.
func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return ""
	}
	return "/" + strings.Trim(p, "/") + "/"
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
		Enabled:   got.NotifyEnabled,
		BotToken:  got.NotifyBotToken,
		ChatID:    got.NotifyChatID,
		Kinds:     kinds,
		Lang:      got.NotifyLang,
		APIServer: got.NotifyAPIServer,
		RunTime:   got.NotifyRunTime,
		Backup:    got.NotifyBackup,
		Thresholds: notify.Thresholds{
			CPU:          got.NotifyCPUThreshold,
			Memory:       got.NotifyMemoryThreshold,
			OutboundDown: got.NotifyOutboundDownThreshold,
			ExpireDays:   got.ExpireDiff,
			TrafficGB:    got.TrafficDiff,
		},
	}
}

// Mail is the email half of the notification settings.
func (s *Settings) Mail(ctx context.Context) notify.MailConfig {
	got, err := s.Get(ctx)
	if err != nil {
		return notify.MailConfig{}
	}
	kinds := make(map[string]bool, len(got.MailKinds))
	for _, k := range got.MailKinds {
		if k = strings.TrimSpace(k); k != "" {
			kinds[k] = true
		}
	}
	return notify.MailConfig{
		Enabled:    got.MailEnabled,
		Host:       got.MailHost,
		Port:       got.MailPort,
		Username:   got.MailUsername,
		Password:   got.MailPassword,
		From:       got.MailFrom,
		FromName:   got.MailFromName,
		To:         got.MailTo,
		Encryption: notify.Encryption(got.MailEncryption),
		Kinds:      kinds,
	}
}

// SessionTTL is the hot path used on every sign-in.
func (s *Settings) SessionTTL(ctx context.Context) time.Duration {
	got, err := s.Get(ctx)
	if err != nil || got.SessionMaxAge <= 0 {
		return defaultSessionMins * time.Minute
	}
	return time.Duration(got.SessionMaxAge) * time.Minute
}

// Overrides is what the saved settings lay over the process configuration at
// start. Every field is empty when the operator has not chosen one.
type Overrides struct {
	Listen         string
	BasePath       string
	TLSCert        string
	TLSKey         string
	TrustedProxies string
	TimeLocation   string
}

// Overrides reads what an operator saved for the next start.
func (s *Settings) Overrides(ctx context.Context) Overrides {
	got, err := s.Get(ctx)
	if err != nil {
		return Overrides{}
	}
	var o Overrides
	if got.WebListen != "" || got.WebPort > 0 {
		host := got.WebListen
		port := got.WebPort
		if port == 0 {
			port = 2096
		}
		o.Listen = netJoin(host, port)
	}
	o.BasePath = got.WebBasePath
	if got.WebCertFile != "" && got.WebKeyFile != "" {
		o.TLSCert, o.TLSKey = got.WebCertFile, got.WebKeyFile
	}
	o.TrustedProxies = got.TrustedProxyCIDRs
	o.TimeLocation = got.TimeLocation
	return o
}

func netJoin(host string, port int) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
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
