package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"path"
	"path/filepath"
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
	keySubListen   = "sub.listen"
	keySubPort     = "sub.port"
	keySubCertFile = "sub.certFile"
	keySubKeyFile  = "sub.keyFile"
	keySubEncode   = "sub.encode"
	keySubSupport  = "sub.supportUrl"
	keySubProfile  = "sub.profileUrl"
	keySubAnnounce = "sub.announce"
	keySubTemplate = "sub.template"
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

	// Listen and Port put the service on a listener of its own, with its
	// own certificate; empty keeps it on the panel's. Applied at start.
	Listen   string `json:"listen"`
	Port     int    `json:"port"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	// Encode returns text subscriptions base64-encoded, which some apps
	// insist on.
	Encode bool `json:"encode"`
	// What the customer's app shows beside the profile: a support link, a
	// web page, and a notice.
	SupportURL string `json:"supportUrl"`
	ProfileURL string `json:"profileUrl"`
	Announce   string `json:"announce"`
	// Template is the look of the page a customer sees in a browser: one of
	// SubTemplates. The owner picks it; every customer's page follows.
	Template string `json:"template"`
}

// SubTemplates are the looks the subscription page can wear.
var SubTemplates = []string{"classic", "aurora", "waves", "network", "minimal", "midnight"}

func validSubTemplate(v string) bool {
	for _, t := range SubTemplates {
		if t == v {
			return true
		}
	}
	return false
}

// SubDefaults is the shape a panel that has never been configured has.
func (s *Subscriptions) Defaults() SubSettings {
	return SubSettings{
		Enabled:     false,
		Path:        DefaultSubPath,
		Title:       "W-UI",
		UpdateHours: 12,
		Template:    "classic",
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
	out.Listen = strings.TrimSpace(stored[keySubListen])
	out.Port = intOr(stored[keySubPort], 0)
	out.CertFile = strings.TrimSpace(stored[keySubCertFile])
	out.KeyFile = strings.TrimSpace(stored[keySubKeyFile])
	out.Encode = stored[keySubEncode] == "true"
	out.SupportURL = strings.TrimSpace(stored[keySubSupport])
	out.ProfileURL = strings.TrimSpace(stored[keySubProfile])
	out.Announce = stored[keySubAnnounce]
	if v := strings.TrimSpace(stored[keySubTemplate]); validSubTemplate(v) {
		out.Template = v
	}
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
	if in.Port < 0 || in.Port > 65535 {
		return SubSettings{}, invalidField("port", "port %d is out of range", in.Port)
	}
	if v := strings.TrimSpace(in.Listen); v != "" {
		if _, err := netip.ParseAddr(v); err != nil {
			return SubSettings{}, invalidField("listen", "%q is not an IP address", v)
		}
	}
	if (in.CertFile == "") != (in.KeyFile == "") {
		return SubSettings{}, invalidField("certFile", "a certificate and its key go together")
	}
	for name, u := range map[string]string{"supportUrl": in.SupportURL, "profileUrl": in.ProfileURL} {
		if u = strings.TrimSpace(u); u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return SubSettings{}, invalidField(name, "this should be a full address, beginning with http:// or https://")
		}
	}
	in.Template = strings.TrimSpace(in.Template)
	if in.Template == "" {
		in.Template = "classic"
	}
	if !validSubTemplate(in.Template) {
		return SubSettings{}, invalidField("template", "%q is not a template; choose one of %s", in.Template, strings.Join(SubTemplates, ", "))
	}
	if len(in.Announce) > 1000 {
		return SubSettings{}, invalidField("announce", "that notice is too long")
	}

	writes := map[string]string{
		keySubEnabled:  strconv.FormatBool(in.Enabled),
		keySubPath:     in.Path,
		keySubHost:     strings.TrimSpace(in.Host),
		keySubTitle:    in.Title,
		keySubInterval: strconv.Itoa(in.UpdateHours),
		keySubProxyURI: strings.TrimRight(in.ReverseProxyURI, "/"),
		keySubListen:   strings.TrimSpace(in.Listen),
		keySubPort:     strconv.Itoa(in.Port),
		keySubCertFile: strings.TrimSpace(in.CertFile),
		keySubKeyFile:  strings.TrimSpace(in.KeyFile),
		keySubEncode:   strconv.FormatBool(in.Encode),
		keySubSupport:  strings.TrimSpace(in.SupportURL),
		keySubProfile:  strings.TrimSpace(in.ProfileURL),
		keySubAnnounce: strings.TrimSpace(in.Announce),
		keySubTemplate: in.Template,
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
	host := strings.TrimRight(cfg.Host, "/")
	if host == "" {
		host = requestHost
	}
	scheme := ""
	if !strings.HasPrefix(host, "https://") && !strings.HasPrefix(host, "http://") {
		scheme = "http://"
		if cfg.Port > 0 && cfg.CertFile != "" && cfg.KeyFile != "" {
			scheme = "https://"
		}
	}
	// A subscription service on its own port is reached on that port, as
	// 3x-ui's is: the link says so, or the customer's app knocks on the
	// panel's instead. A host the operator typed with a port of its own
	// already says where it lives.
	if cfg.Port > 0 {
		host = withPort(host, cfg.Port)
	}
	return scheme + host + cfg.Path + token, nil
}

// withPort puts a port on a host that names none.
func withPort(host string, port int) string {
	prefix := ""
	if i := strings.Index(host, "://"); i >= 0 {
		prefix, host = host[:i+3], host[i+3:]
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return prefix + host
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return prefix + host + ":" + strconv.Itoa(port)
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
	c, err := s.byToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.bundle(ctx, c, format)
}

// BundleForClient renders one customer's configurations for an administrator.
//
// The same work as Serve, reached by client id rather than by a subscription
// token: an operator asking the panel for a customer's files should not be made
// to turn the public subscription service on first, and should not have to
// handle that customer's token to do it.
func (s *Subscriptions) BundleForClient(ctx context.Context, id uint, format string) (*Bundle, error) {
	var c model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").
		Where("id = ?", id).Limit(1).Find(&c).Error; err != nil {
		return nil, fmt.Errorf("service: read client: %w", err)
	}
	if c.ID == 0 {
		return nil, fmt.Errorf("%w: no such client", ErrNotFound)
	}
	return s.bundle(ctx, &c, format)
}

// bundle renders every device a client has, in the format asked for.
func (s *Subscriptions) bundle(ctx context.Context, c *model.Client, format string) (*Bundle, error) {
	cfg, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}

	var ifaces []model.Interface
	if err := s.db.WithContext(ctx).Find(&ifaces).Error; err != nil {
		return nil, fmt.Errorf("service: read interfaces: %w", err)
	}
	byID, err := s.interfacesWithHosts(ctx, ifaces)
	if err != nil {
		return nil, err
	}

	rendered, err := s.renderDevicesFor(ctx, c, byID, bundleFormatName(format))
	if err != nil {
		return nil, err
	}
	parts := make([]backend.ClientProfile, 0, len(rendered))
	for _, d := range rendered {
		parts = append(parts, d.Profile)
	}

	body, ctype := encodeBundle(parts, format)

	return &Bundle{
		Body:        body,
		ContentType: ctype,
		Filename:    safeFilename(c.Name) + configExt(format),
		UserInfo:    userInfo(c),
		Title:       cfg.Title,
		UpdateHours: cfg.UpdateHours,
	}, nil
}

// interfacesWithHosts indexes interfaces by id, each carrying the spare
// addresses an operator configured for it.
//
// The renderer has no database of its own, so the addresses have to arrive
// attached to the interface. Loaded in one query rather than one per interface:
// a customer with several devices on several tunnels would otherwise cost a
// round trip per device just to write their file.
func (s *Subscriptions) interfacesWithHosts(
	ctx context.Context, ifaces []model.Interface,
) (map[uint]model.Interface, error) {
	var hosts []model.Host
	if err := s.db.WithContext(ctx).Where("enabled = ?", true).
		Order("priority, id").Find(&hosts).Error; err != nil {
		return nil, fmt.Errorf("service: read hosts: %w", err)
	}
	byIface := make(map[uint][]model.Host, len(hosts))
	for _, h := range hosts {
		byIface[h.InterfaceID] = append(byIface[h.InterfaceID], h)
	}

	spent, err := s.nodesOverAllowance(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[uint]model.Interface, len(ifaces))
	for i := range ifaces {
		iface := ifaces[i]
		// A server that has used up its host's transfer allowance is left out
		// of what the customer is handed. Their app then uses the servers that
		// are left, which is the whole reason a customer is sold more than one.
		if spent[iface.NodeID] {
			continue
		}
		iface.Hosts = byIface[iface.ID]
		out[iface.ID] = iface
	}
	return out, nil
}

// nodesOverAllowance is the set of servers that have carried more this period
// than their host allows.
func (s *Subscriptions) nodesOverAllowance(ctx context.Context) (map[uint]bool, error) {
	var nodes []model.Node
	if err := s.db.WithContext(ctx).
		Where("data_limit_bytes > 0 AND used_bytes >= data_limit_bytes").
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("service: read node allowances: %w", err)
	}
	out := make(map[uint]bool, len(nodes))
	for _, n := range nodes {
		out[n.ID] = true
	}
	return out, nil
}

// RenderedDevice is one device's configuration together with the account it
// belongs to, so a caller that needs to say which device this is — a page
// listing them — does not have to look the account up again.
type RenderedDevice struct {
	Account model.Account
	Profile backend.ClientProfile
	// Host is the endpoint this entry was written for; nil for the
	// interface's own. A device on an interface with three hosts renders
	// three times, once per host, as 3x-ui fans its links out.
	Host *model.Host
}

// renderDevices produces a configuration for every device that has a working
// driver.
//
// A device whose interface is not running is skipped rather than failing the
// lot: one interface being down should not take away the configurations for a
// customer's other devices. Producing nothing at all is a different matter and
// is an error, because a client app given an empty body accepts it, shows a
// subscription with nothing in it, and leaves the customer with nothing to
// report but "it stopped working".
func (s *Subscriptions) renderDevices(
	ctx context.Context, c *model.Client, byID map[uint]model.Interface,
) ([]RenderedDevice, error) {
	return s.renderDevicesFor(ctx, c, byID, "")
}

// renderDevicesFor renders for one subscription format, so a host left out
// of that format is left out.
func (s *Subscriptions) renderDevicesFor(
	ctx context.Context, c *model.Client, byID map[uint]model.Interface, format string,
) ([]RenderedDevice, error) {
	var out []RenderedDevice

	for i := range c.Accounts {
		acc := c.Accounts[i]
		iface, ok := byID[acc.InterfaceID]
		if !ok {
			continue
		}
		drv, err := s.renderer(acc.InterfaceID, iface.Protocol)
		if err != nil {
			s.log.Warn("no driver could render a configuration",
				"client", c.Name, "account", acc.ID, "error", err)
			continue
		}
		for _, v := range variantsFor(&iface, format) {
			at := withEndpoint(iface, v)
			profile, err := drv.Render(ctx, &acc, &at)
			if err != nil {
				s.log.Warn("could not render a configuration for a subscription",
					"client", c.Name, "account", acc.ID, "error", err)
				continue
			}
			profile.Filename = variantFilename(profile.Filename, v)
			out = append(out, RenderedDevice{Account: acc, Profile: profile, Host: v.Host})
		}
	}

	if len(out) == 0 {
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
	return out, nil
}

// renderer finds something that can produce a customer's file for an interface.
//
// The open driver when there is one, since that is the interface as this
// machine actually runs it. Otherwise an unopened one built from the protocol,
// which is what makes a customer's other servers reachable at all: an interface
// on a node has no driver on the panel that holds the records — that node runs
// it — and requiring an open driver quietly dropped every one of those
// configurations from the subscription. A customer sold three servers received
// only the ones this panel happened to terminate, which is the opposite of the
// reason for having nodes.
//
// Rendering needs no device. A profile is made of the keys, addresses and ports
// already on these rows, which is why an unopened driver is enough.
func (s *Subscriptions) renderer(interfaceID uint, protocol model.Protocol) (backend.Backend, error) {
	if drv, ok := s.pool.Get(interfaceID); ok {
		return drv, nil
	}
	return backend.New(protocol)
}

// SubPage is everything a customer's own page shows them.
//
// Assembled here rather than in the HTTP layer so that the one place which
// decides what a customer may see about their own account is this package, and
// so the page cannot accidentally grow a field the API would not have exposed.
type SubPage struct {
	Title     string
	Name      string
	Status    string
	Protocol  string
	Locale    string
	Template  string
	UpdatedAt time.Time

	QuotaBytes uint64
	UsedBytes  uint64
	UpBytes    uint64
	DownBytes  uint64
	ExpiresAt  *time.Time
	// LastOnline is the freshest handshake on any of the customer's devices.
	LastOnline *time.Time

	// SubURL is the link itself, for a customer who wants to paste it into a
	// client app rather than download a file.
	SubURL  string
	Devices []SubPageDevice
}

// SubPageDevice is one row on that page.
type SubPageDevice struct {
	ID       uint
	Name     string
	Address  string
	Filename string
	Config   string
	// The host this entry was written for, when it was written for one.
	HostID          uint
	HostName        string
	HostDescription string
}

// Unlimited reports whether this plan has no volume ceiling.
func (p SubPage) Unlimited() bool { return p.QuotaBytes == 0 }

// Remaining is what is left of the allowance, floored at zero.
func (p SubPage) Remaining() uint64 {
	if p.QuotaBytes == 0 || p.UsedBytes >= p.QuotaBytes {
		return 0
	}
	return p.QuotaBytes - p.UsedBytes
}

// UsedPercent is how full the allowance is, clamped to 100.
func (p SubPage) UsedPercent() int {
	if p.QuotaBytes == 0 {
		return 0
	}
	pct := int(float64(p.UsedBytes) / float64(p.QuotaBytes) * 100)
	if pct > 100 {
		return 100
	}
	return pct
}

// bundleFormatName is the exclusion name of a bundle format.
func bundleFormatName(format string) string {
	switch strings.ToLower(format) {
	case "zip":
		return "zip"
	case "base64":
		return "base64"
	default:
		return "conf"
	}
}

// PageFor builds what a customer sees when they open their link in a browser.
func (s *Subscriptions) PageFor(ctx context.Context, token, subURL string) (*SubPage, error) {
	c, err := s.byToken(ctx, token)
	if err != nil {
		return nil, err
	}

	cfg, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}

	var ifaces []model.Interface
	if err := s.db.WithContext(ctx).Find(&ifaces).Error; err != nil {
		return nil, fmt.Errorf("service: read interfaces: %w", err)
	}
	byID, err := s.interfacesWithHosts(ctx, ifaces)
	if err != nil {
		return nil, err
	}

	rendered, err := s.renderDevicesFor(ctx, c, byID, "page")
	if err != nil {
		return nil, err
	}

	page := &SubPage{
		Title:      cfg.Title,
		Template:   cfg.Template,
		Name:       c.Name,
		Status:     string(c.Status),
		Protocol:   string(c.Protocol),
		UpdatedAt:  time.Now().UTC(),
		QuotaBytes: c.QuotaBytes,
		UsedBytes:  c.UsedBytes,
		UpBytes:    c.UpBytes,
		DownBytes:  c.DownBytes,
		ExpiresAt:  c.ExpiresAt,
		SubURL:     subURL,
	}
	for _, d := range rendered {
		if hs := d.Account.LastHandshake; hs != nil && (page.LastOnline == nil || hs.After(*page.LastOnline)) {
			t := *hs
			page.LastOnline = &t
		}
		dev := SubPageDevice{
			ID:       d.Account.ID,
			Name:     d.Account.DeviceName,
			Address:  d.Account.IP,
			Filename: d.Profile.Filename,
			Config:   string(d.Profile.Body),
		}
		if d.Host != nil {
			dev.HostID = d.Host.ID
			dev.HostName = d.Host.Name
			dev.HostDescription = d.Host.Description
		}
		page.Devices = append(page.Devices, dev)
	}
	return page, nil
}

// DeviceConfig returns one device's configuration, for a customer downloading a
// single file from their own page.
//
// The device is looked up through the token's client rather than by id alone:
// an id is guessable and a token is not, so the ownership check is what stops
// one customer reading another's keys.
func (s *Subscriptions) DeviceConfig(
	ctx context.Context, token string, deviceID, hostID uint,
) (*backend.ClientProfile, error) {
	c, err := s.byToken(ctx, token)
	if err != nil {
		return nil, err
	}

	var ifaces []model.Interface
	if err := s.db.WithContext(ctx).Find(&ifaces).Error; err != nil {
		return nil, fmt.Errorf("service: read interfaces: %w", err)
	}
	byID, err := s.interfacesWithHosts(ctx, ifaces)
	if err != nil {
		return nil, err
	}

	rendered, err := s.renderDevices(ctx, c, byID)
	if err != nil {
		return nil, err
	}
	for i := range rendered {
		r := &rendered[i]
		if r.Account.ID != deviceID {
			continue
		}
		if hostID == 0 && r.Host == nil || r.Host != nil && r.Host.ID == hostID {
			return &r.Profile, nil
		}
	}
	// A host that was asked for but is not on this device: the first
	// rendering is still the right file for the device.
	for i := range rendered {
		if rendered[i].Account.ID == deviceID {
			return &rendered[i].Profile, nil
		}
	}
	return nil, fmt.Errorf("%w: no such device on this subscription", ErrNotFound)
}

// byToken finds the client a subscription token belongs to.
func (s *Subscriptions) byToken(ctx context.Context, token string) (*model.Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("%w: no subscription token", ErrNotFound)
	}
	var c model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").
		Where("sub_token = ?", token).Limit(1).Find(&c).Error; err != nil {
		return nil, fmt.Errorf("service: read subscription: %w", err)
	}
	if c.ID == 0 {
		// Deliberately the same answer as a token that never existed.
		return nil, fmt.Errorf("%w: no such subscription", ErrNotFound)
	}
	return &c, nil
}

// encodeBundle puts the configurations into the shape asked for.
func encodeBundle(parts []backend.ClientProfile, format string) (body []byte, contentType string) {
	switch strings.ToLower(format) {
	case "zip":
		// One file per device. Concatenating them, which is what every other
		// format here does, produces something no WireGuard client can import:
		// a .conf holds exactly one [Interface], so a customer with a phone and
		// a laptop was being handed a file where the second device silently did
		// not exist. This is the only format that can carry more than one.
		//
		// Not the default, and not what a subscription URL should return:
		// OpenVPN Connect and most WireGuard subscription clients fetch that URL
		// expecting text and reject an archive outright.
		return zipBundle(parts), "application/zip"
	case "base64":
		// What most subscription clients expect. Standard encoding rather than
		// URL-safe: the body is not a URL, and clients decode it as standard.
		enc := base64.StdEncoding.EncodeToString(joinProfiles(parts))
		return []byte(enc), "text/plain; charset=utf-8"
	default:
		return joinProfiles(parts), "text/plain; charset=utf-8"
	}
}

func joinProfiles(parts []backend.ClientProfile) []byte {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(string(p.Body)))
	}
	return []byte(strings.Join(out, "\n\n"))
}

// zipBundle writes one entry per configuration.
//
// Names are made unique as they go: two devices on different interfaces can
// arrive with the same filename, and a zip with two identical names is one an
// unpacker will either refuse or silently reduce to one file -- losing exactly
// the configuration this format exists to deliver.
func zipBundle(parts []backend.ClientProfile) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := make(map[string]int, len(parts))

	for i, p := range parts {
		// Only the last element, and only after slashes are normalised: a
		// filename is chosen by a driver, but an archive entry with a path in
		// it is how an unpacker gets talked into writing outside its directory.
		name := path.Base(filepath.ToSlash(strings.TrimSpace(p.Filename)))
		if name == "" || name == "." || name == "/" {
			name = fmt.Sprintf("device-%d.conf", i+1)
		}

		// Suffixed until it is free, rather than once: -2 can itself already be
		// taken by a device that happened to be named that way.
		base, ext := strings.TrimSuffix(name, path.Ext(name)), path.Ext(name)
		for n := 2; used[name] > 0; n++ {
			name = fmt.Sprintf("%s-%d%s", base, n, ext)
		}
		used[name]++

		w, err := zw.Create(name)
		if err != nil {
			continue
		}
		// A trailing newline: some editors and some clients will not read the
		// last line of a file that does not end in one.
		_, _ = w.Write(append(bytes.TrimSpace(p.Body), '\n'))
	}

	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

func configExt(format string) string {
	switch strings.ToLower(format) {
	case "base64":
		return ".txt"
	case "zip":
		return ".zip"
	default:
		return ".conf"
	}
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
