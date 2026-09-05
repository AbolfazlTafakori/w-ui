package nodes

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// selfSigned builds a certificate of the kind a node reached by bare address
// actually presents.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "node.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// nodeServing starts a TLS server presenting cert, and returns a node pointing
// at it.
func nodeServing(t *testing.T, cert tls.Certificate, mode model.NodeTLSMode, pin string) (model.Node, func()) {
	t.Helper()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()

	return model.Node{
		Name: "berlin", Kind: model.KindRemote, Address: srv.URL,
		TLSMode: mode, TLSPin: pin,
	}, srv.Close
}

// The case pinning exists for: a node with a certificate it signed itself,
// which ordinary verification refuses outright.
func TestAPinnedCertificateIsAccepted(t *testing.T) {
	cert := selfSigned(t)
	pin := Fingerprint(cert.Leaf)

	node, stop := nodeServing(t, cert, model.TLSPin, pin)
	defer stop()

	client, err := clientFor(node, 5*time.Second)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	resp, err := client.Get(node.Address)
	if err != nil {
		t.Fatalf("a node presenting exactly the pinned certificate was refused: %v", err)
	}
	resp.Body.Close()
}

// The half that matters. Somebody standing in the middle with a certificate of
// their own — even one a real authority signed — is refused, because the key is
// not the one pinned.
func TestSomebodyElsesCertificateIsRefused(t *testing.T) {
	pinned := selfSigned(t)
	attacker := selfSigned(t)

	node, stop := nodeServing(t, attacker, model.TLSPin, Fingerprint(pinned.Leaf))
	defer stop()

	client, err := clientFor(node, 5*time.Second)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if resp, err := client.Get(node.Address); err == nil {
		resp.Body.Close()
		t.Fatal("a node presenting a different certificate was accepted")
	} else if !strings.Contains(err.Error(), "not the one pinned") {
		t.Errorf("the refusal does not say what went wrong: %v", err)
	}
}

// Ordinary verification still refuses a self-signed certificate, which is why
// pinning had to exist rather than being a nicety.
func TestVerificationStillRefusesASelfSignedNode(t *testing.T) {
	cert := selfSigned(t)
	node, stop := nodeServing(t, cert, model.TLSVerify, "")
	defer stop()

	client, err := clientFor(node, 5*time.Second)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if resp, err := client.Get(node.Address); err == nil {
		resp.Body.Close()
		t.Fatal("verification accepted a certificate signed by nobody")
	}
}

// Turning checking off has to actually work, or an operator with a node they
// cannot otherwise reach is stuck.
func TestSkippingChecksConnects(t *testing.T) {
	cert := selfSigned(t)
	node, stop := nodeServing(t, cert, model.TLSSkip, "")
	defer stop()

	client, err := clientFor(node, 5*time.Second)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	resp, err := client.Get(node.Address)
	if err != nil {
		t.Fatalf("skipping verification still refused the connection: %v", err)
	}
	resp.Body.Close()
}

// Pinning with nothing to pin would accept nothing at all. Caught when the
// client is built, so it reads as a configuration problem rather than as the
// node being unreachable every twenty seconds.
func TestPinningWithNoFingerprintIsAConfigurationError(t *testing.T) {
	node := model.Node{Name: "berlin", Address: "https://x.example", TLSMode: model.TLSPin}

	if _, err := clientFor(node, time.Second); err == nil {
		t.Fatal("pinning with no fingerprint was accepted")
	}
}

// The pin is of the public key, not of the certificate, so a node that renews
// with the same key keeps working. Pinning the certificate would mean an
// automatic renewal silently cutting the node off.
func TestRenewingWithTheSameKeyKeepsTheSamePin(t *testing.T) {
	first := selfSigned(t)

	// A second certificate over the same key, as a renewal produces.
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "node.example"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	key := first.PrivateKey.(*ecdsa.PrivateKey)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	if Fingerprint(first.Leaf) != Fingerprint(renewed) {
		t.Error("a renewal with the same key changed the pin, so renewing would cut the node off")
	}
}

// Reading a fingerprint off a live address is what saves an operator from
// running openssl by hand, which is how checking ends up switched off instead.
func TestTheFingerprintCanBeReadFromTheNode(t *testing.T) {
	cert := selfSigned(t)
	node, stop := nodeServing(t, cert, model.TLSSkip, "")
	defer stop()

	got, err := FetchPin(node.Address, 5*time.Second)
	if err != nil {
		t.Fatalf("FetchPin: %v", err)
	}
	if want := Fingerprint(cert.Leaf); got != want {
		t.Errorf("FetchPin read %s, want %s", got, want)
	}
	if !strings.HasPrefix(got, "sha256/") {
		t.Errorf("the fingerprint does not say what kind it is: %s", got)
	}
}

// There is nothing to read from a plain-HTTP address, and saying so beats a
// connection error an operator has to interpret.
func TestFetchingFromAPlainAddressSaysWhy(t *testing.T) {
	_, err := FetchPin("http://node.example:2096", time.Second)
	if err == nil {
		t.Fatal("fetching a certificate from a plain HTTP address was accepted")
	}
	if !strings.Contains(err.Error(), "not an https address") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}
}
