package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
)

// newStealthSecret is a device's transport secret, as an account keeps it.
func newStealthSecret() (string, error) {
	s, err := stealth.NewSecret()
	if err != nil {
		return "", err
	}
	return s.String(), nil
}

// EnsureStealthSecrets gives every WireGuard device this panel holds the
// records of a transport secret, when it has none: the devices made before
// the stealth transports were. A device on a tunnel another panel manages
// is that panel's to give one, and its secret arrives with it.
//
// Every device gets one, whether its tunnel has a stealth transport or not,
// so turning one on later needs nothing more than the customer's file again.
func EnsureStealthSecrets(ctx context.Context, db *gorm.DB) error {
	var ids []uint
	if err := db.WithContext(ctx).Model(&model.Account{}).
		Joins("JOIN interfaces ON interfaces.id = accounts.interface_id").
		Where("interfaces.protocol = ? AND interfaces.managed = ? AND (accounts.stealth_secret = '' OR accounts.stealth_secret IS NULL)",
			model.ProtocolWireGuard, false).
		Pluck("accounts.id", &ids).Error; err != nil {
		return fmt.Errorf("service: find devices without a transport secret: %w", err)
	}
	for _, id := range ids {
		secret, err := newStealthSecret()
		if err != nil {
			return err
		}
		// Only where there is still none, so a pass that races another
		// never replaces a secret a customer was already handed. Its
		// updated_at is left: the files a customer has did not change, and
		// their date is read from it.
		if err := db.WithContext(ctx).Model(&model.Account{}).
			Where("id = ? AND (stealth_secret = '' OR stealth_secret IS NULL)", id).
			UpdateColumn("stealth_secret", secret).Error; err != nil {
			return fmt.Errorf("service: store a transport secret: %w", err)
		}
	}
	return nil
}

// checkWireGuardTransport refuses a WireGuard transport there is none of.
func checkWireGuardTransport(name string) error {
	switch name {
	case "", model.TransportUDP, model.TransportWuiUDP, model.TransportWuiWS:
		return nil
	}
	return invalidField("transport", "a WireGuard tunnel's transport is udp, wui-udp or wui-ws, not %q", name)
}

// WSSettings is wui-ws's part of a WireGuard tunnel's transport, as the
// form sends it. model.StealthParams says what each is.
type WSSettings struct {
	Host         string `json:"host"`
	FrontPort    int    `json:"frontPort"`
	Path         string `json:"path"`
	Origin       string `json:"origin"`
	Proxies      string `json:"proxies"`
	ClientHeader string `json:"clientHeader"`
}

// apply puts the settings on p. An empty path keeps p's.
func (w *WSSettings) apply(p *model.StealthParams) {
	p.Host = strings.TrimSpace(w.Host)
	p.FrontPort = w.FrontPort
	if path := strings.TrimSpace(w.Path); path != "" {
		p.Path = path
	}
	p.Origin = strings.TrimSpace(w.Origin)
	p.Proxies = strings.TrimSpace(w.Proxies)
	p.ClientHeader = strings.TrimSpace(w.ClientHeader)
}

// wsKeyDays is how long a wui-ws transport key is used before the next is
// drawn. The one before is still taken until then, so an app that fetches
// its profile at least that often never meets a key it does not know.
const wsKeyDays = 30

// wsPathLen is the length of a path the panel draws, past its slash.
const wsPathLen = 20

// newWSPath draws a path: nothing a site would have, and nothing anyone
// would guess.
func newWSPath() (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, wsPathLen)
	for i := range b {
		var r [1]byte
		for {
			if _, err := rand.Read(r[:]); err != nil {
				return "", fmt.Errorf("service: read entropy: %w", err)
			}
			if int(r[0]) < 252 { // 7 x 36: no character more likely than another
				break
			}
		}
		b[i] = chars[int(r[0])%len(chars)]
	}
	return "/" + string(b), nil
}

// newWSKey draws a transport key, as StealthParams keeps it.
func newWSKey() (string, error) {
	k, err := stealth.NewTransportKey()
	if err != nil {
		return "", err
	}
	return k.String(), nil
}

// checkWS checks wui-ws's settings and fills what the panel draws: a path
// when none was given, and the transport's key when it has none. Whatever
// wui-ws uses is cleared from a transport that is not it.
func checkWS(p model.StealthParams, now time.Time) (model.StealthParams, error) {
	if p.Transport != model.TransportWuiWS {
		return model.StealthParams{Transport: p.Transport, Port: p.Port}, nil
	}
	host := strings.ToLower(strings.TrimSuffix(p.Host, "."))
	switch {
	case host == "":
		return p, invalidField("transportWs.host", "wui-ws needs the name devices reach the front at: the web server's or the CDN's")
	case !validHostName(host):
		return p, invalidField("transportWs.host", "%q is not a host name or an address", p.Host)
	}
	p.Host = host
	if p.FrontPort < 0 || p.FrontPort > 65535 {
		return p, invalidField("transportWs.frontPort", "port %d is out of range", p.FrontPort)
	}
	if p.Path == "" {
		path, err := newWSPath()
		if err != nil {
			return p, err
		}
		p.Path = path
	}
	if err := checkWSPath(p.Path); err != nil {
		return p, err
	}
	if p.Origin != "" {
		u, err := url.Parse(p.Origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			return p, invalidField("transportWs.origin", "the decoy is an address like http://127.0.0.1:8080, with no path: every request goes to its own path there")
		}
		p.Origin = u.Scheme + "://" + u.Host
	}
	if _, err := ParseWSProxies(p.Proxies); err != nil {
		return p, invalidField("transportWs.proxies", "%v", err)
	}
	if p.ClientHeader != "" && !validHeaderName(p.ClientHeader) {
		return p, invalidField("transportWs.clientHeader", "%q is not a header name", p.ClientHeader)
	}
	if p.Key == "" {
		key, err := newWSKey()
		if err != nil {
			return p, err
		}
		p.Key, p.PreviousKey, p.KeyAt = key, "", now.Unix()
	}
	return p, nil
}

// checkWSPath takes a path of at least the gateway's least, of the
// characters a path carries unescaped, from a slash.
func checkWSPath(path string) error {
	if len(path) < 12 || len(path) > 256 || path[0] != '/' {
		return invalidField("transportWs.path", "the path starts with / and is 12 to 256 characters; left empty, one is drawn")
	}
	for _, c := range path {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/-._~", c)) {
			return invalidField("transportWs.path", "the path may hold letters, digits and / - . _ ~ alone, not %q", c)
		}
	}
	return nil
}

// ParseWSProxies reads the fronts a wui-ws gateway trusts: addresses and
// ranges, by commas or white space.
func ParseWSProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	sep := func(r rune) bool { return r == ',' || unicode.IsSpace(r) }
	for _, f := range strings.FieldsFunc(v, sep) {
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or a range", f)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func validHostName(h string) bool {
	if _, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil {
		return true
	}
	if len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func validHeaderName(h string) bool {
	for _, c := range h {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return h != ""
}

// EnsureStealthKeys gives every wui-ws transport this panel manages a key
// when it has none, and its next key when the one it has is wsKeyDays old,
// keeping the one before. A tunnel another panel manages is that panel's to
// keep, and its keys arrive with it.
func EnsureStealthKeys(ctx context.Context, db *gorm.DB, now time.Time) error {
	var ifaces []model.Interface
	if err := db.WithContext(ctx).Where("protocol = ? AND managed = ? AND stealth IS NOT NULL AND stealth <> ''", model.ProtocolWireGuard, false).
		Find(&ifaces).Error; err != nil {
		return fmt.Errorf("service: find stealth transports: %w", err)
	}
	for _, iface := range ifaces {
		p := iface.Stealth.V
		if p.Transport != model.TransportWuiWS {
			continue
		}
		if p.Key != "" && now.Sub(time.Unix(p.KeyAt, 0)) < wsKeyDays*24*time.Hour {
			continue
		}
		key, err := newWSKey()
		if err != nil {
			return err
		}
		if p.Key != "" {
			p.PreviousKey = p.Key
		}
		p.Key, p.KeyAt = key, now.Unix()
		// Only where the transport is still as it was read, so a pass that
		// races an edit or another pass replaces nothing it did not see.
		if err := db.WithContext(ctx).Model(&model.Interface{}).Where("id = ? AND stealth = ?", iface.ID, iface.Stealth).
			UpdateColumn("stealth", model.JSON(p)).Error; err != nil {
			return fmt.Errorf("service: store a transport key: %w", err)
		}
	}
	return nil
}

// checkStealth checks a WireGuard tunnel's transport as it would be, for a
// tunnel of mode on listenPort. selfID is the tunnel when it exists, and
// held the port its gateway already holds, which is no clash with itself.
// local says the tunnel is on this machine, which can be asked whether the
// port is free. Plain UDP comes back as the zero value.
func (s *Interfaces) checkStealth(ctx context.Context, nodeID, selfID uint, mode model.InterfaceMode,
	listenPort int, p model.StealthParams, held int, local bool,
) (model.StealthParams, error) {
	if err := checkWireGuardTransport(p.Transport); err != nil {
		return p, err
	}
	if p.Transport == "" || p.Transport == model.TransportUDP {
		if p.Port != 0 {
			return p, invalidField("transportPort", "plain UDP is on the tunnel's own port and takes no other")
		}
		return model.StealthParams{}, nil
	}
	if mode == model.ModeAmnezia {
		return p, invalidField("transport", "only a standard WireGuard tunnel takes %s; AmneziaWG's packets are its own", p.Transport)
	}
	switch {
	case p.Port == 0:
		return p, invalidField("transportPort", "a port of its own is needed for %s, beside the tunnel's", p.Transport)
	case p.Port < 1 || p.Port > 65535:
		return p, invalidField("transportPort", "port %d is out of range", p.Port)
	case p.Port == listenPort:
		return p, invalidField("transportPort", "port %d is the tunnel's own; %s needs one beside it", p.Port, p.Transport)
	}
	network := model.TransportNetwork(p.Transport)
	if err := s.portClash(ctx, nodeID, selfID, p.Port, []string{network}, "transportPort"); err != nil {
		return p, err
	}
	if local && p.Port != held {
		if err := checkPortFree(p.Port, network); err != nil {
			return p, &FieldError{Field: "transportPort", Err: err}
		}
	}
	return checkWS(p, time.Now())
}

// stealthClash is the tunnel on a server, other than selfID, whose stealth
// transport takes port on one of transports, or nil.
func (s *Interfaces) stealthClash(ctx context.Context, nodeID, selfID uint, port int, transports []string) (*model.Interface, error) {
	var withStealth []model.Interface
	q := s.db.WithContext(ctx).Where("node_id = ? AND protocol = ? AND stealth IS NOT NULL AND stealth <> ''", nodeID, model.ProtocolWireGuard)
	if selfID != 0 {
		q = q.Where("id <> ?", selfID)
	}
	if err := q.Find(&withStealth).Error; err != nil {
		return nil, fmt.Errorf("service: check stealth ports: %w", err)
	}
	for i := range withStealth {
		name, p := withStealth[i].StealthTransport()
		if p == port && slices.Contains(transports, model.TransportNetwork(name)) {
			return &withStealth[i], nil
		}
	}
	return nil, nil
}

// updateStealth takes a WireGuard tunnel's transport and its port. A device
// needs its file again to use a new one, but nobody's tunnel is touched: the
// gateway stands beside WireGuard, which goes on as it was.
func (s *Interfaces) updateStealth(ctx context.Context, iface *model.Interface, in UpdateInterfaceInput, fields map[string]any) error {
	if iface.Protocol != model.ProtocolWireGuard {
		if in.TransportPort != nil && *in.TransportPort != 0 {
			return invalidField("transportPort", "only a WireGuard tunnel's stealth transport takes a port of its own")
		}
		return nil
	}
	if in.Transport == nil && in.TransportPort == nil && in.TransportWS == nil {
		return nil
	}
	want := iface.Stealth.V
	if in.Transport != nil {
		want.Transport = strings.ToLower(strings.TrimSpace(*in.Transport))
		if want.Transport == model.TransportUDP || want.Transport == "" {
			want = model.StealthParams{}
		}
	}
	if in.TransportPort != nil && want.Transport != "" {
		want.Port = *in.TransportPort
	}
	if in.TransportWS != nil && want.Transport == model.TransportWuiWS {
		in.TransportWS.apply(&want)
	}
	if want == iface.Stealth.V {
		return nil
	}
	port := iface.ListenPort
	if v, ok := fields["listen_port"].(int); ok {
		port = v
	}
	mode := iface.Mode
	if v, ok := fields["mode"].(model.InterfaceMode); ok {
		mode = v
	}
	local, err := s.isLocal(ctx, iface.NodeID)
	if err != nil {
		return err
	}
	p, err := s.checkStealth(ctx, iface.NodeID, iface.ID, mode, port, want, iface.Stealth.V.Port, local)
	if err != nil {
		return err
	}
	fields["stealth"] = model.JSON(p)
	return nil
}
