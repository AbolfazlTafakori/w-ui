// Package l2tpconf is the PPP tunnels as data -- L2TP/IPsec, and PPTP beside
// it -- what the panel asks for, how it is checked, and the configuration
// files the server stack reads.
//
// L2TP is served by programs that only run as root -- pppd refuses to start
// otherwise, and the kernel's L2TP sockets are a family the panel's sandbox
// does not allow -- so the panel does not run them. It writes the desired
// state to a file in its data directory, and a small agent running as root
// (package l2tpagent) reads it, checks it again and carries it out.
//
// That file is the only thing the panel can say to root, so everything in it
// is treated as untrusted on the way in. Validate refuses anything that could
// change the meaning of a configuration file once written into it -- a quote
// in a password, a newline in a name -- rather than escaping it: a value that
// cannot reach a file in the wrong shape is safer than one that relies on
// being quoted correctly in four different file formats.
//
// Nothing here touches the system. The rendering is pure so it can be tested
// anywhere, and so the panel and the agent agree on every byte.
package l2tpconf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The ports L2TP/IPsec uses. None of them can be moved: the clients built
// into Windows, macOS and iOS have no setting for any of them, which is also
// why a server carries one L2TP tunnel at most.
const (
	PortIKE  = 500  // IPsec key exchange
	PortNATT = 4500 // IPsec through NAT
	PortL2TP = 1701 // L2TP itself, only ever inside IPsec
	// PortPPTP is PPTP's control connection, over TCP; its data goes in GRE
	// beside it. Fixed too: no built-in client has a setting for it.
	PortPPTP = 1723
)

// Protocols a state can describe.
const (
	ProtocolL2TP = "l2tp"
	ProtocolPPTP = "pptp"
	// ProtocolIKEv2 is the IKEv2 tunnel, which the same agent carries
	// (package ikev2conf describes it); named here for its directory.
	ProtocolIKEv2 = "ikev2"
	// ProtocolOpenConnect is the OpenConnect tunnel (package occonf), the
	// same agent's too.
	ProtocolOpenConnect = "openconnect"
)

// ServerName is the name this server gives itself to pppd, and the server
// column of the lines it writes to /etc/ppp/chap-secrets. pppd matches a
// line only when that column is its own name, so the panel's credentials are
// used by the panel's tunnel alone and never by any other PPP link the
// machine might run.
const ServerName = "wui-l2tp"

// ServerNamePPTP is the same for the PPTP tunnel: its own name, so each
// tunnel's pppd matches its own customers' lines and never the other's.
const ServerNamePPTP = "wui-pptp"

// SchemaVersion is the shape of State. The agent refuses a state written in
// a shape it does not know rather than guessing at it.
const SchemaVersion = 1

// RunDir is where the agent keeps the files it hands to xl2tpd and pppd.
// It is root's own, outside the panel's data directory: the panel writes
// nothing that root executes or hands to pppd unchecked.
const RunDir = "/run/wui-l2tp"

// RunFile is a file in RunDir. Always forward slashes: it is written into
// configuration files read on Linux, whatever builds them.
func RunFile(name string) string { return path.Join(RunDir, name) }

// Limits.
const (
	MinMTU = 576
	MaxMTU = 1500
	// DefaultMTU leaves room for PPP, L2TP, UDP, ESP and NAT-T around every
	// packet on a path narrower than 1500 bytes: mobile networks and PPPoE
	// links often are, and a packet that has to be fragmented after it is
	// encrypted is one some networks drop. 1280 is what the most deployed
	// L2TP/IPsec servers use for the same reason.
	DefaultMTU = 1280
	// MinPSK is one character. A key is handed to every customer, so it keeps
	// out those who are not one and nothing more; and an operator moving an
	// existing service here keeps the key its devices already hold, however
	// short -- "123" is common. WeakPSK is below where the panel says so.
	MinPSK       = 1
	WeakPSK      = 8
	MaxPSK       = 64
	MinSecret    = 6
	MaxSecret    = 64
	MaxUsername  = 64
	MaxAccounts  = 1 << 16
	maxNameBytes = 15
)

// State is the whole of what the panel asks the agent for. An absent state
// file means no L2TP tunnel on this server.
type State struct {
	Version int `json:"version"`
	// Protocol is "pptp" for the PPTP tunnel; empty or "l2tp" is L2TP, as
	// every state written before there was a second one is.
	Protocol  string `json:"protocol,omitempty"`
	Interface Iface  `json:"interface"`
}

// IsPPTP is whether the state describes the PPTP tunnel.
func (s *State) IsPPTP() bool { return s.Protocol == ProtocolPPTP }

// Server is the name the state's pppd gives itself.
func (s *State) Server() string {
	if s.IsPPTP() {
		return ServerNamePPTP
	}
	return ServerName
}

// Iface is the tunnel.
type Iface struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	// Subnet is the customers' address space; Gateway is the server's own
	// address in it, the local end of every PPP session and the resolver the
	// customers are handed when the panel's resolver is on.
	Subnet  string   `json:"subnet"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
	MTU     int      `json:"mtu"`
	PSK     string   `json:"psk"`
	// AllowPlain also accepts L2TP without IPsec, for the routers whose
	// L2TP client has none -- ASUS, OpenWrt, an ISP-style WAN connection.
	// Those sessions are not encrypted.
	AllowPlain bool      `json:"allowPlain,omitempty"`
	Accounts   []Account `json:"accounts"`
}

// Account is one device: the credentials it signs in with and the address it
// is always given, which is what the kernel counts its traffic by.
type Account struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
	IP       string `json:"ip"`
}

// ErrInvalid is wrapped by every validation failure.
var ErrInvalid = errors.New("l2tpconf: invalid state")

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

// Validate checks everything that will be written into a configuration file.
func (s *State) Validate() error {
	if s.Version != SchemaVersion {
		return invalid("state version %d, this build reads %d", s.Version, SchemaVersion)
	}
	if s.Protocol != "" && s.Protocol != ProtocolL2TP && s.Protocol != ProtocolPPTP {
		return invalid("protocol %q is neither %s nor %s", s.Protocol, ProtocolL2TP, ProtocolPPTP)
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
		return invalid("%d resolvers; a PPP link hands out two, and more than four is a mistake", len(f.DNS))
	}
	for _, d := range f.DNS {
		if a, err := netip.ParseAddr(d); err != nil || !a.Is4() {
			return invalid("resolver %q is not an IPv4 address", d)
		}
	}
	if f.MTU < MinMTU || f.MTU > MaxMTU {
		return invalid("MTU %d is outside %d-%d", f.MTU, MinMTU, MaxMTU)
	}
	if s.IsPPTP() {
		// PPTP has no key and no IPsec to go without.
		if f.PSK != "" || f.AllowPlain {
			return invalid("a PPTP tunnel has no pre-shared key")
		}
	} else if err := checkPSK(f.PSK); err != nil {
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
		if err := CheckUsername(a.Username); err != nil {
			return err
		}
		if names[a.Username] {
			return invalid("username %q is used twice", a.Username)
		}
		names[a.Username] = true
		if err := CheckSecret(a.Secret); err != nil {
			return fmt.Errorf("%w (account %d)", err, a.ID)
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
	host := uint32(1)<<(32-p.Bits()) - 1
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// checkName is the tunnel name rule the rest of the panel applies, checked
// again here because it reaches file names.
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

// CheckUsername is the rule for a sign-in name: letters, digits and - _ . @,
// which is also all a pppd secrets line can hold without quoting.
func CheckUsername(u string) error {
	if u == "" || len(u) > MaxUsername {
		return invalid("username %q is empty or longer than %d", u, MaxUsername)
	}
	for _, r := range u {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '@' {
			continue
		}
		return invalid("username %q has %q in it; letters, digits and - _ . @ only", u, string(r))
	}
	return nil
}

// CheckSecret is the rule for a password: printable ASCII with no space, no
// quote and no backslash. Those three are what a pppd secrets line uses for
// its own syntax, and a password that contains them is one quoting mistake
// away from being a different line.
func CheckSecret(s string) error {
	if len(s) < MinSecret || len(s) > MaxSecret {
		return invalid("a password is %d to %d characters", MinSecret, MaxSecret)
	}
	return printable(s, "a password")
}

func checkPSK(s string) error {
	if len(s) < MinPSK || len(s) > MaxPSK {
		return invalid("the pre-shared key is %d to %d characters", MinPSK, MaxPSK)
	}
	return printable(s, "the pre-shared key")
}

// CheckPSK is checkPSK for callers outside the package.
func CheckPSK(s string) error { return checkPSK(s) }

func printable(s, what string) error {
	for _, r := range s {
		if r < 0x21 || r > 0x7e || r == '"' || r == '\\' || r == '#' || r == '\'' {
			return invalid("%s can only hold printable characters other than space, quotes, # and \\", what)
		}
	}
	return nil
}

// Fingerprint identifies a state for "has anything changed" checks.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Paths are the files in the panel's data directory the two sides share.
type Paths struct{ Dir string }

// NewPaths is the shared directory under the panel's data directory.
func NewPaths(dataDir string) Paths { return Paths{Dir: filepath.Join(dataDir, "l2tp")} }

// NewPPTPPaths is the PPTP tunnel's, beside it.
func NewPPTPPaths(dataDir string) Paths { return Paths{Dir: filepath.Join(dataDir, "pptp")} }

// PathsFor is the directory of a protocol's tunnel.
func PathsFor(dataDir, protocol string) Paths {
	if protocol == ProtocolPPTP {
		return NewPPTPPaths(dataDir)
	}
	if protocol == ProtocolIKEv2 {
		return Paths{Dir: filepath.Join(dataDir, "ikev2")}
	}
	if protocol == ProtocolOpenConnect {
		return Paths{Dir: filepath.Join(dataDir, "openconnect")}
	}
	return NewPaths(dataDir)
}

// State is what the panel writes and the agent reads.
func (p Paths) State() string { return filepath.Join(p.Dir, "state.json") }

// Report is what the agent writes and the panel reads.
func (p Paths) Report() string { return filepath.Join(p.Dir, "report.json") }

// KickDir holds a file per account the panel wants disconnected, named by
// the account's id.
func (p Paths) KickDir() string { return filepath.Join(p.Dir, "kick") }

// KickFile is the request to end one account's session.
func (p Paths) KickFile(account uint) string {
	return filepath.Join(p.KickDir(), strconv.FormatUint(uint64(account), 10))
}

// ParseKickName reads an account id back from a request's file name.
func ParseKickName(name string) (uint, bool) {
	n, err := strconv.ParseUint(name, 10, 32)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != name {
		return 0, false
	}
	return uint(n), true
}

// Agent states, as the report gives them.
const (
	AgentRunning    = "running"
	AgentInstalling = "installing"
	AgentFailed     = "failed"
)

// Report is the agent's answer: whether the tunnel is up, and who is on it.
type Report struct {
	// At is when this was written. A report older than a few collection
	// ticks means the agent is not running.
	At int64 `json:"at"`
	// State is running, installing or failed; Error says why when it is not
	// running.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Notice is what the operator should know of a tunnel that is running:
	// something asked for that this server cannot do.
	Notice string `json:"notice,omitempty"`
	// Applied is the fingerprint of the state the tunnel is carrying.
	Applied  string    `json:"applied,omitempty"`
	Sessions []Session `json:"sessions"`
}

// Session is one device connected right now.
type Session struct {
	Account uint   `json:"account"`
	Device  string `json:"device"`
	// Endpoint is the public address the device connects from.
	Endpoint string `json:"endpoint,omitempty"`
	// Since is when the session started, in unix seconds.
	Since int64 `json:"since"`
	// RX and TX are the session's own counters on its PPP device, as the
	// server sees them: RX is what the customer sent.
	RX uint64 `json:"rx"`
	TX uint64 `json:"tx"`
}

// secretsFence is the pair of lines that fence a tunnel's own lines in
// /etc/ppp/chap-secrets. Everything outside them belongs to whoever else
// uses that file and is kept exactly as it was; each tunnel has its own pair,
// so writing one tunnel's customers never touches the other's.
func secretsFence(protocol string) (begin, end string) {
	if protocol == ProtocolPPTP {
		return "# BEGIN W-UI PPTP -- written by the panel; edits here are replaced", "# END W-UI PPTP"
	}
	return "# BEGIN W-UI L2TP -- written by the panel; edits here are replaced", "# END W-UI L2TP"
}

// ChapSecrets returns /etc/ppp/chap-secrets with a tunnel's block replaced
// by one line per account, or removed when s is nil. Lines outside the block
// are kept byte for byte.
func ChapSecrets(existing, protocol string, s *State) string {
	begin, end := secretsFence(protocol)
	var kept []string
	inside := false
	for _, line := range strings.Split(existing, "\n") {
		switch {
		case strings.TrimSpace(line) == begin:
			inside = true
			continue
		case strings.TrimSpace(line) == end:
			inside = false
			continue
		case inside:
			continue
		}
		kept = append(kept, line)
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	var b strings.Builder
	for _, line := range kept {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if s == nil {
		return b.String()
	}
	accs := append([]Account(nil), s.Interface.Accounts...)
	sort.Slice(accs, func(i, j int) bool { return accs[i].Username < accs[j].Username })
	b.WriteString(begin + "\n")
	for _, a := range accs {
		// client  server  secret  address: the address column is what pppd
		// gives the peer, so a device always comes up on its own address.
		fmt.Fprintf(&b, "%s %s \"%s\" %s\n", a.Username, s.Server(), a.Secret, a.IP)
	}
	b.WriteString(end + "\n")
	return b.String()
}

// XL2TPD renders xl2tpd's configuration.
//
// xl2tpd does not hand out addresses: pppd takes each device's from its
// secrets line, which is what keeps a device on the one address its usage is
// counted by. Only CHAP is offered -- pppd narrows it to MS-CHAPv2, the
// method every built-in client uses -- and nothing is accepted
// unauthenticated.
func XL2TPD(s *State) string {
	var b strings.Builder
	b.WriteString("[global]\n")
	fmt.Fprintf(&b, "port = %d\n", PortL2TP)
	b.WriteString("access control = no\n")
	fmt.Fprintf(&b, "auth file = %s\n", RunFile("l2tp-secrets"))
	b.WriteString("\n[lns default]\n")
	// Never asks a client to prove a tunnel password: the clients built
	// into Windows, macOS and iOS have none. One that asks the server to
	// prove it -- a modem with a "Tunnel Password" field -- is answered with
	// the secret in the auth file.
	b.WriteString("challenge = no\n")
	b.WriteString("assign ip = no\n")
	b.WriteString("require chap = yes\n")
	b.WriteString("refuse pap = yes\n")
	b.WriteString("require authentication = yes\n")
	fmt.Fprintf(&b, "name = %s\n", ServerName)
	fmt.Fprintf(&b, "pppoptfile = %s\n", RunFile("options"))
	b.WriteString("length bit = yes\n")
	return b.String()
}

// TunnelSecrets renders xl2tpd's auth file: the secret a modem's L2TP
// client may ask the server to prove it knows, in the "Tunnel Password"
// field of the Greenpacket modems Irancell sells (TF-i60, GP-2101) and of
// Huawei's. Iranian services tell customers to type their key there, so it
// is the tunnel's pre-shared key: one key for the customer to know, wherever
// a device asks for one.
//
// xl2tpd reads a secret up to the first space or "!", and a ";" or "#" as the
// start of a comment; a key holding any of those cannot be carried, and the
// file then holds none -- a modem that asks is refused, and every other
// client is as before.
func TunnelSecrets(s *State) string {
	if !TunnelSecretUsable(s.Interface.PSK) {
		return "# the pre-shared key holds a character this file cannot carry\n"
	}
	return "* * " + s.Interface.PSK + "\n"
}

// TunnelSecretUsable reports whether a key can be xl2tpd's tunnel secret.
func TunnelSecretUsable(psk string) bool {
	return psk != "" && !strings.ContainsAny(psk, "!;# \t")
}

// PPPOptions renders the options pppd runs each session with.
func PPPOptions(s *State) string {
	f := s.Interface
	var b strings.Builder
	fmt.Fprintf(&b, "name %s\n", ServerName)
	// The server's end of every session; the peer's comes from its line in
	// the secrets file.
	fmt.Fprintf(&b, "%s:\n", f.Gateway)
	b.WriteString("auth\n")
	b.WriteString("require-mschap-v2\n")
	if f.AllowPlain {
		// Routers' L2TP clients are built on all sorts of PPP; CHAP is the
		// one every one of them has. It is still a challenge, never the
		// password itself -- and against a recorded sign-in a long random
		// password holds up better under CHAP than under MS-CHAPv2, whose
		// strength does not depend on the password at all. PAP, the
		// password as it is, is never accepted.
		b.WriteString("require-chap\n")
		b.WriteString("refuse-pap\nrefuse-mschap\nrefuse-eap\n")
	} else {
		b.WriteString("refuse-pap\nrefuse-chap\nrefuse-mschap\nrefuse-eap\n")
	}
	// No CCP, so no MPPE either. pppd cannot offer MPPE to the devices that
	// ask and leave it out for those that do not: it is required of every
	// session or refused, and requiring it would shut out the routers whose
	// PPP has only CHAP. A device set to require MPPE is told to turn it off.
	b.WriteString("noccp\n")
	b.WriteString("nodefaultroute\n")
	b.WriteString("noipdefault\n")
	b.WriteString("lock\n")
	fmt.Fprintf(&b, "mtu %d\nmru %d\n", f.MTU, f.MTU)
	for _, d := range f.DNS {
		fmt.Fprintf(&b, "ms-dns %s\n", d)
	}
	// A device that drops off without saying so is noticed within two
	// minutes, and its session -- and its address -- freed.
	b.WriteString("lcp-echo-interval 30\nlcp-echo-failure 4\n")
	b.WriteString("connect-delay 5000\n")
	return b.String()
}

// PPTPD renders pptpd's configuration.
//
// pptpd hands out no addresses: "delegate" leaves that to pppd, which takes
// each device's from its secrets line -- the address its usage is counted by,
// as on L2TP.
func PPTPD(s *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "option %s\n", RunFile("pptp-options"))
	b.WriteString("delegate\n")
	// pptpd's own limit on sessions; the panel's is the accounts.
	b.WriteString("connections 4096\n")
	return b.String()
}

// PPTPOptions renders the options the PPTP tunnel's pppd runs each session
// with: MS-CHAPv2 and 128-bit MPPE, required, and nothing else. PPTP has no
// other protection -- that, and that both have been broken for years, is why
// the panel says so wherever it offers PPTP -- but nothing weaker than what
// it has is accepted.
func PPTPOptions(s *State) string {
	f := s.Interface
	var b strings.Builder
	fmt.Fprintf(&b, "name %s\n", ServerNamePPTP)
	fmt.Fprintf(&b, "%s:\n", f.Gateway)
	b.WriteString("auth\n")
	b.WriteString("require-mschap-v2\n")
	b.WriteString("refuse-pap\nrefuse-chap\nrefuse-mschap\nrefuse-eap\n")
	b.WriteString("require-mppe-128\n")
	b.WriteString("nobsdcomp\nnodeflate\n")
	b.WriteString("nodefaultroute\n")
	b.WriteString("noipdefault\n")
	b.WriteString("lock\n")
	fmt.Fprintf(&b, "mtu %d\nmru %d\n", f.MTU, f.MTU)
	for _, d := range f.DNS {
		fmt.Fprintf(&b, "ms-dns %s\n", d)
	}
	b.WriteString("lcp-echo-interval 30\nlcp-echo-failure 4\n")
	return b.String()
}

// Swanctl renders the IPsec half for strongSwan: IKEv1 with the tunnel's
// pre-shared key, transport mode, and only L2TP's own port carried.
//
// The proposals are wide on purpose, strongest first. Each built-in client
// offers its own fixed set, and one the server does not accept is a device
// that cannot connect, with nothing on its screen to say why:
//
//   - Windows, unchanged, offers 3DES, SHA-1 and the 1024-bit group only
//     (Microsoft's own table); AES-256 and the 2048-bit group come with a
//     registry value nobody sets.
//   - iOS and macOS lead with AES-256, SHA-256 and the 2048-bit group.
//   - MikroTik's L2TP client with IPsec uses its default profile: SHA-1,
//     AES-128 or 3DES, the 1024-bit group; Android 11 and earlier the same
//     weak group.
//   - Windows told to prefer the stronger set, and other clients, may offer
//     the elliptic-curve groups.
//
// The 1024-bit group and 3DES stay because the most common client in the
// world cannot connect without them; the rest are only ever chosen when a
// device offers nothing better.
var ikeProposals = []string{
	"aes256-sha256-modp2048", "aes256-sha256-ecp384", "aes256-sha256-ecp256",
	"aes256-sha384-modp2048", "aes128-sha256-modp2048", "aes128-sha256-ecp256",
	"aes256-sha1-modp2048", "aes128-sha1-modp2048", "aes256-sha1-modp1536",
	"aes256-sha1-modp1024", "aes128-sha1-modp1024", "3des-sha1-modp1024",
}

// espProposals are the same, for the data: AES-GCM where a device offers it,
// then AES with SHA-2, then the SHA-1 and 3DES that Windows, MikroTik and
// older Android use.
//
// Each again with a key exchange of its own, for the devices that ask for
// perfect forward secrecy: a proposal with no group matches only a device
// that asks for none, and MikroTik's default profile asks for group 2, D-Link
// and Omada for whatever their PFS switch says. Those are the same sets with
// the groups the IKE proposals above already accept.
var espProposals = []string{
	"aes256gcm16", "aes128gcm16",
	"aes256-sha256", "aes128-sha256", "aes256-sha512",
	"aes256-sha1", "aes128-sha1", "3des-sha1",
	"aes256gcm16-modp2048", "aes128gcm16-modp2048", "aes256gcm16-ecp256",
	"aes256-sha256-modp2048", "aes128-sha256-modp2048", "aes256-sha256-ecp256",
	"aes256-sha1-modp2048", "aes128-sha1-modp2048",
	"aes256-sha1-modp1536", "aes128-sha1-modp1536",
	"aes256-sha1-modp1024", "aes128-sha1-modp1024", "3des-sha1-modp1024",
}

func Swanctl(s *State) string {
	var b strings.Builder
	b.WriteString("connections {\n")
	fmt.Fprintf(&b, "  %s {\n", ServerName)
	b.WriteString("    version = 1\n")
	fmt.Fprintf(&b, "    proposals = %s\n", strings.Join(ikeProposals, ","))
	b.WriteString("    dpd_delay = 30s\n")
	b.WriteString("    local {\n      auth = psk\n    }\n")
	b.WriteString("    remote {\n      auth = psk\n    }\n")
	b.WriteString("    children {\n")
	fmt.Fprintf(&b, "      %s {\n", ServerName)
	b.WriteString("        mode = transport\n")
	// UDP by its number: the name needs /etc/protocols, which a minimal
	// system can be without, and swanctl then refuses the whole connection.
	fmt.Fprintf(&b, "        local_ts = dynamic[17/%d]\n", PortL2TP)
	b.WriteString("        remote_ts = dynamic[17]\n")
	fmt.Fprintf(&b, "        esp_proposals = %s\n", strings.Join(espProposals, ","))
	b.WriteString("        dpd_action = clear\n")
	b.WriteString("      }\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("secrets {\n")
	fmt.Fprintf(&b, "  ike-%s {\n", ServerName)
	fmt.Fprintf(&b, "    secret = \"%s\"\n", s.Interface.PSK)
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// GuardTable is the nftables table the agent owns.
const GuardTable = "wui_l2tp"

// Guard renders the agent's firewall program: L2TP's port answers only what
// came out of IPsec. Plain L2TP carries the password exchange with nothing
// around it, and a server that accepted it would let anyone who never had the
// pre-shared key try customers' passwords -- so the port is opened to it only
// on a tunnel that says it serves routers without IPsec, and then the table
// is taken away.
func Guard(s *State) string {
	if s.Interface.AllowPlain {
		return GuardRemoval()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "add table inet %s\n", GuardTable)
	fmt.Fprintf(&b, "delete table inet %s\n", GuardTable)
	fmt.Fprintf(&b, "table inet %s {\n", GuardTable)
	b.WriteString("\tchain input {\n")
	b.WriteString("\t\ttype filter hook input priority filter - 1; policy accept;\n")
	fmt.Fprintf(&b, "\t\tudp dport %d meta ipsec missing drop\n", PortL2TP)
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.String()
}

// GuardRemoval takes the table away.
func GuardRemoval() string {
	return fmt.Sprintf("add table inet %s\ndelete table inet %s\n", GuardTable, GuardTable)
}
