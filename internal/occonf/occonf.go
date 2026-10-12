// Package occonf is the OpenConnect tunnel as data: what the panel asks for,
// how it is checked, and the ocserv and RADIUS-client files the root agent
// writes from it.
//
// It is the arrangement L2TP's and IKEv2's are (packages l2tpconf and
// ikev2conf): the panel cannot run ocserv, so it writes the tunnel it wants
// into its data directory, and the agent -- root -- reads it, checks it
// again, and carries it out. Everything in a state is untrusted on the way
// in, and a value that could change the meaning of a configuration file is
// refused rather than quoted.
//
// How a device signs in. ocserv asks the agent's own RADIUS server
// (package radiusd) for every sign-in, by PAP: the agent knows each
// device's password and address, ends the device's other sessions before
// it lets the new one in -- one sign-in, one connection, the newest kept --
// and hands ocserv the device's own address, which is what the kernel
// counts its traffic by. ocserv's own password file is not used, so no
// password is written into ocserv's files at all.
//
// Nothing here touches the system: the rendering is pure, so it can be
// tested anywhere and the panel and the agent agree on every byte.
package occonf

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// SchemaVersion is the shape of State.
const SchemaVersion = 1

// Limits.
const (
	MaxAccounts = 1 << 16
	maxNameLen  = 15
	maxPEM      = 32 << 10
	// MinSecret and MaxSecret bound the camouflage secret: what a device puts
	// after "?" in the server's address.
	MinCamouflage = 8
	MaxCamouflage = 64
	// MinMTU and MaxMTU are what a tunnel inside TLS or DTLS, inside a
	// 1500-byte link, can be.
	MinMTU = 1200
	MaxMTU = 1460
	// DefaultMTU leaves room for DTLS, UDP and IP on a 1500-byte link, and
	// for the PPPoE and mobile links that are less.
	DefaultMTU = 1400
)

// RadiusPort is where the agent's RADIUS server answers ocserv, on loopback.
// IKEv2's is 18120; each has its own, with its own secret.
const RadiusPort = 18121

// State is the whole of what the panel asks the agent for. An absent state
// file means no OpenConnect tunnel on this server.
type State struct {
	Version   int   `json:"version"`
	Interface Iface `json:"interface"`
}

// Iface is the tunnel.
type Iface struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	// Port is where ocserv listens: TLS on TCP, DTLS on UDP, the same number.
	Port int `json:"port"`
	// Subnet is the customers' address space; Gateway is the server's own
	// address in it.
	Subnet  string   `json:"subnet"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
	MTU     int      `json:"mtu"`
	// DTLS lets a device carry its traffic over UDP once signed in, which is
	// faster; without it everything is TLS over TCP.
	DTLS bool `json:"dtls"`
	// Camouflage hides the server from anything that does not know the
	// secret: without "?secret" in the address it answers like an ordinary
	// web server. ocserv 1.2 and later; an older one ignores it, and the
	// agent says so.
	Camouflage       bool   `json:"camouflage"`
	CamouflageSecret string `json:"camouflageSecret,omitempty"`
	// The server's certificate and key, in PEM: the server's own, or the
	// panel's when it has one for this name.
	Cert     string    `json:"cert"`
	Key      string    `json:"key"`
	Accounts []Account `json:"accounts"`
}

// Account is one device: its username and password, and its address.
type Account struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
	IP       string `json:"ip"`
}

// ErrInvalid is wrapped by every validation failure.
var ErrInvalid = errors.New("occonf: invalid state")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Decode reads a state file and validates it.
func Decode(raw []byte) (*State, error) {
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, invalid("not JSON: %v", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Encode renders a state file, accounts in id order so an unchanged tunnel
// is an unchanged file.
func Encode(s *State) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	c := *s
	c.Interface.Accounts = append([]Account(nil), s.Interface.Accounts...)
	sort.Slice(c.Interface.Accounts, func(i, j int) bool { return c.Interface.Accounts[i].ID < c.Interface.Accounts[j].ID })
	return json.MarshalIndent(c, "", "  ")
}

// Validate checks everything that will be written into a file.
func (s *State) Validate() error {
	if s.Version != SchemaVersion {
		return invalid("state version %d, this build reads %d", s.Version, SchemaVersion)
	}
	f := &s.Interface
	if f.ID == 0 {
		return invalid("the tunnel has no id")
	}
	if err := checkName(f.Name); err != nil {
		return err
	}
	if f.Port < 1 || f.Port > 65535 {
		return invalid("port %d is out of range", f.Port)
	}
	pfx, err := netip.ParsePrefix(f.Subnet)
	if err != nil || !pfx.Addr().Is4() || pfx != pfx.Masked() || pfx.Bits() > 30 {
		return invalid("subnet %q is not an IPv4 network of at least four addresses", f.Subnet)
	}
	gw, err := netip.ParseAddr(f.Gateway)
	if err != nil || !gw.Is4() || !pfx.Contains(gw) || gw == pfx.Addr() {
		return invalid("gateway %q is not a host in %s", f.Gateway, f.Subnet)
	}
	if len(f.DNS) > 4 {
		return invalid("%d resolvers; more than four is a mistake", len(f.DNS))
	}
	for _, d := range f.DNS {
		if a, err := netip.ParseAddr(d); err != nil || !a.Is4() {
			return invalid("resolver %q is not an IPv4 address", d)
		}
	}
	if f.MTU < MinMTU || f.MTU > MaxMTU {
		return invalid("MTU %d is outside %d-%d", f.MTU, MinMTU, MaxMTU)
	}
	if f.Camouflage {
		if err := CheckCamouflage(f.CamouflageSecret); err != nil {
			return err
		}
	}
	if err := checkCertPair(f.Cert, f.Key); err != nil {
		return err
	}
	if len(f.Accounts) > MaxAccounts {
		return invalid("%d accounts on one tunnel", len(f.Accounts))
	}
	ids := map[uint]bool{}
	names := map[string]bool{}
	ips := map[netip.Addr]bool{}
	broadcast := lastAddr(pfx)
	for _, a := range f.Accounts {
		if a.ID == 0 || ids[a.ID] {
			return invalid("account id %d is missing or repeated", a.ID)
		}
		ids[a.ID] = true
		if err := l2tpconf.CheckUsername(a.Username); err != nil {
			return invalid("%s (account %d)", strings.TrimPrefix(err.Error(), l2tpconf.ErrInvalid.Error()+": "), a.ID)
		}
		if names[a.Username] {
			return invalid("username %q is used twice", a.Username)
		}
		names[a.Username] = true
		if err := l2tpconf.CheckSecret(a.Secret); err != nil {
			return invalid("%s (account %d)", strings.TrimPrefix(err.Error(), l2tpconf.ErrInvalid.Error()+": "), a.ID)
		}
		ip, err := netip.ParseAddr(a.IP)
		if err != nil || !pfx.Contains(ip) || ip == gw || ip == pfx.Addr() || ip == broadcast {
			return invalid("account %d address %q is not a free host in %s", a.ID, a.IP, f.Subnet)
		}
		if ips[ip] {
			return invalid("address %s is given to two accounts", ip)
		}
		ips[ip] = true
	}
	return nil
}

// CheckCamouflage: letters and digits only, so the secret is the same
// typed into any client and written into any file.
func CheckCamouflage(s string) error {
	if len(s) < MinCamouflage || len(s) > MaxCamouflage {
		return invalid("the camouflage secret is %d to %d letters and digits", MinCamouflage, MaxCamouflage)
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return invalid("the camouflage secret is letters and digits only")
	}
	return nil
}

func checkName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return invalid("tunnel name %q is empty or longer than %d", name, maxNameLen)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return invalid("tunnel name %q has %q in it", name, string(r))
	}
	return nil
}

// checkCertPair: a certificate and the key for it, nothing else in either.
func checkCertPair(cert, key string) error {
	if len(cert) == 0 || len(cert) > maxPEM || len(key) == 0 || len(key) > maxPEM {
		return invalid("the server's certificate or key is missing or too large")
	}
	rest := []byte(cert)
	var leaf *x509.Certificate
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type != "CERTIFICATE" {
			return invalid("the certificate file holds a %q block", b.Type)
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return invalid("the server's certificate: %v", err)
		}
		if leaf == nil {
			leaf = c
		}
	}
	if leaf == nil || strings.TrimSpace(string(rest)) != "" {
		return invalid("the certificate file is not PEM certificates and nothing else")
	}
	kb, krest := pem.Decode([]byte(key))
	if kb == nil || strings.TrimSpace(string(krest)) != "" {
		return invalid("the key file is not one PEM key")
	}
	var pub any
	switch kb.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
		if err != nil {
			return invalid("the server's key: %v", err)
		}
		pub = &k.PublicKey
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(kb.Bytes)
		if err != nil {
			return invalid("the server's key: %v", err)
		}
		pub = &k.PublicKey
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
		if err != nil {
			return invalid("the server's key: %v", err)
		}
		type publicer interface{ Public() crypto.PublicKey }
		p, ok := k.(publicer)
		if !ok {
			return invalid("the server's key is of a kind ocserv cannot use")
		}
		pub = p.Public()
	default:
		return invalid("the key file holds a %q block", kb.Type)
	}
	type equaler interface{ Equal(crypto.PublicKey) bool }
	if e, ok := pub.(equaler); !ok || !e.Equal(leaf.PublicKey) {
		return invalid("the server's key is not the certificate's")
	}
	return nil
}

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Addr().As4()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= uint32(1)<<(32-p.Bits()) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// NewPaths is where the panel and the agent meet for this tunnel.
func NewPaths(dataDir string) l2tpconf.Paths {
	return l2tpconf.PathsFor(dataDir, l2tpconf.ProtocolOpenConnect)
}

// The files the agent writes, in Dir (ConfDir), all root's and the
// key and RADIUS secret readable by root only.
const (
	ConfDir     = "/etc/wui-ocserv"
	ConfFile    = "ocserv.conf"
	RadcliFile  = "radiusclient.conf"
	CertFile    = "server-cert.pem"
	KeyFile     = "server-key.pem"
	SocketFile  = "/run/wui-ocserv.socket"
	OcctlSocket = "/run/wui-occtl.socket"
	PIDFile     = "/run/wui-ocserv.pid"
	DeviceName  = "vpns"
	radcliDict  = "/etc/radcli/dictionary"
)

// File is one file the agent writes.
type File struct {
	Name   string
	Body   string
	Secret bool
}

// Files are everything the agent writes into ConfDir.
func Files(s *State, radiusSecret string) []File {
	f := &s.Interface
	return []File{
		{Name: ConfFile, Body: Ocserv(s)},
		{Name: RadcliFile, Body: Radcli(radiusSecret), Secret: true},
		{Name: CertFile, Body: strings.TrimSpace(f.Cert) + "\n"},
		{Name: KeyFile, Body: strings.TrimSpace(f.Key) + "\n", Secret: true},
	}
}

// Radcli is the RADIUS client's configuration: the agent's server on
// loopback, its secret written beside it rather than in a servers file of
// its own.
func Radcli(secret string) string {
	addr := "127.0.0.1:" + strconv.Itoa(RadiusPort)
	var b strings.Builder
	b.WriteString("# Written by W-UI for its OpenConnect tunnel; replaced on every change.\n")
	b.WriteString("nas-identifier wui-ocserv\n")
	fmt.Fprintf(&b, "authserver %s:%s\n", addr, secret)
	fmt.Fprintf(&b, "dictionary %s\n", radcliDict)
	b.WriteString("default_realm\n")
	b.WriteString("radius_timeout 5\n")
	b.WriteString("radius_retries 2\n")
	b.WriteString("bindaddr *\n")
	return b.String()
}

// Ocserv is ocserv's configuration.
func Ocserv(s *State) string {
	f := &s.Interface
	pfx := netip.MustParsePrefix(f.Subnet)
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Written by W-UI for OpenConnect tunnel %s; replaced on every change.", f.Name)
	// Every sign-in is the agent's to decide, and so is every device's
	// address and resolvers: groupconfig has ocserv take them from its
	// answer.
	w(`auth = "radius[config=%s/%s,groupconfig=true]"`, ConfDir, RadcliFile)
	w("tcp-port = %d", f.Port)
	if f.DTLS {
		w("udp-port = %d", f.Port)
	}
	w("run-as-user = nobody")
	w("run-as-group = nogroup")
	w("socket-file = %s", SocketFile)
	w("occtl-socket-file = %s", OcctlSocket)
	w("use-occtl = true")
	w("pid-file = %s", PIDFile)
	w("server-cert = %s/%s", ConfDir, CertFile)
	w("server-key = %s/%s", ConfDir, KeyFile)
	w("isolate-workers = true")
	w("max-clients = 0")
	// No limit of ocserv's own: the agent ends a device's older session as
	// the new one signs in, which a limit here would refuse instead.
	w("max-same-clients = 0")
	w("rate-limit-ms = 100")
	w("server-stats-reset-time = 604800")
	w("keepalive = 32400")
	w("dpd = 90")
	w("mobile-dpd = 1800")
	w("switch-to-tcp-timeout = 25")
	w("try-mtu-discovery = true")
	w(`tls-priorities = "NORMAL:%%SERVER_PRECEDENCE:%%COMPAT:-VERS-SSL3.0:-VERS-TLS1.0:-VERS-TLS1.1"`)
	w("auth-timeout = 240")
	w("min-reauth-time = 2")
	w("max-ban-score = 80")
	w("ban-reset-time = 300")
	w("cookie-timeout = 300")
	w("deny-roaming = false")
	w("rekey-time = 172800")
	w("rekey-method = ssl")
	w("device = %s", DeviceName)
	w("predictable-ips = true")
	w("ipv4-network = %s", pfx.Addr())
	w("ipv4-netmask = %s", netmask(pfx))
	for _, d := range f.DNS {
		w("dns = %s", d)
	}
	w("tunnel-all-dns = true")
	w("route = default")
	w("mtu = %d", f.MTU)
	w("ping-leases = false")
	w("cisco-client-compat = true")
	w("dtls-legacy = true")
	w("compression = false")
	if f.Camouflage {
		w("camouflage = true")
		w(`camouflage_secret = "%s"`, f.CamouflageSecret)
		w(`camouflage_realm = "Restricted Content"`)
	}
	return b.String()
}

func netmask(p netip.Prefix) string {
	m := uint32(0xffffffff) << (32 - p.Bits())
	return fmt.Sprintf("%d.%d.%d.%d", m>>24, m>>16&0xff, m>>8&0xff, m&0xff)
}

// URL is the address a device dials: host and port, and the camouflage
// secret after "?" when the tunnel has one.
func URL(host string, port int, camouflage bool, secret string) string {
	h := host
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		h = "[" + h + "]"
	}
	u := h
	if port != 443 {
		u += ":" + strconv.Itoa(port)
	}
	if camouflage && secret != "" {
		u += "/?" + secret
	}
	return u
}
