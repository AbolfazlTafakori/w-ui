package service

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
	"strings"
	"testing"
	"time"
)

// writeCert puts a self-signed certificate for host on disk, ending at
// the given time, and returns its path.
func writeCert(t *testing.T, host string, ends time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    ends.Add(-90 * 24 * time.Hour),
		NotAfter:     ends,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fullchain.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The panel reads how long its certificate has left, and says so in time:
// nothing while there is room, a warning inside the last two weeks, and a
// louder one once it has passed.
func TestCertificateWarningsComeInTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		ends    time.Time
		wantID  string
		wantDay bool
	}{
		{"months left", now.Add(60 * 24 * time.Hour), "", false},
		{"a fortnight", now.Add(13 * 24 * time.Hour), "certificate-expiring", true},
		{"gone", now.Add(-2 * time.Hour), "certificate-expired", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			a := NewAudit(db, NewSubscriptions(db, nil, nil, quietLog()),
				"127.0.0.1:2096", writeCert(t, "panel.example.com", tc.ends), nil)
			var got *Warning
			for _, w := range a.checkCertificates(t.Context()) {
				if strings.HasPrefix(w.ID, "certificate-") {
					c := w
					got = &c
				}
			}
			if tc.wantID == "" {
				if got != nil {
					t.Fatalf("a certificate with room to spare warned: %+v", got)
				}
				return
			}
			if got == nil || got.ID != tc.wantID {
				t.Fatalf("want %q, got %+v", tc.wantID, got)
			}
			if !strings.Contains(got.Detail, "panel.example.com") {
				t.Fatalf("the warning does not say which certificate: %q", got.Detail)
			}
			if tc.wantDay && !strings.Contains(got.Title, "13 day") {
				t.Fatalf("the warning does not say how long is left: %q", got.Title)
			}
		})
	}

	// A path that is not a certificate is not a warning of its own.
	db := testDB(t)
	junk := filepath.Join(t.TempDir(), "not-a-cert.pem")
	if err := os.WriteFile(junk, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewAudit(db, NewSubscriptions(db, nil, nil, quietLog()), "127.0.0.1:2096", junk, nil)
	if got := a.checkCertificates(t.Context()); len(got) != 0 {
		t.Fatalf("an unreadable certificate warned: %+v", got)
	}
}
