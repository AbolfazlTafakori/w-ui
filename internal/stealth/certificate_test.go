package stealth

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCertificateIsMadeAgainWithTheSameKey(t *testing.T) {
	key, err := NewCertificateKey()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := CertificateKeyPin(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !CertificateDue("", key, "", now) {
		t.Fatal("no certificate is not due")
	}
	first, err := MakeCertificate(key, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	c, info, err := ReadCertificate(first)
	if err != nil {
		t.Fatal(err)
	}
	life := info.NotAfter.Sub(info.NotBefore)
	switch {
	case info.Pin != pin:
		t.Fatal("the certificate is not of its key")
	case !ValidCertificateName(info.Name) || len(c.DNSNames) != 1 || c.DNSNames[0] != info.Name:
		t.Fatalf("name %q, alternatives %v", info.Name, c.DNSNames)
	case life < certLifeMin || life > certLifeMin+certLifeSpread*24*time.Hour:
		t.Fatalf("lives %v", life)
	case info.NotBefore.After(now) || info.NotBefore.Before(now.Add(-certBackdate-time.Second)):
		t.Fatalf("starts %v", info.NotBefore)
	case c.IsCA || c.KeyUsage != x509.KeyUsageDigitalSignature || len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth:
		t.Fatal("not a server's certificate")
	case c.SerialNumber.Sign() <= 0 || c.SerialNumber.BitLen() != 127 || len(c.SubjectKeyId) != 20:
		t.Fatalf("serial %v, key id %x", c.SerialNumber, c.SubjectKeyId)
	case strings.Contains(strings.ToLower(string(c.Raw)), "w-ui") || strings.Contains(strings.ToLower(string(c.Raw)), "wui"):
		t.Fatal("the certificate names W-UI")
	}
	if CertificateDue(first, key, "", now) || CertificateDue(first, key, info.Name, now) {
		t.Fatal("a fresh certificate is due")
	}
	if !CertificateDue(first, key, "www.example.org", now) {
		t.Fatal("a certificate for another name is not due")
	}
	due := info.NotAfter.Add(-CertificateLead)
	if CertificateDue(first, key, "", due.Add(-time.Second)) || !CertificateDue(first, key, "", due) {
		t.Fatal("not due at its lead")
	}
	if !CertificateDue(first, key, "", info.NotBefore.Add(-25*time.Hour)) {
		t.Fatal("a clock set back keeps a certificate not valid yet")
	}
	// Made afresh, it keeps its key and the name it drew.
	again, err := MakeCertificate(key, "", first, due)
	if err != nil {
		t.Fatal(err)
	}
	_, info2, _ := ReadCertificate(again)
	if info2.Pin != pin || info2.Name != info.Name || !info2.NotAfter.After(info.NotAfter) {
		t.Fatalf("renewed: %+v", info2)
	}
	// The operator's own name replaces the drawn one.
	named, err := MakeCertificate(key, "www.example.org", again, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, info3, _ := ReadCertificate(named); info3.Name != "www.example.org" || info3.Pin != pin {
		t.Fatalf("named: %+v", info3)
	}
	// Another key's certificate is due.
	other, _ := NewCertificateKey()
	if !CertificateDue(first, other, "", now) {
		t.Fatal("another key's certificate is not due")
	}
	if _, err := TLSCertificate(first, other); err == nil {
		t.Fatal("served a certificate with another key")
	}
}

func TestDrawnNamesDifferInShape(t *testing.T) {
	shapes := map[string]bool{}
	for range 200 {
		n, err := drawHostName()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidCertificateName(n) {
			t.Fatalf("%q is no host name", n)
		}
		host, suffix, _ := strings.Cut(n, ".")
		shape := suffix
		if strings.Contains(host, "-") {
			shape += " two words"
		} else if strings.ContainsAny(host, "0123456789") {
			shape += " numbered"
		}
		shapes[shape] = true
	}
	if len(shapes) < 15 {
		t.Fatalf("only %d shapes: %v", len(shapes), shapes)
	}
}

func TestPinnedHandshake(t *testing.T) {
	key, _ := NewCertificateKey()
	cert, _ := MakeCertificate(key, "", "", time.Now())
	served, err := TLSCertificate(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := CertificateKeyPin(key)
	if p, err := ParsePin(pin.String()); err != nil || p != pin {
		t.Fatal("a pin does not read back")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go tls.Server(a, &tls.Config{Certificates: []tls.Certificate{*served}}).Handshake()
	var seen Pin
	cl := tls.Client(b, &tls.Config{InsecureSkipVerify: true, VerifyConnection: func(cs tls.ConnectionState) error {
		seen = PinOf(cs.PeerCertificates[0])
		return nil
	}})
	if err := cl.Handshake(); err != nil {
		t.Fatal(err)
	}
	if seen != pin {
		t.Fatal("the pin a client sees is not the key's")
	}
}
