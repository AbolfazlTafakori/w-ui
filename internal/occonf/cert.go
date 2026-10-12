package occonf

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"time"
)

// A tunnel's own certificate: RSA 2048, which every Cisco and OpenConnect
// client takes, for 825 days -- what Apple's clients accept of a server's --
// and made again 60 days before it ends.
const (
	certLifetime = 825 * 24 * time.Hour
	RenewBefore  = 60 * 24 * time.Hour
)

// NewServerCert makes a server certificate of the tunnel's own for the
// names and addresses devices dial; the first is its common name.
func NewServerCert(names []string) (cert, key string, err error) {
	if len(names) == 0 || strings.TrimSpace(names[0]) == "" {
		return "", "", errors.New("occonf: a server certificate needs a name")
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"W-UI"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(strings.Trim(n, "[]")); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return "", "", err
	}
	cert = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	key = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	return cert, key, nil
}

func leaf(certPEM string) (*x509.Certificate, error) {
	b, _ := pem.Decode([]byte(certPEM))
	if b == nil {
		return nil, errors.New("occonf: no certificate")
	}
	return x509.ParseCertificate(b.Bytes)
}

// NeedsRenewal reports whether a certificate of the tunnel's own is
// missing, near its end, or not for this name.
func NeedsRenewal(certPEM, name string, now time.Time) bool {
	c, err := leaf(certPEM)
	if err != nil || now.Add(RenewBefore).After(c.NotAfter) {
		return true
	}
	return c.VerifyHostname(strings.Trim(name, "[]")) != nil
}

// Covers reports whether a certificate is for a name and still in date: what
// the panel's own has to be to go on the tunnel.
func Covers(certPEM, name string, now time.Time) bool {
	c, err := leaf(certPEM)
	if err != nil || now.After(c.NotAfter) || now.Before(c.NotBefore) {
		return false
	}
	return c.VerifyHostname(strings.Trim(name, "[]")) == nil
}

// Pin is the certificate's public key as OpenConnect pins a server:
// "pin-sha256:" and the SHA-256 of its SubjectPublicKeyInfo in base64. A
// client given it trusts that server and no other, whoever signed it.
func Pin(certPEM string) string {
	c, err := leaf(certPEM)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return "pin-sha256:" + base64.StdEncoding.EncodeToString(sum[:])
}

// SelfSigned reports whether a certificate signed itself: one no device
// trusts by its own authorities.
func SelfSigned(certPEM string) bool {
	c, err := leaf(certPEM)
	if err != nil {
		return false
	}
	// CheckSignatureFrom would ask the issuer to be an authority, which a
	// server's own certificate is not: the signature is checked against its
	// own key directly.
	return string(c.RawIssuer) == string(c.RawSubject) &&
		c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}
