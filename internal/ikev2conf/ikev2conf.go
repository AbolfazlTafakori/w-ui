// Package ikev2conf is the IKEv2/IPsec tunnel as data: what the panel asks
// for, how it is checked, and the strongSwan configuration and certificate
// files the root agent writes from it.
//
// It is the same arrangement as L2TP's (package l2tpconf): the panel cannot
// run strongSwan, so it writes the tunnel it wants into its data directory,
// and the agent -- root -- reads it, checks it again, and carries it out. So
// everything in a state is untrusted on the way in, and a value that could
// change the meaning of a configuration file is refused rather than quoted.
//
// How devices are told apart. strongSwan chooses the connection a device
// gets by the identity the device announces in IKE, before any password or
// certificate is looked at. So every device has a connection of its own --
// one per way of signing in the tunnel allows -- matched by its identity,
// which is its username, and each with a pool of one address: the device's
// own, which is what the kernel counts its traffic by. strongSwan moves a
// device between its connections by the way it signs in (a key, a password,
// a certificate), so one identity serves all four. A device that announces
// no identity of its own -- Windows signing in by password does that -- has
// no connection to land on and is refused; the guides have it sign in by
// certificate instead, which names itself.
//
// Nothing here touches the system: the rendering is pure, so it can be
// tested anywhere and the panel and the agent agree on every byte.
package ikev2conf

import (
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
	"github.com/abolfazl/w-ui/internal/vici"
)

// The ports IKEv2 uses: key exchange, and everything once a NAT is in the
// way. Both are the system's and fixed -- no built-in client has a setting
// for either -- and shared with L2TP/IPsec, whose IKEv1 the same strongSwan
// answers.
const (
	PortIKE  = l2tpconf.PortIKE
	PortNATT = l2tpconf.PortNATT
)

// SchemaVersion is the shape of State.
const SchemaVersion = 1

// Prefix names everything this tunnel owns in strongSwan: its connections,
// pools, secrets and files.
const Prefix = "wui-ikev2"

// ConfFile is the file in /etc/swanctl/conf.d the agent writes.
const ConfFile = Prefix + ".conf"

// The ways a device may sign in, as State.Modes names them.
const (
	ModeMSCHAPv2 = "mschapv2"
	ModePSK      = "psk"
	ModeRSA      = "rsa"
	ModeEAPTLS   = "eap-tls"
)

// Limits.
const (
	MaxAccounts = 1 << 16
	// MaxServerID bounds the identity the server announces: a host name or
	// an address.
	MaxServerID  = 253
	maxNameBytes = 15
	maxPEM       = 16 << 10
)

// State is the whole of what the panel asks the agent for. An absent state
// file means no IKEv2 tunnel on this server.
type State struct {
	Version   int   `json:"version"`
	Interface Iface `json:"interface"`
}

// Iface is the tunnel.
type Iface struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	// Subnet is the customers' address space; Gateway is the server's own
	// address in it.
	Subnet  string   `json:"subnet"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
	// Modes are the ways a device may sign in.
	Modes []string `json:"modes"`
	// ServerID is the identity the server announces: the name devices dial,
	// which its certificate carries.
	ServerID string `json:"serverId"`
	// The authority every device's certificate is checked against, and the
	// server's own certificate and key, in PEM.
	CACert     string    `json:"caCert"`
	ServerCert string    `json:"serverCert"`
	ServerKey  string    `json:"serverKey"`
	Accounts   []Account `json:"accounts"`
	// IfID ties every session to the tunnel's own device: what is sent to a
	// device is routed into it and encrypted there, and what a device sends
	// comes out of it. That is where its speed is limited, as on every other
	// tunnel. Zero on a kernel without the device; the sessions then run on
	// policies alone, unshaped.
	IfID uint32 `json:"ifId,omitempty"`
}

// Account is one device: the identity it announces (its username), the
// secret it signs in with -- its password, and its key in PSK -- the
// certificate it may sign in with instead, and its address.
type Account struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
	// Cert is the device's certificate, in PEM, when the tunnel signs in by
	// certificate. Only this certificate is accepted for the device: one
	// replaced -- its keys rotated -- no longer gets in, though the
	// authority still signed it.
	Cert string `json:"cert,omitempty"`
	IP   string `json:"ip"`
}

// Has reports whether the tunnel allows a way of signing in.
func (f *Iface) Has(mode string) bool {
	for _, m := range f.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ByCertificate reports whether a device may sign in by certificate.
func (f *Iface) ByCertificate() bool { return f.Has(ModeRSA) || f.Has(ModeEAPTLS) }

// ErrInvalid is wrapped by every validation failure.
var ErrInvalid = errors.New("ikev2conf: invalid state")

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

// Encode renders a state file, accounts in id order so an unchanged tunnel is
// an unchanged file.
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
	if len(f.Modes) == 0 {
		return invalid("the tunnel lets no device in: no way of signing in is on")
	}
	seen := map[string]bool{}
	for _, m := range f.Modes {
		switch m {
		case ModeMSCHAPv2, ModePSK, ModeRSA, ModeEAPTLS:
		default:
			return invalid("%q is not a way of signing in", m)
		}
		if seen[m] {
			return invalid("%q is listed twice", m)
		}
		seen[m] = true
	}
	if err := checkServerID(f.ServerID); err != nil {
		return err
	}
	// The server shows its certificate in every way but PSK, and checks the
	// devices' against the authority in the two by certificate.
	if f.Has(ModeMSCHAPv2) || f.ByCertificate() {
		ca, err := parseCert(f.CACert, "the authority's certificate")
		if err != nil {
			return err
		}
		srv, err := parseCert(f.ServerCert, "the server's certificate")
		if err != nil {
			return err
		}
		if err := srv.CheckSignatureFrom(ca); err != nil {
			return invalid("the server's certificate is not signed by the tunnel's authority")
		}
		if err := checkKey(f.ServerKey); err != nil {
			return err
		}
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
		if strings.EqualFold(a.Username, f.ServerID) {
			return invalid("account %d has the server's own identity", a.ID)
		}
		if names[a.Username] {
			return invalid("username %q is used twice", a.Username)
		}
		names[a.Username] = true
		if err := l2tpconf.CheckSecret(a.Secret); err != nil {
			return invalid("%s (account %d)", strings.TrimPrefix(err.Error(), l2tpconf.ErrInvalid.Error()+": "), a.ID)
		}
		if f.ByCertificate() {
			c, err := parseCert(a.Cert, fmt.Sprintf("account %d's certificate", a.ID))
			if err != nil {
				return err
			}
			if !certNames(c, a.Username) {
				return invalid("account %d's certificate is not for %q", a.ID, a.Username)
			}
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

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Addr().As4()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= uint32(1)<<(32-p.Bits()) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func checkName(name string) error {
	if name == "" || len(name) > maxNameBytes {
		return invalid("tunnel name %q is empty or longer than %d", name, maxNameBytes)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return invalid("tunnel name %q has %q in it", name, string(r))
	}
	return nil
}

// checkServerID: a host name or an address, nothing a file could misread.
func checkServerID(id string) error {
	if id == "" || len(id) > MaxServerID {
		return invalid("the server's identity %q is empty or too long", id)
	}
	if _, err := netip.ParseAddr(id); err == nil {
		return nil
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			continue
		}
		return invalid("the server's identity %q is neither an address nor a host name", id)
	}
	return nil
}

func parseCert(p, what string) (*x509.Certificate, error) {
	if p == "" || len(p) > maxPEM {
		return nil, invalid("%s is missing", what)
	}
	b, rest := pem.Decode([]byte(p))
	if b == nil || b.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		return nil, invalid("%s is not one certificate in PEM", what)
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return nil, invalid("%s does not parse: %v", what, err)
	}
	return c, nil
}

func checkKey(p string) error {
	if p == "" || len(p) > maxPEM {
		return invalid("the server's key is missing")
	}
	b, rest := pem.Decode([]byte(p))
	if b == nil || strings.TrimSpace(string(rest)) != "" {
		return invalid("the server's key is not one key in PEM")
	}
	switch b.Type {
	case "RSA PRIVATE KEY":
		_, err := x509.ParsePKCS1PrivateKey(b.Bytes)
		if err != nil {
			return invalid("the server's key does not parse")
		}
	case "PRIVATE KEY":
		if _, err := x509.ParsePKCS8PrivateKey(b.Bytes); err != nil {
			return invalid("the server's key does not parse")
		}
	default:
		return invalid("the server's key is a %q, not a private key", b.Type)
	}
	return nil
}

// certNames reports whether a device's certificate is for name: its common
// name and, what strongSwan checks the identity against, a name in it.
func certNames(c *x509.Certificate, name string) bool {
	if c.Subject.CommonName != name {
		return false
	}
	for _, n := range c.DNSNames {
		if n == name {
			return true
		}
	}
	return false
}

// ── Paths the panel and the agent share ─────────────────────────────────

// NewPaths is the shared directory under the panel's data directory.
func NewPaths(dataDir string) l2tpconf.Paths {
	return l2tpconf.PathsFor(dataDir, l2tpconf.ProtocolIKEv2)
}

// ── What the agent writes ───────────────────────────────────────────────

// File is one file the tunnel needs: Dir is the swanctl directory it goes in
// (x509ca, x509, private, conf.d, CertDir), or SettingsDir for strongSwan's
// own settings (strongswan.d).
type File struct {
	Dir, Name, Body string
	// Secret is a file only root may read.
	Secret bool
}

// The tunnel's certificate files, under these names in their directories.
const (
	caFile     = Prefix + "-ca.pem"
	serverFile = Prefix + "-server.pem"
)

// pinnedCert is a device's certificate as its connection names it: swanctl
// reads a relative path from x509, and the certificates are beside it.
func pinnedCert(id uint) string { return "../" + CertDir + "/" + accountFile(id) }

func accountFile(id uint) string { return Prefix + "-" + strconv.FormatUint(uint64(id), 10) + ".pem" }

// SettingsDir is File.Dir for a file in strongSwan's settings directory.
const SettingsDir = "strongswan.d"

// CertDir is the swanctl directory, beside x509, the devices' certificates
// go in. They are not credentials of their own: each is pinned by its
// device's connection, which reads it when the connections are loaded, and
// is gone with that connection. In x509 every load of the credentials would
// hand strongSwan all of them again, and one taken away would stay loaded.
const CertDir = Prefix + "-certs"

// The tunnel's three files in conf.d, split by what strongSwan loads them
// with, so one changing reloads only its own part.
const (
	PoolsFile   = Prefix + "-pools.conf"
	SecretsFile = Prefix + "-secrets.conf"
)

// What a changed file has strongSwan load again.
const (
	LoadSettings = "settings" // swanctl --reload-settings
	LoadCreds    = "creds"    // swanctl --load-creds: certificates and keys
	LoadSecrets  = "secrets"  // swanctl --load-creds: the secrets file
	LoadPools    = "pools"    // swanctl --load-pools
	LoadConns    = "conns"    // swanctl --load-conns
)

// LoadOf is what a file of the tunnel's, written or removed, has strongSwan
// load again.
func LoadOf(dir, name string) string {
	switch dir {
	case SettingsDir:
		return LoadSettings
	case CertDir:
		return LoadConns
	case "conf.d":
		switch name {
		case PoolsFile:
			return LoadPools
		case SecretsFile:
			return LoadSecrets
		}
		// The connections -- and, from before they were split, the
		// whole tunnel: every part again.
		if name == ConfFile {
			return LoadConns
		}
		return ""
	}
	return LoadCreds
}

// RadiusConn is the one connection devices signing in by password come in
// on; RADIUS (package radiusd) tells strongSwan who they are and which
// address is theirs.
const RadiusConn = Prefix + "-password"

// RadiusAddr is where the agent's RADIUS server listens: loopback, for the
// strongSwan on this machine alone.
const (
	RadiusHost = "127.0.0.1"
	RadiusPort = 18120
)

// RadiusSettings points strongSwan's eap-radius plugin at the agent's
// server, under a secret the agent makes and never writes anywhere else.
func RadiusSettings(secret string) string {
	return fmt.Sprintf(`# Written by W-UI's agent. Edits are replaced.
charon {
  plugins {
    eap-radius {
      servers {
        %s {
          address = %s
          auth_port = %d
          secret = %s
        }
      }
    }
  }
}
`, Prefix, RadiusHost, RadiusPort, secret)
}

// Files is every file the tunnel's strongSwan needs, the configuration
// last. A file that is not in it and has the tunnel's prefix is the agent's
// to remove.
func Files(s *State, radiusSecret string) []File {
	f := &s.Interface
	var out []File
	if f.Has(ModeMSCHAPv2) {
		out = append(out, File{Dir: SettingsDir, Name: Prefix + "-radius.conf", Body: RadiusSettings(radiusSecret), Secret: true})
	}
	if f.Has(ModeMSCHAPv2) || f.ByCertificate() {
		out = append(out,
			File{Dir: "x509ca", Name: caFile, Body: f.CACert},
			File{Dir: "x509", Name: serverFile, Body: f.ServerCert},
			File{Dir: "private", Name: serverFile, Body: f.ServerKey, Secret: true})
	}
	if f.ByCertificate() {
		for _, a := range sorted(f.Accounts) {
			out = append(out, File{Dir: CertDir, Name: accountFile(a.ID), Body: a.Cert})
		}
	}
	out = append(out,
		File{Dir: "conf.d", Name: PoolsFile, Body: swanctlPools(s), Secret: true},
		File{Dir: "conf.d", Name: SecretsFile, Body: swanctlSecrets(s), Secret: true},
		File{Dir: "conf.d", Name: ConfFile, Body: swanctlConns(s), Secret: true})
	return out
}

func sorted(accs []Account) []Account {
	c := append([]Account(nil), accs...)
	sort.Slice(c, func(i, j int) bool { return c[i].ID < c[j].ID })
	return c
}

// Proposals: strongSwan's own first, then what the built-in clients offer
// that it does not -- Windows' IKEv2 proposes Diffie-Hellman group 2
// (modp1024) and SHA-1 unless told otherwise, and older Androids SHA-1.
var (
	ikeProposals = []string{
		"default",
		"aes256gcm16-prfsha384-ecp384", "aes256gcm16-prfsha256-ecp256",
		"aes256-sha256-modp2048", "aes128-sha256-modp2048", "aes256-sha384-modp2048",
		"aes256-sha1-modp2048", "aes256-sha1-modp1024", "aes128-sha1-modp1024", "3des-sha1-modp1024",
	}
	espProposals = []string{
		"default",
		"aes256gcm16", "aes128gcm16",
		"aes256-sha256", "aes128-sha256", "aes256-sha1", "aes128-sha1", "3des-sha1",
	}
)

// TLSIdentity is the identity a device announces to sign in by EAP-TLS.
//
// Not its username: EAP-MSCHAPv2 and EAP-TLS look the same until the EAP
// method is chosen, which is after strongSwan has chosen the connection by
// the identity -- and the strongSwan the distributions ship cannot offer a
// device a choice of method (eap-dynamic is not packaged). So the two have
// identities of their own, and the guides say which to type.
func TLSIdentity(username string) string { return "tls-" + username }

// ConnName is a device's connection for one way of signing in.
func ConnName(id uint, mode string) string {
	return Prefix + "-" + strconv.FormatUint(uint64(id), 10) + "-" + mode
}

// PoolName is a device's pool: its one address.
func PoolName(id uint) string { return Prefix + "-" + strconv.FormatUint(uint64(id), 10) }

// ParseConnName reads an account id back from a connection's name, for the
// sessions the agent lists.
func ParseConnName(name string) (uint, bool) {
	rest, ok := strings.CutPrefix(name, Prefix+"-")
	if !ok {
		return 0, false
	}
	idPart, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(idPart, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint(n), true
}

// Swanctl renders the tunnel for /etc/swanctl/conf.d, its three files as one.
func Swanctl(s *State) string {
	return swanctlConns(s) + "\n" + swanctlPools(s) + "\n" + swanctlSecrets(s)
}

const header = "# Written by W-UI's agent from the panel's IKEv2 tunnel. Edits are replaced.\n"

// The kinds of Item.
const (
	KindConn   = "conn"
	KindPool   = "pool"
	KindSecret = "secret"
)

// Item is one thing strongSwan holds for the tunnel -- a connection, a pool,
// a secret -- which the agent can load or unload alone. The files are the
// same items written out.
type Item struct {
	Kind string
	Sec  *vici.Section
}

// Key names the item among all the tunnel's.
func (it Item) Key() string { return it.Kind + ":" + it.Sec.Name }

// Load is the VICI request that loads the item, or replaces it.
func (it Item) Load() (string, *vici.Section) {
	switch it.Kind {
	case KindConn:
		return "load-conn", &vici.Section{Entries: []vici.Entry{{Key: it.Sec.Name, Sub: it.Sec}}}
	case KindPool:
		return "load-pool", &vici.Section{Entries: []vici.Entry{{Key: it.Sec.Name, Sub: it.Sec}}}
	}
	// A secret: as swanctl sends one from a file, under its section's name.
	msg := &vici.Section{}
	msg.Value("id", it.Sec.Name).Value("type", "IKE")
	var owners []string
	for _, e := range it.Sec.Entries {
		switch e.Key {
		case "secret":
			msg.Value("data", e.Values[0])
		case "id":
			owners = append(owners, e.Values[0])
		}
	}
	msg.List("owners", owners...)
	return "load-shared", msg
}

// Unload is the VICI request that unloads the item.
func (it Item) Unload() (string, *vici.Section) {
	msg := &vici.Section{}
	switch it.Kind {
	case KindConn:
		return "unload-conn", msg.Value("name", it.Sec.Name)
	case KindPool:
		return "unload-pool", msg.Value("name", it.Sec.Name)
	}
	return "unload-shared", msg.Value("id", it.Sec.Name)
}

// Items is every connection, pool and secret of the tunnel.
func Items(s *State) []Item {
	f := &s.Interface
	var out []Item
	if f.Has(ModeMSCHAPv2) {
		out = append(out, Item{KindConn, radiusConn(f)})
	}
	for _, a := range sorted(f.Accounts) {
		for _, m := range []string{ModePSK, ModeRSA, ModeEAPTLS} {
			if f.Has(m) {
				out = append(out, Item{KindConn, conn(f, a, m)})
			}
		}
	}
	for _, a := range sorted(f.Accounts) {
		out = append(out, Item{KindPool, pool(f, a)})
	}
	for _, a := range sorted(f.Accounts) {
		// A device's key for PSK is its password. Each is bound to the
		// device's identity, so a key without one -- L2TP's, which every
		// L2TP customer holds and the same strongSwan loads -- never
		// stands in for a device's. Passwords for EAP are RADIUS's, not
		// strongSwan's.
		if f.Has(ModePSK) {
			sec := &vici.Section{Name: "ike-" + PoolName(a.ID)}
			sec.Quoted("id", a.Username).Quoted("secret", a.Secret)
			out = append(out, Item{KindSecret, sec})
		}
	}
	return out
}

// swanctlBlock writes the items of one kind under one top-level section.
func swanctlBlock(s *State, top, kind string) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(top + " {\n")
	for _, it := range Items(s) {
		if it.Kind == kind {
			it.Sec.Swanctl(&b, 1)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func swanctlConns(s *State) string   { return swanctlBlock(s, "connections", KindConn) }
func swanctlPools(s *State) string   { return swanctlBlock(s, "pools", KindPool) }
func swanctlSecrets(s *State) string { return swanctlBlock(s, "secrets", KindSecret) }

func pool(f *Iface, a Account) *vici.Section {
	p := &vici.Section{Name: PoolName(a.ID)}
	p.Value("addrs", a.IP+"/32")
	if len(f.DNS) > 0 {
		p.List("dns", f.DNS...)
	}
	return p
}

// child is a connection's one child: every address, into the tunnel's
// device when it has one.
func child(c *vici.Section, name string, f *Iface) {
	ch := c.Sub("children").Sub(name)
	ch.List("local_ts", "0.0.0.0/0")
	ch.List("esp_proposals", espProposals...)
	ch.Value("dpd_action", "clear")
	if f.IfID != 0 {
		id := strconv.FormatUint(uint64(f.IfID), 10)
		ch.Value("if_id_in", id).Value("if_id_out", id)
	}
}

// radiusConn is the connection for every device signing in by password,
// whatever identity it announces -- Windows announces its address.
func radiusConn(f *Iface) *vici.Section {
	c := &vici.Section{Name: RadiusConn}
	c.Value("version", "2")
	c.List("proposals", ikeProposals...)
	// The address RADIUS gives, which is the device's own.
	c.List("pools", "radius")
	// Not replaced by identity: the identity here can be a device's local
	// address, which two customers in two homes may well share. The agent
	// holds each device to one session, by its address.
	c.Value("unique", "no")
	c.Value("mobike", "yes")
	c.Value("fragmentation", "yes")
	c.Value("dpd_delay", "30s")
	c.Value("send_cert", "always")
	c.Value("send_certreq", "no")
	local := c.Sub("local")
	local.Value("auth", "pubkey")
	local.FileList("certs", serverFile, f.ServerCert)
	local.Quoted("id", f.ServerID)
	remote := c.Sub("remote")
	remote.Value("auth", "eap-radius")
	remote.Value("eap_id", "%any")
	child(c, RadiusConn, f)
	return c
}

func conn(f *Iface, a Account, mode string) *vici.Section {
	name := ConnName(a.ID, mode)
	c := &vici.Section{Name: name}
	c.Value("version", "2")
	c.List("proposals", ikeProposals...)
	c.List("pools", PoolName(a.ID))
	// One sign-in, one session: the device signing in again replaces the
	// session before, as every other protocol here does.
	c.Value("unique", "replace")
	// A phone moves between networks; certificates make big messages.
	c.Value("mobike", "yes")
	c.Value("fragmentation", "yes")
	c.Value("dpd_delay", "30s")
	if mode != ModePSK {
		// The server's certificate goes with every answer: a device that
		// did not ask for it (iOS asks for none) still has to check it.
		c.Value("send_cert", "always")
	}
	local := c.Sub("local")
	if mode == ModePSK {
		local.Value("auth", "psk")
	} else {
		local.Value("auth", "pubkey")
		local.FileList("certs", serverFile, f.ServerCert)
	}
	local.Quoted("id", f.ServerID)
	remote := c.Sub("remote")
	if mode == ModeEAPTLS {
		remote.Quoted("id", TLSIdentity(a.Username))
	} else {
		remote.Quoted("id", a.Username)
	}
	switch mode {
	case ModePSK:
		remote.Value("auth", "psk")
	case ModeRSA:
		remote.Value("auth", "pubkey")
		remote.FileList("certs", pinnedCert(a.ID), a.Cert)
		remote.FileList("cacerts", caFile, f.CACert)
	case ModeEAPTLS:
		remote.Value("auth", "eap-tls")
		// Whatever EAP identity the device gives -- clients differ -- it
		// is the device's own certificate, pinned here, that lets it in.
		remote.Value("eap_id", "%any")
		remote.FileList("certs", pinnedCert(a.ID), a.Cert)
		remote.FileList("cacerts", caFile, f.CACert)
		// The server asks for no certificate where the device has none to
		// give; Windows otherwise stalls on the request.
		c.Value("send_certreq", "no")
	}
	child(c, name, f)
	return c
}
