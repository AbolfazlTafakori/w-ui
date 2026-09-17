package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/dnsproxy"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
	"github.com/abolfazl/w-ui/internal/wgconf"
)

// The engine page: what the classic panel keeps under Xray -- the core's own knobs --
// kept here for the kernel tunnels this panel runs instead. One JSON blob
// rather than a key per field, because the page saves it whole, and the
// parts that need a restart (the collection interval, the log settings)
// are read once at start like the listen address is.

const keyEngine = "engine.settings"

// EngineSettings is everything the engine page can change.
type EngineSettings struct {
	// DomainStrategy is how names in routing lists resolve: AsIs (both
	// families), UseIPv4 or UseIPv6.
	DomainStrategy string `json:"domainStrategy"`
	// OutboundTestURL is what an outbound check fetches.
	OutboundTestURL string `json:"outboundTestUrl"`

	// CollectInterval is how often traffic is read, in seconds; 0 keeps the
	// environment's. Applied at start. OutboundCounters keeps a byte
	// counter per outbound in the kernel.
	CollectInterval  int  `json:"collectInterval"`
	OutboundCounters bool `json:"outboundCounters"`
	// OnlineWindow is how many seconds since a handshake still counts as
	// online.
	OnlineWindow int `json:"onlineWindow"`

	// The log: level and format apply at start; the access log and address
	// masking at once.
	LogLevel    string `json:"logLevel"`
	LogFormat   string `json:"logFormat"`
	AccessLog   bool   `json:"accessLog"`
	MaskAddress bool   `json:"maskAddress"`

	// The balancer observatory: how often members are measured, and
	// whether all at once.
	ProbeInterval    int  `json:"probeInterval"`
	ProbeConcurrency bool `json:"probeConcurrency"`

	DNS dnsproxy.Config `json:"dns"`
}

// Engine reads, writes and applies the engine settings.
type Engine struct {
	db      *gorm.DB
	log     *slog.Logger
	routing *Routing
	dns     *dnsproxy.Proxy

	mu    sync.RWMutex
	cache *EngineSettings
}

// Live hooks the rest of the panel reads without a database call.
var (
	// ProbeURL is what outbound checks fetch. Only the trace endpoint
	// yields an egress address and country; anything else measures alone.
	ProbeURL atomic.Value // string
	// OnlineWithin is how recent a handshake has to be to count as online.
	OnlineWithin atomic.Int64 // seconds
	// OutboundCounters switches the per-outbound kernel counters.
	OutboundCounters atomic.Bool
	// AccessLog and MaskAddress steer the request log.
	AccessLog   atomic.Bool
	MaskAddress atomic.Bool
	// ProbeInterval and ProbeConcurrency steer the balancer checks.
	ProbeInterval    atomic.Int64 // seconds
	ProbeConcurrency atomic.Bool
)

func init() {
	ProbeURL.Store(traceURL)
	OnlineWithin.Store(180)
	OutboundCounters.Store(true)
	AccessLog.Store(true)
	ProbeInterval.Store(60)
	ProbeConcurrency.Store(true)
}

func NewEngine(db *gorm.DB, routing *Routing, dns *dnsproxy.Proxy, log *slog.Logger) *Engine {
	return &Engine{db: db, log: log, routing: routing, dns: dns}
}

// Defaults are what a fresh install behaves as if it had.
func (e *Engine) Defaults() EngineSettings {
	return EngineSettings{
		DomainStrategy:   "AsIs",
		OutboundTestURL:  traceURL,
		CollectInterval:  0,
		OutboundCounters: true,
		OnlineWindow:     180,
		LogLevel:         "",
		LogFormat:        "",
		AccessLog:        true,
		MaskAddress:      false,
		ProbeInterval:    60,
		ProbeConcurrency: true,
		// The resolver is on from the start, answering at the tunnel's own
		// address, for two reasons a customer notices. It answers only for
		// IPv4: the tunnels carry IPv4, and a name that also came back with an
		// IPv6 address made a phone try that first, wait for it to fail, and
		// only then load the page -- or not load it at all in an app that does
		// not fall back. And it caches, so the second lookup costs nothing.
		DNS: dnsproxy.Config{
			Enabled:       true,
			QueryStrategy: "UseIPv4",
			Hosts:         []dnsproxy.Host{},
			Servers: []dnsproxy.Server{
				{Address: "1.1.1.1"},
				{Address: "8.8.8.8"},
			},
		},
	}
}

// Get returns the stored settings over the defaults.
func (e *Engine) Get(ctx context.Context) (EngineSettings, error) {
	e.mu.RLock()
	if e.cache != nil {
		out := *e.cache
		e.mu.RUnlock()
		return out, nil
	}
	e.mu.RUnlock()

	out := e.Defaults()
	raw, ok, err := database.GetSetting(e.db.WithContext(ctx), keyEngine)
	if err != nil {
		return out, fmt.Errorf("service: read engine settings: %w", err)
	}
	if ok && strings.TrimSpace(raw) != "" {
		// Decoded over the defaults, so a field this version added reads as
		// its default rather than as zero.
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			e.log.Warn("stored engine settings are unreadable; using defaults", "error", err)
			out = e.Defaults()
		}
	}
	if out.DNS.Hosts == nil {
		out.DNS.Hosts = []dnsproxy.Host{}
	}
	if out.DNS.Servers == nil {
		out.DNS.Servers = []dnsproxy.Server{}
	}
	e.mu.Lock()
	e.cache = &out
	e.mu.Unlock()
	return out, nil
}

// Save validates, stores and applies.
func (e *Engine) Save(ctx context.Context, in EngineSettings) (EngineSettings, error) {
	if err := e.validate(&in); err != nil {
		return EngineSettings{}, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return EngineSettings{}, err
	}
	if err := database.PutSetting(e.db.WithContext(ctx), keyEngine, string(raw)); err != nil {
		return EngineSettings{}, fmt.Errorf("service: save engine settings: %w", err)
	}
	e.mu.Lock()
	e.cache = &in
	e.mu.Unlock()
	e.Apply(ctx)
	return in, nil
}

// Reset puts the defaults back.
func (e *Engine) Reset(ctx context.Context) (EngineSettings, error) {
	return e.Save(ctx, e.Defaults())
}

func (e *Engine) validate(in *EngineSettings) error {
	switch in.DomainStrategy {
	case "", "AsIs":
		in.DomainStrategy = "AsIs"
	case "UseIPv4", "UseIPv6":
	default:
		return fmt.Errorf("%w: unknown domain strategy %q", ErrInvalid, in.DomainStrategy)
	}
	in.OutboundTestURL = strings.TrimSpace(in.OutboundTestURL)
	if in.OutboundTestURL == "" {
		in.OutboundTestURL = traceURL
	}
	if !strings.HasPrefix(in.OutboundTestURL, "http://") && !strings.HasPrefix(in.OutboundTestURL, "https://") {
		return fmt.Errorf("%w: the test URL must begin with http:// or https://", ErrInvalid)
	}
	if in.CollectInterval < 0 || in.CollectInterval > 3600 {
		return fmt.Errorf("%w: the collection interval is 0 to 3600 seconds", ErrInvalid)
	}
	if in.OnlineWindow < 10 || in.OnlineWindow > 86400 {
		return fmt.Errorf("%w: the online window is 10 to 86400 seconds", ErrInvalid)
	}
	switch in.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("%w: unknown log level %q", ErrInvalid, in.LogLevel)
	}
	switch in.LogFormat {
	case "", "text", "json":
	default:
		return fmt.Errorf("%w: unknown log format %q", ErrInvalid, in.LogFormat)
	}
	if in.ProbeInterval < 10 || in.ProbeInterval > 86400 {
		return fmt.Errorf("%w: the probe interval is 10 to 86400 seconds", ErrInvalid)
	}
	switch in.DNS.QueryStrategy {
	case "", "UseIP":
		in.DNS.QueryStrategy = "UseIP"
	case "UseIPv4", "UseIPv6":
	default:
		return fmt.Errorf("%w: unknown query strategy %q", ErrInvalid, in.DNS.QueryStrategy)
	}
	if in.DNS.ServeExpiredTTL < 0 {
		return fmt.Errorf("%w: the stale TTL cannot be negative", ErrInvalid)
	}
	hosts := in.DNS.Hosts[:0]
	for _, h := range in.DNS.Hosts {
		h.Domain = strings.ToLower(strings.TrimSpace(h.Domain))
		if h.Domain == "" {
			continue
		}
		var vals []string
		for _, v := range h.Values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, err := netip.ParseAddr(v); err != nil {
				return fmt.Errorf("%w: host %s: %q is not an IP address", ErrInvalid, h.Domain, v)
			}
			vals = append(vals, v)
		}
		h.Values = vals
		hosts = append(hosts, h)
	}
	in.DNS.Hosts = hosts
	servers := in.DNS.Servers[:0]
	for _, s := range in.DNS.Servers {
		s.Address = strings.TrimSpace(s.Address)
		if s.Address == "" {
			continue
		}
		if s.Port < 0 || s.Port > 65535 {
			return fmt.Errorf("%w: DNS server %s: port %d is out of range", ErrInvalid, s.Address, s.Port)
		}
		var doms []string
		for _, d := range s.Domains {
			if d = strings.TrimSpace(d); d != "" {
				doms = append(doms, d)
			}
		}
		s.Domains = doms
		servers = append(servers, s)
	}
	in.DNS.Servers = servers
	if in.DNS.Enabled && len(in.DNS.Servers) == 0 {
		return fmt.Errorf("%w: DNS needs at least one server to forward to", ErrInvalid)
	}
	return nil
}

// Apply puts the live settings where the rest of the panel reads them.
func (e *Engine) Apply(ctx context.Context) {
	cfg, err := e.Get(ctx)
	if err != nil {
		return
	}
	ProbeURL.Store(cfg.OutboundTestURL)
	OnlineWithin.Store(int64(cfg.OnlineWindow))
	OutboundCounters.Store(cfg.OutboundCounters)
	AccessLog.Store(cfg.AccessLog)
	MaskAddress.Store(cfg.MaskAddress)
	ProbeInterval.Store(int64(cfg.ProbeInterval))
	ProbeConcurrency.Store(cfg.ProbeConcurrency)
	if cfg.LogLevel != "" {
		logger.SetLevel(cfg.LogLevel)
	}
	if e.routing != nil {
		e.routing.SetDomainStrategy(cfg.DomainStrategy)
	}
	// Customers are handed the tunnel's own address as their resolver
	// while the proxy is on; otherwise whatever the interface says.
	dnsOn := cfg.DNS.Enabled && len(cfg.DNS.Servers) > 0
	dnsFor := func(iface *model.Interface) string {
		if dnsOn {
			if gw, ok := gatewayOf(iface.Subnet); ok {
				return gw.String()
			}
		}
		return iface.DNS
	}
	wgconf.DNSFor = dnsFor
	ovpnconf.DNSFor = dnsFor
	e.syncDNS(ctx, cfg)
}

// EngineOverrides is what applies at the next start.
type EngineOverrides struct {
	CollectInterval time.Duration
	LogLevel        string
	LogFormat       string
}

// Overrides reads the start-time values.
func (e *Engine) Overrides(ctx context.Context) EngineOverrides {
	cfg, err := e.Get(ctx)
	if err != nil {
		return EngineOverrides{}
	}
	return EngineOverrides{
		CollectInterval: time.Duration(cfg.CollectInterval) * time.Second,
		LogLevel:        cfg.LogLevel,
		LogFormat:       cfg.LogFormat,
	}
}

// RunDNS keeps the resolver's listeners in step with the enabled tunnels.
func (e *Engine) RunDNS(ctx context.Context) {
	if e.dns == nil {
		return
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				e.dns.Close()
				return
			case <-t.C:
				if cfg, err := e.Get(ctx); err == nil {
					e.syncDNS(ctx, cfg)
				}
			}
		}
	}()
}

func (e *Engine) syncDNS(ctx context.Context, cfg EngineSettings) {
	if e.dns == nil {
		return
	}
	var addrs []netip.Addr
	if cfg.DNS.Enabled {
		var ifaces []model.Interface
		if err := e.db.WithContext(ctx).Where("enabled = ?", true).Find(&ifaces).Error; err == nil {
			for _, i := range ifaces {
				if gw, ok := gatewayOf(i.Subnet); ok {
					addrs = append(addrs, gw)
				}
			}
		}
	}
	// Upstream queries leave the way customer traffic does.
	if e.routing != nil {
		e.dns.SetMark(e.routing.DefaultMark())
	}
	e.dns.Reconfigure(cfg.DNS, addrs)
}

// DNSListening reports where the resolver answers.
func (e *Engine) DNSListening() []string {
	if e.dns == nil {
		return nil
	}
	return e.dns.Listening()
}

// gatewayOf is the first host of a tunnel subnet, which is the address the
// server holds inside it.
func gatewayOf(subnet string) (netip.Addr, bool) {
	pfx, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return netip.Addr{}, false
	}
	return pfx.Masked().Addr().Next(), true
}
