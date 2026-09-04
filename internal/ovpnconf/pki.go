package ovpnconf

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The PKI is generated here rather than by shelling out to easy-rsa.
//
// easy-rsa is a set of shell scripts around openssl that keeps mutable state in
// a directory: an index, a serial counter, and a `vars` file. That state has to
// survive reinstalls and stay consistent with the database, and every distro
// ships a slightly different version with different defaults. Generating the
// certificates in-process removes the tool, the directory and the drift: the
// only place any of this is stored is the interface row, so a database backup is
// a complete backup.

const (
	// Ten years. These certificates identify one server to its own customers,
	// nothing more; an expiry that lands mid-contract would take every customer
	// offline at once for no security benefit.
	pkiValidity = 10 * 365 * 24 * time.Hour

	// 2048-bit RSA rather than an elliptic curve. Elliptic curves would be
	// smaller and faster, but this key has to be accepted by whatever OpenVPN
	// client a customer happens to have installed, including old Android and
	// router builds. RSA-2048 is the one every version accepts.
	pkiKeyBits = 2048

	// An OpenVPN static key is 256 bytes rendered as hex, which is what
	// `openvpn --genkey` produces and what every client expects to parse.
	staticKeyBytes = 256
	staticKeyCols  = 32
)

// NewPKI generates a certificate authority, a server certificate and a
// tls-crypt key for one interface.
//
// Each interface gets its own authority. Two interfaces on the same machine are
// then cryptographically unrelated, so a key leaked from one cannot be used to
// impersonate the other.
func NewPKI(commonName string) (model.OpenVPNParams, error) {
	var out model.OpenVPNParams

	if strings.TrimSpace(commonName) == "" {
		return out, fmt.Errorf("ovpnconf: PKI needs a common name")
	}

	caKey, err := rsa.GenerateKey(rand.Reader, pkiKeyBits)
	if err != nil {
		return out, fmt.Errorf("ovpnconf: generate CA key: %w", err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, pkiKeyBits)
	if err != nil {
		return out, fmt.Errorf("ovpnconf: generate server key: %w", err)
	}

	now := time.Now().UTC()
	caSerial, err := serialNumber()
	if err != nil {
		return out, err
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: commonName + " CA"},
		NotBefore:             now.Add(-time.Hour), // tolerate a skewed clock
		NotAfter:              now.Add(pkiValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true, // signs leaves only, never another CA
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return out, fmt.Errorf("ovpnconf: sign CA: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return out, fmt.Errorf("ovpnconf: parse CA: %w", err)
	}

	serverSerial, err := serialNumber()
	if err != nil {
		return out, err
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(pkiValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		// Clients are configured with `remote-cert-tls server`, which checks
		// for exactly this. Without it a customer's client would accept any
		// certificate this authority signed, including another customer's, and
		// a customer could impersonate the server to other customers.
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return out, fmt.Errorf("ovpnconf: sign server certificate: %w", err)
	}

	tlsCrypt, err := NewStaticKey()
	if err != nil {
		return out, err
	}

	out = model.OpenVPNParams{
		Transport:   "udp",
		CipherSuite: "AES-256-GCM",
		Auth:        "SHA256",
		CACert:      encodePEM("CERTIFICATE", caDER),
		ServerCert:  encodePEM("CERTIFICATE", serverDER),
		ServerKey:   encodePEM("PRIVATE KEY", mustPKCS8(serverKey)),
		TLSCryptKey: tlsCrypt,
		DuplicateCN: false,
	}
	return out, nil
}

// NewStaticKey generates an OpenVPN static key in the format the tls-crypt
// directive expects.
//
// tls-crypt wraps the whole TLS handshake in a symmetric layer keyed by this
// value. It does not hide that the port is speaking OpenVPN -- the opcode byte
// in the first packet is in the clear either way, which was measured rather
// than assumed. What it hides is everything after that: without it a TLS
// ClientHello and the server's own certificate are visible in the client's
// stream, which is a far better fingerprint than the opcode. It also stops
// anyone at all making the server do TLS work. With
// it, a packet that does not authenticate is dropped before any state is
// allocated, which is both the anti-probing and the anti-DoS property.
func NewStaticKey() (string, error) {
	raw := make([]byte, staticKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("ovpnconf: generate static key: %w", err)
	}

	var b strings.Builder
	b.WriteString("-----BEGIN OpenVPN Static key V1-----\n")
	encoded := hex.EncodeToString(raw)
	for i := 0; i < len(encoded); i += staticKeyCols {
		b.WriteString(encoded[i : i+staticKeyCols])
		b.WriteByte('\n')
	}
	b.WriteString("-----END OpenVPN Static key V1-----")
	return b.String(), nil
}

// serialNumber draws a random 128-bit certificate serial.
func serialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("ovpnconf: serial number: %w", err)
	}
	return n, nil
}

func encodePEM(blockType string, der []byte) string {
	return strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
		Type:  blockType,
		Bytes: der,
	})))
}

// mustPKCS8 marshals a private key. The only documented failure is an
// unsupported key type, and the caller has just generated an RSA key.
func mustPKCS8(key *rsa.PrivateKey) []byte {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(fmt.Sprintf("ovpnconf: marshalling a freshly generated RSA key failed: %v", err))
	}
	return der
}

// SecretAlphabet is the character set customer passwords are drawn from.
//
// It is deliberately narrower than base64: the authentication script rejects
// anything outside it, so a generator using a wider alphabet would issue
// credentials that are refused at login with no message saying why. Generating
// and validating against one constant is what keeps the two ends in agreement.
const SecretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// NewSecret draws a password of n characters from SecretAlphabet.
//
// Selection is rejection-sampled rather than reduced modulo the alphabet size,
// which would make the first few characters measurably more likely than the
// rest.
func NewSecret(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("ovpnconf: secret length must be positive")
	}

	const size = len(SecretAlphabet)
	limit := byte(256 - (256 % size))

	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("ovpnconf: generate secret: %w", err)
		}
		for _, b := range buf {
			if b >= limit {
				continue // would skew the distribution
			}
			out = append(out, SecretAlphabet[int(b)%size])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}
