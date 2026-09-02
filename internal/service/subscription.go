package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Setting keys for the subscription service.
const (
	keySubEnabled  = "sub.enabled"
	keySubPath     = "sub.path"
	keySubHost     = "sub.host"
	keySubTitle    = "sub.title"
	keySubInterval = "sub.updateHours"
	keySubProxyURI = "sub.reverseProxyUri"
)

// DefaultSubPath is where the subscription service answers when nothing else
// is configured.
//
// Deliberately not "/sub/". Every panel of this kind uses that, so it is the
// first thing anybody scanning for one tries, and a path that is guessable
// turns "you need the link" into "you need to have looked". The security
// warnings say so if an operator sets it back.
const DefaultSubPath = "/subscribe/"

// Subscriptions serves customers their own configuration over a link.
//
// A configuration file emailed once is a configuration file that is out of date
// the moment anything changes — a new device, a moved endpoint, a rotated key.
// A subscription link is fetched by the customer's client on a schedule, so the
// panel stays the single source of what they should be using.
type Subscriptions struct {
	db    *gorm.DB
	pool  *backend.Pool
	hosts *Hosts
	log   *slog.Logger
}

func NewSubscriptions(
	db *gorm.DB, pool *backend.Pool, hosts *Hosts, log *slog.Logger,
) *Subscriptions {
	return &Subscriptions{db: db, pool: pool, hosts: hosts, log: log}
}

// SubSettings is what the settings page can change.
type SubSettings struct {
	Enabled bool `json:"enabled"`
	// Path is where the service answers, beginning and ending with a slash.
	Path string `json:"path"`
	// Host is what goes into the link handed to a customer. Empty means the
	// address they reached the panel on, which is right for most installs and
	// wrong for one behind a proxy.
	Host string `json:"host"`
	// Title is shown by the client app as the profile name.
	Title string `json:"title"`
	// UpdateHours is what the client app is told about how often to refetch.
	UpdateHours int `json:"updateHours"`
	// ReverseProxyURI is the public prefix when the panel sits behind one.
	ReverseProxyURI string `json:"reverseProxyUri"`
}

// SubDefaults is the shape a panel that has never been configured has.
func (s *Subscriptions) Defaults() SubSettings {
	return SubSettings{
		Enabled:     false,
		Path:        DefaultSubPath,
		Title:       "W-UI",
		UpdateHours: 12,
	}
}

// Settings reads the configuration.
func (s *Subscriptions) Settings(ctx context.Context) (SubSettings, error) {
	out := s.Defaults()

	var rows []model.Setting
	if err := s.db.WithContext(ctx).Where("key LIKE ?", "sub.%").
		Find(&rows).Error; err != nil {
		return out, fmt.Errorf("service: read subscription settings: %w", err)
	}
	stored := make(map[string]string, len(rows))
	for _, r := range rows {
		stored[r.Key] = r.Value
	}

	out.Enabled = stored[keySubEnabled] == "true"
	if v := strings.TrimSpace(stored[keySubPath]); v != "" {
		out.Path = v
	}
	out.Host = strings.TrimSpace(stored[keySubHost])
	if v := strings.TrimSpace(stored[keySubTitle]); v != "" {
		out.Title = v
	}
	out.UpdateHours = intOr(stored[keySubInterval], out.UpdateHours)
	out.ReverseProxyURI = strings.TrimSpace(stored[keySubProxyURI])
	return out, nil
}

// SaveSettings validates and stores them.
func (s *Subscriptions) SaveSettings(ctx context.Context, in SubSettings) (SubSettings, error) {
	in.Path = normalisePath(in.Path)
	if err := checkSubPath(in.Path); err != nil {
		return SubSettings{}, err
	}
	if in.UpdateHours < 1 || in.UpdateHours > 24*7 {
		return SubSettings{}, invalidField("updateHours",
			"an update interval of %d hours is outside the useful range of 1 to 168", in.UpdateHours)
	}
	in.Title = strings.TrimSpace(in.Title)
	if len(in.Title) > 64 {
		return SubSettings{}, invalidField("title", "that title is too long")
	}
	if in.ReverseProxyURI != "" && !strings.HasPrefix(in.ReverseProxyURI, "http") {
		return SubSettings{}, invalidField("reverseProxyUri",
			"this should be the full public address, beginning with http:// or https://")
	}

	writes := map[string]string{
		keySubEnabled:  strconv.FormatBool(in.Enabled),
		keySubPath:     in.Path,
		keySubHost:     strings.TrimSpace(in.Host),
		keySubTitle:    in.Title,
		keySubInterval: strconv.Itoa(in.UpdateHours),
		keySubProxyURI: strings.TrimRight(in.ReverseProxyURI, "/"),
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for k, v := range writes {
			row := model.Setting{Key: k, Value: v, UpdatedAt: time.Now().UTC()}
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return SubSettings{}, fmt.Errorf("service: save subscription settings: %w", err)
	}

	s.log.Info("subscription settings changed", "enabled", in.Enabled, "path", in.Path)
	return s.Settings(ctx)
}

// EnsureToken gives a client a subscription token if it has none.
//
// The token is the whole of the secret: anyone holding the link gets the
// configuration. It is 192 bits from the system generator, which is more than a
// URL needs but costs nothing, and it is stored rather than derived so that
// rotating it is a one-row update and not a migration.
func (s *Subscriptions) EnsureToken(ctx context.Context, clientID uint) (string, error) {
	var c model.Client
	if err := s.db.WithContext(ctx).First(&c, clientID).Error; err != nil {
		return "", fmt.Errorf("%w: no client %d", ErrNotFound, clientID)
	}
	if c.SubToken != "" {
		return c.SubToken, nil
	}
	token, err := newSubToken()
	if err != nil {
		return "", err
	}
	if err := s.db.WithContext(ctx).Model(&c).Update("sub_token", token).Error; err != nil {
		return "", fmt.Errorf("service: store subscription token: %w", err)
	}
	return token, nil
}

// RotateToken issues a new token, which makes every existing link stop working.
func (s *Subscriptions) RotateToken(ctx context.Context, clientID uint) (string, error) {
	var c model.Client
	if err := s.db.WithContext(ctx).First(&c, clientID).Error; err != nil {
		return "", fmt.Errorf("%w: no client %d", ErrNotFound, clientID)
	}
	token, err := newSubToken()
	if err != nil {
		return "", err
	}
	if err := s.db.WithContext(ctx).Model(&c).Update("sub_token", token).Error; err != nil {
		return "", fmt.Errorf("service: rotate subscription token: %w", err)
	}
	s.log.Info("subscription link rotated", "client", c.Name)
	return token, nil
}

// LinkFor builds the address handed to a customer.
func (s *Subscriptions) LinkFor(ctx context.Context, token, requestHost string) (string, error) {
	cfg, err := s.Settings(ctx)
	if err != nil {
		return "", err
	}
	if cfg.ReverseProxyURI != "" {
		return strings.TrimRight(cfg.ReverseProxyURI, "/") + cfg.Path + token, nil
	}
	host := cfg.Host
	if host == "" {
		host = requestHost
	}
	scheme := "http://"
	if strings.HasPrefix(host, "https://") || strings.HasPrefix(host, "http://") {
		scheme = ""
	}
	return scheme + strings.TrimRight(host, "/") + cfg.Path + token, nil
}

// Bundle is what a customer's client fetches.
type Bundle struct {
	// Body is the configuration itself, already in the requested shape.
	Body []byte
	// ContentType is what to serve it as.
	ContentType string
	// Filename is offered for a browser that saves rather than subscribes.
	Filename string
	// UserInfo is the header client apps read to show quota and expiry.
	UserInfo string
	// Title is the profile name the client app displays.
	Title string
	// UpdateHours is what the client is told about refetching.
	UpdateHours int
}

// Serve renders one client's subscription.
//
// A client that is out of data or past its expiry still gets an answer. Serving
// a 404 would look to the customer's app exactly like the server being down,
// and the header below is what lets their app say "you have run out" instead.
func (s *Subscriptions) Serve(ctx context.Context, token, format string) (*Bundle, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: no subscription token", ErrNotFound)
	}

	var c model.Client
	err := s.db.WithContext(ctx).Preload("Accounts").
		Where("sub_token = ?", token).Limit(1).Find(&c).Error
	if err != nil {
		return nil, fmt.Errorf("service: read subscription: %w", err)
	}
	if c.ID == 0 {
		// Deliberately the same answer as a token that never existed.
		return nil, fmt.Errorf("%w: no such subscription", ErrNotFound)
	}

	cfg, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}

	var ifaces []model.Interface
	if err := s.db.WithContext(ctx).Find(&ifaces).Error; err != nil {
		return nil, fmt.Errorf("service: read interfaces: %w", err)
	}
	byID := make(map[uint]model.Interface, len(ifaces))
	for i := range ifaces {
		byID[ifaces[i].ID] = ifaces[i]
	}

	var parts [][]byte
	for i := range c.Accounts {
		acc := c.Accounts[i]
		iface, ok := byID[acc.InterfaceID]
		if !ok {
			continue
		}
		drv, ok := s.pool.Get(acc.InterfaceID)
		if !ok {
			// The interface exists in the database but its driver did not open,
			// which is normal on a host that cannot serve that protocol. Skipped
			// rather than failing the whole bundle: the customer's other devices
			// should still work.
			continue
		}
		profile, err := drv.Render(ctx, &acc, &iface)
		if err != nil {
			s.log.Warn("could not render a configuration for a subscription",
				"client", c.Name, "account", acc.ID, "error", err)
			continue
		}
		parts = append(parts, profile.Body)
	}

	// An empty body with a 200 is the worst available answer: the customer's app
	// accepts it, shows a subscription with nothing in it, and gives them
	// nothing to report but "it stopped working". A client that genuinely has no
	// devices is a different thing from one whose drivers are down, and the two
	// are distinguished here so the operator sees the real cause in the log.
	if len(parts) == 0 {
		if len(c.Accounts) == 0 {
			return nil, fmt.Errorf("%w: %s has no devices to configure", ErrInvalid, c.Name)
		}
		s.log.Error("a subscription could not be rendered",
			"client", c.Name, "devices", len(c.Accounts),
			"reason", "no driver produced a configuration; the interfaces are probably not open")
		return nil, fmt.Errorf(
			"service: no configuration could be produced for %s: its interfaces are not running",
			c.Name)
	}

	body, ctype := encodeBundle(parts, format)

	return &Bundle{
		Body:        body,
		ContentType: ctype,
		Filename:    safeFilename(c.Name) + configExt(format),
		UserInfo:    userInfo(&c),
		Title:       cfg.Title,
		UpdateHours: cfg.UpdateHours,
	}, nil
}

// encodeBundle puts the configurations into the shape asked for.
func encodeBundle(parts [][]byte, format string) (body []byte, contentType string) {
	joined := []byte(strings.Join(asStrings(parts), "\n\n"))

	switch strings.ToLower(format) {
	case "base64":
		// What most subscription clients expect. Standard encoding rather than
		// URL-safe: the body is not a URL, and clients decode it as standard.
		enc := base64.StdEncoding.EncodeToString(joined)
		return []byte(enc), "text/plain; charset=utf-8"
	default:
		return joined, "text/plain; charset=utf-8"
	}
}

func asStrings(parts [][]byte) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(string(p)))
	}
	return out
}

func configExt(format string) string {
	if strings.EqualFold(format, "base64") {
		return ".txt"
	}
	return ".conf"
}

// userInfo renders the header client apps read.
//
// The format is the one the subscription clients already understand:
// upload, download and total in bytes, and expire as a unix timestamp with 0
// meaning never. Getting this right is what makes a customer's own app show
// their remaining data, which is the single thing that stops them asking.
func userInfo(c *model.Client) string {
	var expire int64
	if c.ExpiresAt != nil {
		expire = c.ExpiresAt.Unix()
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		c.UpBytes, c.DownBytes, c.QuotaBytes, expire)
}

// safeFilename keeps a customer's name out of the response headers as anything
// but plain characters. A name with a quote or a newline in it would otherwise
// let the customer write their own headers.
func safeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "config"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func newSubToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("service: generate subscription token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func normalisePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return DefaultSubPath
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func checkSubPath(p string) error {
	// The paths the panel already serves. A subscription mounted on one of them
	// would shadow it, and the symptom would be the whole panel breaking rather
	// than anything pointing at this field.
	for _, taken := range []string{"/", "/api/", "/assets/", "/login/"} {
		if p == taken {
			return invalidField("path", "%q is already used by the panel itself", p)
		}
	}
	if strings.HasPrefix(p, "/api/") {
		return invalidField("path", "the path cannot start with /api/, which the panel serves")
	}
	for _, r := range p {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '/' || r == '-' || r == '_' {
			continue
		}
		return invalidField("path",
			"a path can only contain letters, digits, - and _ (found %q)", string(r))
	}
	if len(p) < 3 {
		return invalidField("path", "that path is too short to be worth having")
	}
	return nil
}
