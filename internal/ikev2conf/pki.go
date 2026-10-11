package ikev2conf

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// Certificates.
//
// Each tunnel is its own authority, made here when the tunnel is, so the
// tunnel's row is the one place its keys exist. RSA rather than elliptic
// curves: the type Android's built-in client is named for, and the one every
// built-in client takes.
//
// The server's certificate carries what Windows and macOS look for before
// they accept a VPN server's -- server authentication and the IKE
// intermediate use -- and every name devices dial. A device's carries its
// username, which is the identity it signs in as and which strongSwan checks
// against the certificate.

const (
	keyBits = 2048
	// Lifetimes. The server's is the longest Apple accepts for a server
	// certificate under a private authority; the panel issues a new one
	// before it runs out (NeedsRenewal).
	caLifetime     = 20 * 365 * 24 * time.Hour
	serverLifetime = 825 * 24 * time.Hour
	deviceLifetime = 10 * 365 * 24 * time.Hour
	// RenewBefore is how long before its end a server certificate is
	// replaced.
	RenewBefore = 60 * 24 * time.Hour
)

// oidIKEIntermediate is id-kp-ipsecIKE's predecessor that Windows and macOS
// still look for on an IKEv2 server's certificate.
var oidIKEIntermediate = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 8, 2, 2}

// Authority is a tunnel's certificate authority, in PEM.
type Authority struct{ Cert, Key string }

// NewAuthority makes a tunnel's authority.
func NewAuthority(tunnel string) (Authority, error) {
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return Authority{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "W-UI " + tunnel + " IKEv2 authority", Organization: []string{"W-UI"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return Authority{}, err
	}
	return Authority{Cert: certPEM(der), Key: keyPEM(key)}, nil
}

// Issued is a certificate the authority signed, and its key, in PEM.
type Issued struct{ Cert, Key string }

// IssueServer makes the server's certificate, for every name and address
// devices dial; the first is its common name.
func (a Authority) IssueServer(names []string) (Issued, error) {
	if len(names) == 0 {
		return Issued{}, errors.New("ikev2conf: a server certificate needs a name")
	}
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: names[0], Organization: []string{"W-UI"}},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// Windows and macOS look for the IKE intermediate use as well.
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{oidIKEIntermediate},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	return a.issue(tmpl, serverLifetime)
}

// IssueDevice makes a device's certificate, for its username.
func (a Authority) IssueDevice(username string) (Issued, error) {
	tmpl := &x509.Certificate{
		Subject: pkix.Name{CommonName: username, Organization: []string{"W-UI"}},
		// Both identities the device signs in as: its username, and the one
		// for EAP-TLS (TLSIdentity).
		DNSNames:    []string{username, TLSIdentity(username)},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return a.issue(tmpl, deviceLifetime)
}

func (a Authority) issue(tmpl *x509.Certificate, life time.Duration) (Issued, error) {
	caCert, caKey, err := a.parse()
	if err != nil {
		return Issued{}, err
	}
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return Issued{}, err
	}
	now := time.Now()
	tmpl.SerialNumber = serial()
	tmpl.NotBefore = now.Add(-time.Hour)
	tmpl.NotAfter = now.Add(life)
	if tmpl.NotAfter.After(caCert.NotAfter) {
		tmpl.NotAfter = caCert.NotAfter
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return Issued{}, err
	}
	return Issued{Cert: certPEM(der), Key: keyPEM(key)}, nil
}

func (a Authority) parse() (*x509.Certificate, *rsa.PrivateKey, error) {
	cb, _ := pem.Decode([]byte(a.Cert))
	kb, _ := pem.Decode([]byte(a.Key))
	if cb == nil || kb == nil {
		return nil, nil, errors.New("ikev2conf: the tunnel's authority is missing")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ikev2conf: the tunnel's authority: %w", err)
	}
	key, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ikev2conf: the tunnel's authority key: %w", err)
	}
	return cert, key, nil
}

// NeedsRenewal reports whether a server certificate is missing, near its
// end, or not for exactly these names: a new endpoint or host means a new
// certificate, or devices that check it refuse the server.
func NeedsRenewal(certPEMText string, names []string, now time.Time) bool {
	b, _ := pem.Decode([]byte(certPEMText))
	if b == nil {
		return true
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil || now.Add(RenewBefore).After(c.NotAfter) {
		return true
	}
	have := map[string]bool{}
	for _, n := range c.DNSNames {
		have[n] = true
	}
	for _, ip := range c.IPAddresses {
		have[ip.String()] = true
	}
	if len(have) != len(names) || len(names) == 0 || c.Subject.CommonName != names[0] {
		return true
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			n = ip.String()
		}
		if !have[n] {
			return true
		}
	}
	return false
}

// PKCS12 is a device's certificate, key and its tunnel's authority in one
// file, under password: what Android, iOS, macOS and Windows install. The
// legacy encryption, because it is the one every one of them opens.
func PKCS12(cert, key, ca, password string) ([]byte, error) {
	cb, _ := pem.Decode([]byte(cert))
	kb, _ := pem.Decode([]byte(key))
	ab, _ := pem.Decode([]byte(ca))
	if cb == nil || kb == nil || ab == nil {
		return nil, errors.New("ikev2conf: the device's certificate is missing")
	}
	c, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	auth, err := x509.ParseCertificate(ab.Bytes)
	if err != nil {
		return nil, err
	}
	return pkcs12.Legacy.Encode(k, c, []*x509.Certificate{auth}, password)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n.Add(n, big.NewInt(1))
}

func certPEM(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func keyPEM(k *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}
