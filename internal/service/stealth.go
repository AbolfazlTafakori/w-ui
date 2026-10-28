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
	case "", model.TransportUDP, model.TransportWuiUDP, model.TransportWuiTLS, model.TransportWuiWS, model.TransportWuiPoll, model.TransportWuiPlain:
		return nil
	}
	return invalidField("transport", "a WireGuard tunnel's transport is udp, wui-udp, wui-tls, wui-ws, wui-poll or wui-plain, not %q", name)
}

// WSSettings is the rest of a WireGuard tunnel's transport, wui-tls's,
// wui-ws's, wui-poll's or wui-plain's, as the form sends it. model.StealthParams says
// what each is.
type WSSettings struct {
	Host         string `json:"host"`
	FrontPort    int    `json:"frontPort"`
	Path         string `json:"path"`
	Origin       string `json:"origin"`
	Proxies      string `json:"proxies"`
	ClientHeader string `json:"clientHeader"`
	// Cookie is wui-plain's alone.
	Cookie string `json:"cookie"`
	// UsePanelCert and ProxyProtocol are wui-tls's alone.
	UsePanelCert  bool `json:"usePanelCert"`
	ProxyProtocol bool `json:"proxyProtocol"`
}

// apply puts the settings on p. An empty path or cookie keeps p's.
func (w *WSSettings) apply(p *model.StealthParams) {
	p.Host = strings.TrimSpace(w.Host)
	p.FrontPort = w.FrontPort
	if path := strings.TrimSpace(w.Path); path != "" {
		p.Path = path
	}
	p.Origin = strings.TrimSpace(w.Origin)
	p.Proxies = strings.TrimSpace(w.Proxies)
	p.ClientHeader = strings.TrimSpace(w.ClientHeader)
	if c := strings.TrimSpace(w.Cookie); c != "" {
		p.Cookie = c
	}
	p.UsePanelCert, p.ProxyProtocol = w.UsePanelCert, w.ProxyProtocol
}

// wsKeyDays is how long a wui-ws, wui-poll or wui-tls transport key is used before
// the next is drawn. The one before is still taken until then, so an app that fetches
// its profile at least that often never meets a key it does not know.
const wsKeyDays = 30

// plainKeyDays is wui-plain's: every proof it opens crossed the network in
// the clear, so a key that leaks opens the records of fewer of them. The
// key only hides which device a proof is from; the session's keys need the
// device's secret as well.
const plainKeyDays = 7

// keyDays is how long a transport's key is used.
func keyDays(transport string) int {
	if transport == model.TransportWuiPlain {
		return plainKeyDays
	}
	return wsKeyDays
}

// newPlainCookie draws the name wui-plain's token travels under: 8 to 16
// characters, as a site's session cookie might be named, and different on
// every server, so no name is wui-plain's.
func newPlainCookie() (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789_"
	pick := func(n int) (int, error) {
		for {
			var r [1]byte
			if _, err := rand.Read(r[:]); err != nil {
				return 0, fmt.Errorf("service: read entropy: %w", err)
			}
			if lim := 256 - 256%n; int(r[0]) < lim { // no value more likely than another
				return int(r[0]) % n, nil
			}
		}
	}
	extra, err := pick(9)
	if err != nil {
		return "", err
	}
	b := make([]byte, 8+extra)
	for i := range b {
		n := len(chars)
		if i == 0 {
			n = 26 // a letter first
		}
		c, err := pick(n)
		if err != nil {
			return "", err
		}
		b[i] = chars[c]
	}
	return string(b), nil
}

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

// checkWS checks wui-tls's, wui-ws's, wui-poll's or wui-plain's settings and fills
// what the panel draws: a path when none was given, wui-plain's cookie
// name, and the transport's key when it has none. Whatever they use is
// cleared from a transport that is none of them, what a front needs from
// wui-tls, wui-tls's own from the others, and the cookie from all but
// wui-plain. wui-tls's certificate is tlsCertFor's.
func checkWS(p model.StealthParams, now time.Time) (model.StealthParams, error) {
	if !p.Proven() {
		return model.StealthParams{Transport: p.Transport, Port: p.Port}, nil
	}
	plain, direct := p.Transport == model.TransportWuiPlain, p.Transport == model.TransportWuiTLS
	host := strings.ToLower(strings.TrimSuffix(p.Host, "."))
	switch {
	case host == "" && direct:
		return p, invalidField("transportWs.host", "the name or address devices reach this server at is needed for wui-tls")
	case host == "":
		return p, invalidField("transportWs.host", "the name devices reach the front at is needed for %s: the web server's or the CDN's", p.Transport)
	case !validHostName(host):
		return p, invalidField("transportWs.host", "%q is not a host name or an address", p.Host)
	}
	p.Host = host
	if direct {
		// No front: devices reach the gateway on its own port.
		p.FrontPort, p.Proxies, p.ClientHeader = 0, "", ""
	} else {
		p.Cert, p.CertKey, p.UsePanelCert, p.PanelCert, p.PanelCertKey, p.CertSource, p.ProxyProtocol = "", "", false, "", "", "", false
	}
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
		if direct && u.Scheme != "http" {
			return p, invalidField("transportWs.origin", "wui-tls hands a stranger's bytes to the decoy as they came, after its own TLS, so the decoy is an http:// address, like http://127.0.0.1:8080")
		}
		p.Origin = u.Scheme + "://" + u.Host
	} else if direct {
		return p, invalidField("transportWs.origin", "the decoy is needed for wui-tls: whoever is not a device is handed to your own site, so the port answers as a web server does")
	} else if plain {
		return p, invalidField("transportWs.origin", "the decoy is needed for wui-plain: a stranger's answer crosses the network in the clear, so it must be your site's own")
	}
	if _, err := ParseWSProxies(p.Proxies); err != nil {
		return p, invalidField("transportWs.proxies", "%v", err)
	}
	if p.ClientHeader != "" && !validHeaderName(p.ClientHeader) {
		return p, invalidField("transportWs.clientHeader", "%q is not a header name", p.ClientHeader)
	}
	switch {
	case !plain:
		p.Cookie = ""
	case p.Cookie == "":
		c, err := newPlainCookie()
		if err != nil {
			return p, err
		}
		p.Cookie = c
	case !stealth.CookieName(p.Cookie):
		return p, invalidField("transportWs.cookie", "the cookie's name is 1 to %d letters, digits, - and _; left empty, one is drawn", stealth.CookieMax)
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

// EnsureStealthKeys gives every wui-tls, wui-ws, wui-poll and wui-plain transport
// this panel manages a key when it has none, and its next key when the one it has is
// keyDays old, keeping the one before. A tunnel another panel manages is
// that panel's to keep, and its keys arrive with it.
func EnsureStealthKeys(ctx context.Context, db *gorm.DB, now time.Time) error {
	var ifaces []model.Interface
	if err := db.WithContext(ctx).Where("protocol = ? AND managed = ? AND stealth IS NOT NULL AND stealth <> ''", model.ProtocolWireGuard, false).
		Find(&ifaces).Error; err != nil {
		return fmt.Errorf("service: find stealth transports: %w", err)
	}
	for _, iface := range ifaces {
		p := iface.Stealth.V
		if !p.Proven() {
			continue
		}
		if p.Key != "" && now.Sub(time.Unix(p.KeyAt, 0)) < time.Duration(keyDays(p.Transport))*24*time.Hour {
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
	now := time.Now()
	p, err := checkWS(p, now)
	if err != nil {
		return p, err
	}
	if p.Transport == model.TransportWuiTLS {
		if !local && p.UsePanelCert {
			return p, invalidField("transportWs.usePanelCert", "only a tunnel on the panel's own server takes the panel's certificate: its key would have to go to the node with it")
		}
		if _, err := tlsCertFor(&p, s.panelCert, now); err != nil {
			return p, err
		}
	}
	return p, s.plainApart(ctx, selfID, p)
}

// plainApart refuses a wui-plain transport whose front name or path is a
// covered transport's -- wui-tls's, wui-ws's or wui-poll's -- and a covered one whose
// are a wui-plain's, on any tunnel of any server: the network reads
// wui-plain's name and path in the clear, and one they shared would tie the
// covered transport to it.
func (s *Interfaces) plainApart(ctx context.Context, selfID uint, p model.StealthParams) error {
	if !p.Proven() {
		return nil
	}
	var others []model.Interface
	q := s.db.WithContext(ctx).Where("protocol = ? AND stealth IS NOT NULL AND stealth <> ''", model.ProtocolWireGuard)
	if selfID != 0 {
		q = q.Where("id <> ?", selfID)
	}
	if err := q.Find(&others).Error; err != nil {
		return fmt.Errorf("service: check the fronts: %w", err)
	}
	plain := p.Transport == model.TransportWuiPlain
	for _, o := range others {
		op := o.Stealth.V
		if !op.Proven() || (op.Transport == model.TransportWuiPlain) == plain {
			continue
		}
		switch {
		case strings.EqualFold(op.Host, p.Host):
			return invalidField("transportWs.host", "%s is the front of %s on the tunnel %q; wui-plain's front is read in the clear, so it never has a covered transport's name", p.Host, op.Transport, o.Name)
		case op.Path == p.Path:
			return invalidField("transportWs.path", "%s is the path of %s on the tunnel %q; wui-plain's path is read in the clear, so it never has a covered transport's", p.Path, op.Transport, o.Name)
		}
	}
	return nil
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
		// A transport of another name starts with nothing of the old one's
		// that is drawn: wui-plain's key, path and cookie are its own, and a
		// covered transport never takes on what wui-plain showed in the
		// clear.
		if want.Transport != iface.Stealth.V.Transport {
			want.Path, want.Cookie, want.Key, want.PreviousKey, want.KeyAt = "", "", "", "", 0
		}
	}
	if in.TransportPort != nil && want.Transport != "" {
		want.Port = *in.TransportPort
	}
	if in.TransportWS != nil && want.Proven() {
		in.TransportWS.apply(&want)
		// The old transport's path sent back with the new one's name is
		// not taken: a path is a transport's own, like its key.
		if had := iface.Stealth.V; want.Transport != had.Transport && want.Path == had.Path {
			want.Path = ""
		}
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
