package stealth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"strings"
	"time"
)

// The certificate of anchor.
//
// A anchor server ends TLS with a certificate of its own, which nobody
// issued: the panel draws an ECDSA P-256 key once, keeps it, and signs a
// certificate with it. A device trusts it by its pin, the SHA-256 of the
// key's SubjectPublicKeyInfo, which every profile carries, and by nothing
// else: no chain, no name, no dates. So the key is drawn once and never
// again -- a new key is a new pin, and every profile would need fetching --
// while the certificate is made afresh with the same key before it runs
// out, which no device notices.
//
// Nothing in the certificate says W-UI. Its name is the operator's own host
// name when they give one, or one drawn once per tunnel in a shape a private
// network's hosts have, and kept across renewals. Its serial is random, its
// lifetime drawn from a range, and its start a random time in the past week,
// so no two servers' certificates line up.

// Certificate lifetimes.
const (
	// certLifeMin and certLifeSpread: a certificate lives 80 days and up to
	// 20 more.
	certLifeMin    = 80 * 24 * time.Hour
	certLifeSpread = 21
	// certBackdate is how far into the past its start is drawn.
	certBackdate = 7 * 24 * time.Hour
	// CertificateLead is how long before its end a certificate is made
	// afresh, so it never runs out while a server runs.
	CertificateLead = 20 * 24 * time.Hour
)

// PinLen is the length of a pin.
const PinLen = sha256.Size

// Pin is the SHA-256 of a key's SubjectPublicKeyInfo.
type Pin [PinLen]byte

// String is the pin in standard base64, as a profile carries it.
func (p Pin) String() string { return base64.StdEncoding.EncodeToString(p[:]) }

// ParsePin reads a pin written by String.
func ParsePin(v string) (Pin, error) {
	var p Pin
	if err := parse32(v, p[:]); err != nil {
		return Pin{}, fmt.Errorf("stealth: pin: %w", err)
	}
	return p, nil
}

// PinOf is the pin of a certificate's key.
func PinOf(c *x509.Certificate) Pin { return sha256.Sum256(c.RawSubjectPublicKeyInfo) }

// NewCertificateKey draws a certificate key: ECDSA P-256, PKCS #8, PEM as
// the database keeps it.
func NewCertificateKey() (string, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("stealth: draw a certificate key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", fmt.Errorf("stealth: certificate key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func parseCertificateKey(v string) (*ecdsa.PrivateKey, error) {
	b, _ := pem.Decode([]byte(v))
	if b == nil || b.Type != "PRIVATE KEY" {
		return nil, errors.New("stealth: the certificate key is not a PEM private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, errors.New("stealth: the certificate key does not read")
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, errors.New("stealth: the certificate key is not ECDSA on P-256")
	}
	return ec, nil
}

// CertificateKeyPin is the pin of a certificate key.
func CertificateKeyPin(key string) (Pin, error) {
	k, err := parseCertificateKey(key)
	if err != nil {
		return Pin{}, err
	}
	return keyPin(k)
}

func keyPin(k *ecdsa.PrivateKey) (Pin, error) {
	spki, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return Pin{}, fmt.Errorf("stealth: certificate key: %w", err)
	}
	return sha256.Sum256(spki), nil
}

// CertificateInfo is what a certificate made by MakeCertificate says.
type CertificateInfo struct {
	Name      string
	NotBefore time.Time
	NotAfter  time.Time
	Pin       Pin
}

// ReadCertificate reads the first certificate of a PEM chain as the
// database keeps it: the one a server shows first.
func ReadCertificate(cert string) (*x509.Certificate, CertificateInfo, error) {
	b, _ := pem.Decode([]byte(cert))
	if b == nil || b.Type != "CERTIFICATE" {
		return nil, CertificateInfo{}, errors.New("stealth: the certificate is not PEM")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return nil, CertificateInfo{}, errors.New("stealth: the certificate does not read")
	}
	return c, CertificateInfo{Name: c.Subject.CommonName, NotBefore: c.NotBefore, NotAfter: c.NotAfter, Pin: PinOf(c)}, nil
}

// CertificateDue says whether cert is to be made afresh with key: when it is
// missing, does not read, is of another key, names another name than name
// asks for, is within CertificateLead of its end, or starts more than a day
// from now -- a clock that was set back would otherwise keep a certificate
// that is not valid yet. An empty name asks for none in particular.
func CertificateDue(cert, key, name string, now time.Time) bool {
	if cert == "" {
		return true
	}
	pin, err := CertificateKeyPin(key)
	if err != nil {
		return true
	}
	_, info, err := ReadCertificate(cert)
	if err != nil || info.Pin != pin || (name != "" && info.Name != name) {
		return true
	}
	return !now.Before(info.NotAfter.Add(-CertificateLead)) || info.NotBefore.After(now.Add(24*time.Hour))
}

// MakeCertificate signs a certificate with key for name, or for the name
// before carries when name is empty, or for one drawn when neither has one.
// It is PEM.
func MakeCertificate(key, name, before string, now time.Time) (string, error) {
	k, err := parseCertificateKey(key)
	if err != nil {
		return "", err
	}
	if name == "" && before != "" {
		if _, info, err := ReadCertificate(before); err == nil && ValidCertificateName(info.Name) {
			name = info.Name
		}
	}
	if name == "" {
		if name, err = drawHostName(); err != nil {
			return "", err
		}
	}
	if !ValidCertificateName(name) {
		return "", fmt.Errorf("stealth: %q is no host name for a certificate", name)
	}
	var drawn [16]byte
	if _, err := rand.Read(drawn[:]); err != nil {
		return "", fmt.Errorf("stealth: read entropy: %w", err)
	}
	serial := make([]byte, 16)
	if _, err := rand.Read(serial); err != nil {
		return "", fmt.Errorf("stealth: read entropy: %w", err)
	}
	serial[0] &= 0x7f // a serial is positive
	serial[0] |= 0x40 // and all of its 16 bytes count
	start := now.Truncate(time.Second).Add(-time.Duration(binary.BigEndian.Uint32(drawn[:4])%uint32(certBackdate/time.Second)) * time.Second)
	end := start.Add(certLifeMin + time.Duration(drawn[4]%certLifeSpread)*24*time.Hour)
	// The key identifier as RFC 5280 4.2.1.2 has it, which certificates a
	// server is usually given carry: SHA-1 of the key's bits.
	point, err := k.PublicKey.Bytes()
	if err != nil {
		return "", fmt.Errorf("stealth: certificate key: %w", err)
	}
	ski := sha1.Sum(point)
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(serial),
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		NotBefore:             start,
		NotAfter:              end,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		SubjectKeyId:          ski[:],
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return "", fmt.Errorf("stealth: sign a certificate: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

// TLSCertificate is a PEM chain and its key as a server serves them: the
// panel's own certificate, or one MakeCertificate made.
func TLSCertificate(cert, key string) (*tls.Certificate, error) {
	c, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil {
		return nil, fmt.Errorf("stealth: the certificate and its key: %w", err)
	}
	if c.Leaf == nil {
		if c.Leaf, err = x509.ParseCertificate(c.Certificate[0]); err != nil {
			return nil, fmt.Errorf("stealth: the certificate: %w", err)
		}
	}
	return &c, nil
}

// ValidCertificateName says whether name is a host name a certificate can
// carry: lower-case labels of letters, digits and hyphens, not an address.
func ValidCertificateName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
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

// privateSuffixes are the endings a private network's hosts are given.
var privateSuffixes = [...]string{"lan", "local", "internal", "home.arpa", "localdomain", "intranet", "corp", "lab", "office", "private"}

// drawHostName draws a name a private network's host could have. The shapes
// differ -- a word, a word and a number, two words -- so no one pattern
// matches every server.
func drawHostName() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("stealth: read entropy: %w", err)
	}
	const consonants, vowels = "bcdfghjklmnprstvz", "aeiou"
	word := func(at, n int) string {
		w := make([]byte, n)
		for i := range w {
			if i%2 == 0 {
				w[i] = consonants[int(b[at+i])%len(consonants)]
			} else {
				w[i] = vowels[int(b[at+i])%len(vowels)]
			}
		}
		return string(w)
	}
	first := word(4, 4+int(b[1]%4))
	var host string
	switch b[0] % 3 {
	case 0:
		host = first
	case 1:
		host = fmt.Sprintf("%s%d", first, 1+int(b[2])%99)
	default:
		host = first + "-" + word(12, 3+int(b[3]%3))
	}
	return host + "." + privateSuffixes[int(b[20])%len(privateSuffixes)], nil
}
