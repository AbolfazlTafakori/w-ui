package certfile

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePair(t *testing.T, dir, cn string) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	kd, _ := x509.MarshalECPrivateKey(key)
	c, k := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(k, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600)
	return c, k
}

func TestReloadsWhenFilesChange(t *testing.T) {
	dir := t.TempDir()
	c, k := writePair(t, dir, "one")
	l, err := New(c, k)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := l.GetCertificate(nil)
	writePair(t, dir, "two")
	past := time.Now().Add(-time.Hour)
	os.Chtimes(c, past, past)
	os.Chtimes(k, past, past)
	l.checked = time.Time{} // pretend a minute passed
	second, _ := l.GetCertificate(nil)
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("the rewritten certificate was not picked up")
	}
}

func TestBadPairIsReportedAtStart(t *testing.T) {
	if _, err := New("/nonexistent/c", "/nonexistent/k"); err == nil {
		t.Fatal("expected an error")
	}
}
