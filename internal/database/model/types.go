package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSONField stores a Go value as a JSON text column. It is used for grouped
// settings that are always read and written as a unit, so they do not earn
// their own columns.
type JSONField[T any] struct {
	V T
}

// JSON wraps a value for storage.
func JSON[T any](v T) JSONField[T] { return JSONField[T]{V: v} }

// Value implements driver.Valuer.
func (j JSONField[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, fmt.Errorf("encode json column: %w", err)
	}
	return string(b), nil
}

// Scan implements sql.Scanner.
func (j *JSONField[T]) Scan(src any) error {
	var zero T
	if src == nil {
		j.V = zero
		return nil
	}

	var raw []byte
	switch s := src.(type) {
	case []byte:
		raw = s
	case string:
		raw = []byte(s)
	default:
		return fmt.Errorf("decode json column: unsupported source type %T", src)
	}

	if len(raw) == 0 {
		j.V = zero
		return nil
	}
	if err := json.Unmarshal(raw, &j.V); err != nil {
		return fmt.Errorf("decode json column: %w", err)
	}
	return nil
}

// GormDataType tells GORM which column type to create.
func (JSONField[T]) GormDataType() string { return "text" }

// MarshalJSON keeps the wrapper invisible in API responses.
func (j JSONField[T]) MarshalJSON() ([]byte, error) { return json.Marshal(j.V) }

// UnmarshalJSON keeps the wrapper invisible in API requests.
func (j *JSONField[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.V) }

// AWGParams holds the AmneziaWG obfuscation profile.
//
// These belong to the interface, never to an individual account. S1-S4 and
// HeaderProtectionKey must be byte-identical on the server and every client, so
// storing them per-account would mean the first edit silently disconnects every
// customer at once.
//
// Jc/Jmin/Jmax and I1-I5 are chosen by whoever sends and ignored by the
// receiver, so they may differ per client; they live here anyway because the
// panel generates them once per interface.
type AWGParams struct {
	Jc   int `json:"jc"`   // junk packet count, 3-6
	Jmin int `json:"jmin"` // min junk size, 40-89
	Jmax int `json:"jmax"` // max junk size, Jmin+50..250

	S1 int `json:"s1"` // init message padding      — must match
	S2 int `json:"s2"` // response message padding  — must match
	S3 int `json:"s3"` // cookie message padding    — must match
	S4 int `json:"s4"` // data message padding      — must match

	H1 uint32 `json:"h1"` // init message identifier
	H2 uint32 `json:"h2"` // response identifier
	H3 uint32 `json:"h3"` // cookie identifier
	H4 uint32 `json:"h4"` // data identifier

	// H1Max-H4Max make each identifier a range (AmneziaWG 2.0): every packet
	// carries a value drawn from H..HMax rather than one fixed number, so no
	// single value marks the protocol. Zero, or no more than H, is the single
	// value H -- what every profile before ranges was, and what a panel that
	// predates them reads.
	H1Max uint32 `json:"h1Max,omitempty"`
	H2Max uint32 `json:"h2Max,omitempty"`
	H3Max uint32 `json:"h3Max,omitempty"`
	H4Max uint32 `json:"h4Max,omitempty"`

	// AmneziaWG 3.0+. HeaderProtectionKey requires S1-S4 >= 12 because the
	// ChaCha20 nonce is drawn from the first 12 bytes of the S padding.
	HeaderProtectionKey string `json:"headerProtectionKey,omitempty"`

	// I1-I5 are packets sent before each handshake to look like another
	// protocol (AmneziaWG 1.5): a chain of tags, <b 0x..> bytes, <r N>
	// random bytes, <rc N> random letters, <rd N> random digits, <t> the
	// time. Only the sender uses them.
	I1 string `json:"i1,omitempty"`
	I2 string `json:"i2,omitempty"`
	I3 string `json:"i3,omitempty"`
	I4 string `json:"i4,omitempty"`
	I5 string `json:"i5,omitempty"`
}

// HeaderRange is identifier n (1-4) as a range: lo-hi, or lo alone.
func (p AWGParams) HeaderRange(n int) (lo, hi uint32) {
	switch n {
	case 1:
		lo, hi = p.H1, p.H1Max
	case 2:
		lo, hi = p.H2, p.H2Max
	case 3:
		lo, hi = p.H3, p.H3Max
	case 4:
		lo, hi = p.H4, p.H4Max
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi
}

// Specials are I1-I5 in order.
func (p AWGParams) Specials() [5]string { return [5]string{p.I1, p.I2, p.I3, p.I4, p.I5} }

// UsesAWG2 reports whether the profile needs AmneziaWG 1.5 or later on both
// ends: identifier ranges, or packets before the handshake.
func (p AWGParams) UsesAWG2() bool {
	for n := 1; n <= 4; n++ {
		if lo, hi := p.HeaderRange(n); hi > lo {
			return true
		}
	}
	for _, s := range p.Specials() {
		if s != "" {
			return true
		}
	}
	return false
}

// L2TPParams holds interface-scoped L2TP/IPsec settings.
//
// PSK is the IPsec pre-shared key every device on the tunnel types in beside
// its own username and password. It is the tunnel's, not a customer's: the
// clients built into Windows, macOS and iOS have no other way to start the
// IPsec half, so it is handed to everyone and shown on the panel, and taking
// a customer away is removing their credentials, not changing it.
type L2TPParams struct {
	PSK string `json:"psk"`
	// AllowPlain also accepts L2TP without IPsec, for routers whose L2TP
	// client has none. Off unless an operator turns it on: those sessions are
	// not encrypted.
	AllowPlain bool `json:"allowPlain"`
}

// IKEv2Params holds interface-scoped IKEv2/IPsec settings.
//
// A tunnel can let a device sign in four ways, each one a type in the VPN
// settings Android, iOS, macOS and Windows have built in; which ones it
// allows is the operator's choice. Every device has one identity for all of
// them -- its username -- and one address.
//
// The tunnel is its own certificate authority: it signs the server's
// certificate, which every way but PSK shows the device, and each device's
// own, for the two that sign in by certificate. The keys are stored here,
// like OpenVPN's, and taken out on the way out of the API (redactKeys).
type IKEv2Params struct {
	// MSCHAPv2 is a username and password (EAP-MSCHAPv2), PSK a key of the
	// device's own, RSA a certificate, EAPTLS a certificate inside EAP.
	MSCHAPv2 bool `json:"mschapv2"`
	PSK      bool `json:"psk"`
	RSA      bool `json:"rsa"`
	EAPTLS   bool `json:"eapTls"`

	CACert     string `json:"caCert,omitempty"`
	CAKey      string `json:"caKey,omitempty"`
	ServerCert string `json:"serverCert,omitempty"`
	ServerKey  string `json:"serverKey,omitempty"`
	// ServerNames are the names and addresses the server's certificate is
	// for: what devices dial. A device checking it (iOS, Windows) refuses a
	// certificate that does not name what it dialled.
	ServerNames []string `json:"serverNames,omitempty"`
}

// AnyMode reports whether the tunnel lets a device in at all.
func (p IKEv2Params) AnyMode() bool { return p.MSCHAPv2 || p.PSK || p.RSA || p.EAPTLS }

// ByCertificate reports whether a device may sign in with a certificate of
// its own, which then has to be issued to it.
func (p IKEv2Params) ByCertificate() bool { return p.RSA || p.EAPTLS }

// OpenConnectParams holds interface-scoped OpenConnect settings.
//
// The server's certificate is the tunnel's own, made by the panel, unless
// the panel has one of its own for the name devices dial (UsePanelCert):
// the Cisco client refuses a certificate no authority it knows has signed
// unless its "Block untrusted servers" switch is turned off, so a real one is
// the one that needs nothing from the customer. Key is taken out on the way
// out of the API (redactKeys).
type OpenConnectParams struct {
	// DTLS carries a signed-in device's traffic over UDP on the same port,
	// which is faster; off, everything goes over TLS on TCP.
	DTLS bool `json:"dtls"`
	// Camouflage answers like an ordinary web server unless the address has
	// "?CamouflageSecret" on it (ocserv 1.2 and later).
	Camouflage       bool   `json:"camouflage"`
	CamouflageSecret string `json:"camouflageSecret,omitempty"`
	// UsePanelCert puts the panel's own certificate on the tunnel when it
	// has one for the tunnel's address.
	UsePanelCert bool   `json:"usePanelCert"`
	Cert         string `json:"cert,omitempty"`
	Key          string `json:"key,omitempty"`
	// CertSource says whose certificate is on the tunnel now: "own" or
	// "panel".
	CertSource string `json:"certSource,omitempty"`
}

// StealthParams is a WireGuard tunnel's transport, when it is not plain UDP
// alone.
//
// Plain WireGuard stays on the tunnel's own port whatever is chosen, so the
// files stock apps hold keep working. A stealth transport is a second way to
// the same tunnel: a gateway on a port of its own that wraps every packet so
// that nothing of WireGuard shows (package stealthgw), for W-UI's own app.
// The zero value is plain UDP.
type StealthParams struct {
	// Transport is the stealth transport, by its name: TransportWuiUDP,
	// TransportWuiTLS, TransportWuiWS, TransportWuiPoll or TransportWuiPlain.
	Transport string `json:"transport,omitempty"`
	// Port is where it listens.
	Port int `json:"port,omitempty"`

	// The rest is wui-ws's, wui-poll's and wui-plain's: WebSocket, or on
	// wui-poll POST requests, behind the operator's own web server or a CDN,
	// which hands the gateway plain HTTP on Port. In front of wui-ws and
	// wui-poll it ends TLS; wui-plain has none.

	// Host is the front's name: what devices connect to, and on wui-ws and
	// wui-poll what TLS names.
	Host string `json:"host,omitempty"`
	// FrontPort is the front's port; 443 when zero, or 80 on wui-plain.
	FrontPort int `json:"frontPort,omitempty"`
	// Path is the transport's path on the front.
	Path string `json:"path,omitempty"`
	// Origin is the decoy, the operator's own site, where every request that
	// is not a device's goes as it came; empty, such a request is answered
	// 404. wui-plain must have one: a stranger's answer crosses the network
	// in the clear there.
	Origin string `json:"origin,omitempty"`
	// Proxies are the fronts, besides the loopback, trusted to say who a
	// visitor is: addresses and ranges, by commas or spaces. With none the
	// gateway listens on the loopback alone, for a front on the same
	// machine, and its port stays shut in the firewall.
	Proxies string `json:"proxies,omitempty"`
	// ClientHeader is the header a front names the visitor in;
	// X-Forwarded-For when empty.
	ClientHeader string `json:"clientHeader,omitempty"`
	// Cookie is the name of the cookie wui-plain's token travels in, drawn
	// by the panel and kept across its keys.
	Cookie string `json:"cookie,omitempty"`
	// Key and PreviousKey are the transport's private keys, base64: the
	// current one, which profiles name, and the one before, still taken
	// from apps that have not fetched their profile since. KeyAt is when Key
	// was drawn, Unix seconds. The API never shows either key. wui-plain's
	// keys are its own, never a covered transport's.
	Key         string `json:"key,omitempty"`
	PreviousKey string `json:"previousKey,omitempty"`
	KeyAt       int64  `json:"keyAt,omitempty"`

	// The rest is wui-tls's: no front, the gateway is the server on Port
	// and ends TLS itself. Host, Path, Origin and the keys above are its
	// too; Origin must be an http:// address, since the gateway hands a
	// stranger's bytes to it as they came.

	// Cert and CertKey are the certificate the panel makes for the tunnel
	// and its key, PEM. The key is drawn once and kept, since devices trust
	// the certificate by its key's pin alone; the certificate is made again
	// with it before it runs out. The API never shows CertKey.
	Cert    string `json:"cert,omitempty"`
	CertKey string `json:"certKey,omitempty"`
	// UsePanelCert asks for the panel's own issued certificate instead,
	// when it is in date for Host and the tunnel is on the panel's own
	// server. Devices then check it as a browser does, by its chain and
	// name. PanelCert and PanelCertKey are a copy of it, PEM; CertSource
	// says which one is served: "own" or "panel".
	UsePanelCert bool   `json:"usePanelCert,omitempty"`
	PanelCert    string `json:"panelCert,omitempty"`
	PanelCertKey string `json:"panelCertKey,omitempty"`
	CertSource   string `json:"certSource,omitempty"`
	// ProxyProtocol opens every connection to the decoy with a PROXY
	// protocol header, version 2, naming the visitor, for a decoy set to
	// expect one.
	ProxyProtocol bool `json:"proxyProtocol,omitempty"`
}

// CertSources: whose certificate a wui-tls gateway serves.
const (
	CertSourceOwn   = "own"
	CertSourcePanel = "panel"
)

// ServedCert is the certificate and key a wui-tls gateway serves, PEM.
func (p StealthParams) ServedCert() (cert, key string) {
	if p.CertSource == CertSourcePanel {
		return p.PanelCert, p.PanelCertKey
	}
	return p.Cert, p.CertKey
}

// Proven says whether the transport opens with a proof sealed to its own
// key: wui-tls, wui-ws, wui-poll or wui-plain.
func (p StealthParams) Proven() bool {
	return p.Transport == TransportWuiTLS || p.Fronted()
}

// FrontPortOr is the front's port when none is set: 443, or 80 for
// wui-plain, which is plain HTTP.
func (p StealthParams) FrontPortOr() int {
	switch {
	case p.FrontPort != 0:
		return p.FrontPort
	case p.Transport == TransportWuiPlain:
		return 80
	}
	return 443
}

// Fronted says whether the transport is behind the operator's web server:
// wui-ws, wui-poll or wui-plain.
func (p StealthParams) Fronted() bool {
	return p.Transport == TransportWuiWS || p.Transport == TransportWuiPoll || p.Transport == TransportWuiPlain
}

// WireGuard's transports, as a tunnel's form and the API name them. Plain
// UDP is WireGuard's own and needs no gateway; the others are package
// stealth's, by the names it gives them.
const (
	TransportUDP      = "udp"
	TransportWuiUDP   = "wui-udp"
	TransportWuiTLS   = "wui-tls"
	TransportWuiWS    = "wui-ws"
	TransportWuiPoll  = "wui-poll"
	TransportWuiPlain = "wui-plain"
)

// TransportNetwork is the network a WireGuard transport listens on, or ""
// for a name that is none.
func TransportNetwork(name string) string {
	switch name {
	case TransportWuiUDP:
		return "udp"
	case TransportWuiTLS, TransportWuiWS, TransportWuiPoll, TransportWuiPlain:
		return "tcp"
	}
	return ""
}

// OpenVPNParams holds interface-scoped OpenVPN settings.
//
// The panel runs OpenVPN in credential-only mode: verify-client-cert none plus
// an auth-user-pass-verify hook, with tls-crypt to keep unauthenticated probes
// off the port. That means one shared .ovpn profile and per-customer
// credentials, and no per-client PKI or CRL to maintain.
type OpenVPNParams struct {
	Transport   string `json:"transport"`   // udp | tcp
	CipherSuite string `json:"cipherSuite"` // e.g. AES-256-GCM
	Auth        string `json:"auth"`        // e.g. SHA256

	CACert      string `json:"caCert,omitempty"`
	ServerCert  string `json:"serverCert,omitempty"`
	ServerKey   string `json:"serverKey,omitempty"`
	TLSCryptKey string `json:"tlsCryptKey,omitempty"`

	// DuplicateCN off means a second session for the same username evicts the
	// first, which is how the one-device-per-credential guarantee is enforced
	// for free. Turn it on only when the device limit is counted in the panel.
	DuplicateCN bool `json:"duplicateCN"`

	ManagementSocket string `json:"managementSocket,omitempty"`
}

// XUIInbound is an inbound on a 3x-ui node, as that panel reports it.
type XUIInbound struct {
	// RemoteID is the inbound's id on the 3x-ui panel.
	RemoteID int    `json:"remoteId"`
	Tag      string `json:"tag"`
	Remark   string `json:"remark"`
	// Kind is the xray protocol: vless, vmess, trojan, shadowsocks,
	// hysteria, or another the panel can read but not add clients to.
	Kind     string `json:"kind"`
	Network  string `json:"network"`
	Security string `json:"security"`
	// Method is a Shadowsocks inbound's cipher, which decides the shape of
	// a client's key.
	Method string `json:"method,omitempty"`
	// Flow is the VLESS flow a client on it is given (xtls-rprx-vision on
	// a Reality or TLS inbound over TCP), empty otherwise.
	Flow string `json:"flow,omitempty"`
	// Enabled is the switch on the 3x-ui panel; Gone is an inbound removed
	// there that still has customers here.
	Enabled bool `json:"enabled"`
	Gone    bool `json:"gone,omitempty"`
	// Customers says this panel's customers can be put on it: that panel
	// keeps a client per user on it and counts them (service.InboundKinds).
	Customers bool `json:"customers"`
	// Clients and Up/Down are the inbound's own totals there, every client
	// on it counted, ours or not.
	Clients int    `json:"clients"`
	Up      uint64 `json:"up"`
	Down    uint64 `json:"down"`
}
