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
