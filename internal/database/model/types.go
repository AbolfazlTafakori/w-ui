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

	// AmneziaWG 3.0+. HeaderProtectionKey requires S1-S4 >= 12 because the
	// ChaCha20 nonce is drawn from the first 12 bytes of the S padding.
	HeaderProtectionKey string `json:"headerProtectionKey,omitempty"`

	I1 string `json:"i1,omitempty"`
	I2 string `json:"i2,omitempty"`
	I3 string `json:"i3,omitempty"`
	I4 string `json:"i4,omitempty"`
	I5 string `json:"i5,omitempty"`
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
