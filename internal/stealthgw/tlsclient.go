package stealthgw

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"slices"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The TLS a device speaks to a stream transport.
//
// A server's certificate is held to one of two checks, and which one is the
// profile's to say, never the server's:
//
//   - Pinned alone, for wui-tls's own certificate: the key of the
//     certificate the server shows must be one of the pins, and TLS 1.3
//     must be spoken. Its chain, its name and its dates are not looked at --
//     nobody issued it, and its name is drawn. TLS 1.3 has the server sign
//     the handshake with that key, and the signature is checked all the
//     same, so a server that shows the certificate without holding its key
//     fails.
//   - Issued: the chain leads to a root the device trusts, for the name
//     asked for, at the device's own time. With pins as well, the key must
//     be one of them too and TLS 1.3 spoken: a pin is a second condition
//     there, never a way around the first.
//
// Either way TLS 1.2, where it is taken, ends on a key agreement and an
// AEAD, and nothing that fails is tried again with less.

// ErrPin is a server whose certificate is not of a pinned key.
var ErrPin = errors.New("stealthgw: the server's certificate is not pinned")

// ClientTLS is the TLS for a server named name: a host name, which is sent,
// or an address, which is not. selfSigned trusts pins alone; roots are the
// trusted roots otherwise, the system's when nil.
func ClientTLS(name string, pins []stealth.Pin, selfSigned bool, roots *x509.CertPool) (*tls.Config, error) {
	if selfSigned && len(pins) == 0 {
		return nil, errors.New("stealthgw: a certificate trusted by its pins alone needs a pin")
	}
	pins = slices.Clone(pins)
	cfg := &tls.Config{
		ServerName:   name,
		RootCAs:      roots,
		MinVersion:   tls.VersionTLS12,
		CipherSuites: tls12Suites,
		NextProtos:   []string{"http/1.1"},
	}
	if len(pins) > 0 {
		cfg.MinVersion = tls.VersionTLS13
	}
	cfg.InsecureSkipVerify = selfSigned
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(pins) == 0 {
			return nil // the issued chain was checked by TLS itself
		}
		if cs.Version != tls.VersionTLS13 || len(cs.PeerCertificates) == 0 {
			return ErrPin
		}
		if !slices.Contains(pins, stealth.PinOf(cs.PeerCertificates[0])) {
			return ErrPin
		}
		return nil
	}
	return cfg, nil
}
