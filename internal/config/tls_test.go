package config

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

// writePair puts a self-signed certificate and its key in a temporary
// directory and returns both paths.
func writePair(t *testing.T, name string) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	der, err = x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// A certificate that will not work has to be refused at boot. The panel would
// otherwise start, report itself healthy, and fail every connection — which
// looks like a network fault and is not one.
func TestTLSValidation(t *testing.T) {
	certA, keyA := writePair(t, "a")
	_, keyB := writePair(t, "b")

	cases := []struct {
		name       string
		cert, key  string
		wantErr    string
		wantTLS    bool
		wantScheme string
	}{
		{
			name: "neither is plain http", wantScheme: "http",
		},
		{
			name: "certificate without a key",
			cert: certA,
			// Half a pair is a typo, not a request for HTTP.
			wantErr: "WUI_TLS_CERT is set without",
		},
		{
			name: "key without a certificate",
			key:  keyA,
			// The message names the half that is present, and what it needs.
			wantErr: "WUI_TLS_KEY is set without",
		},
		{
			name: "a path that is not there",
			cert: filepath.Join(t.TempDir(), "absent.crt"), key: keyA,
			wantErr: "WUI_TLS_CERT",
		},
		{
			name: "a certificate and a key from different pairs",
			cert: certA, key: keyB,
			wantErr: "do not form a usable pair",
		},
		{
			name: "a matching pair",
			cert: certA, key: keyA,
			wantTLS: true, wantScheme: "https",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.DBSource = "x.db"
			c.TLSCert, c.TLSKey = tc.cert, tc.key

			err := c.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want no error", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("Validate() = %v, want it to mention %q", err, tc.wantErr)
			}
			if tc.wantErr != "" {
				return
			}
			if got := c.TLS(); got != tc.wantTLS {
				t.Errorf("TLS() = %v, want %v", got, tc.wantTLS)
			}
			if got := c.Scheme(); got != tc.wantScheme {
				t.Errorf("Scheme() = %q, want %q", got, tc.wantScheme)
			}
		})
	}
}

// The environment is the only way an operator sets these, so it is worth one
// test that they arrive at all.
func TestLoadReadsTLSFromEnvironment(t *testing.T) {
	cert, key := writePair(t, "env")
	t.Setenv("WUI_TLS_CERT", cert)
	t.Setenv("WUI_TLS_KEY", key)
	t.Setenv("WUI_DATA_DIR", t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if !c.TLS() || c.Scheme() != "https" {
		t.Fatalf("Load() gave TLS=%v scheme=%q, want true/https", c.TLS(), c.Scheme())
	}
}
