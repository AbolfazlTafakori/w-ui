package nodes

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

func mtlsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	return db
}

// The identity is minted once and then kept. Minting a new one on every call
// would mean every node's stored authority stopped matching the moment the
// panel restarted.
func TestTheIdentityIsMintedOnceAndKept(t *testing.T) {
	db := mtlsDB(t)

	first, err := EnsureIdentity(db)
	if err != nil {
		t.Fatalf("EnsureIdentity: %v", err)
	}
	second, err := EnsureIdentity(db)
	if err != nil {
		t.Fatalf("EnsureIdentity again: %v", err)
	}

	if first.CACertPEM != second.CACertPEM {
		t.Error("the authority changed between calls; every node's trust would break")
	}
	if first.ClientCertPEM != second.ClientCertPEM {
		t.Error("the client certificate changed between calls")
	}
	if !strings.Contains(first.CACertPEM, "BEGIN CERTIFICATE") {
		t.Error("the authority is not a certificate anybody could paste")
	}
}

// What is handed to an operator to paste into a node is the public half. The
// key staying here is the whole reason a certificate is worth more than a
// token — one that travelled would be a token with extra steps.
func TestTheAuthorityHandedOutCarriesNoKey(t *testing.T) {
	db := mtlsDB(t)

	id, err := EnsureIdentity(db)
	if err != nil {
		t.Fatalf("EnsureIdentity: %v", err)
	}
	if strings.Contains(id.CACertPEM, "PRIVATE KEY") {
		t.Fatal("the authority handed out contains a private key")
	}
}

// The client certificate has to be signed by the authority, or a node that
// trusts the authority would refuse the panel that issued it.
func TestTheClientCertificateIsSignedByTheAuthority(t *testing.T) {
	db := mtlsDB(t)

	id, err := EnsureIdentity(db)
	if err != nil {
		t.Fatalf("EnsureIdentity: %v", err)
	}
	pool, err := TrustPool(id.CACertPEM)
	if err != nil {
		t.Fatalf("TrustPool: %v", err)
	}

	cert, err := tls.X509KeyPair([]byte(id.ClientCertPEM), []byte(id.ClientKeyPEM))
	if err != nil {
		t.Fatalf("the client certificate and key do not go together: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("a node trusting this authority would refuse the panel that issued it: %v", err)
	}
}

// ── what a node actually sees ───────────────────────────────────────────────

// The point of the whole arrangement: a caller with a perfectly good token and
// no certificate is refused. A token travels in every request and can be read
// out of a log or a backup; a key does not.
func TestANodeRefusesACallerWithNoCertificate(t *testing.T) {
	db := mtlsDB(t)
	id, err := EnsureIdentity(db)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := TrustPool(id.CACertPEM)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	defer srv.Close()

	// A client that knows the address and would know the token, presenting
	// nothing of its own.
	bare := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	if resp, err := bare.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("a caller with no client certificate was let in")
	}
}

// And the managing panel, presenting the certificate it minted, is let in.
func TestTheManagingPanelIsLetIn(t *testing.T) {
	db := mtlsDB(t)
	id, err := EnsureIdentity(db)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := TrustPool(id.CACertPEM)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	defer srv.Close()

	cert, err := clientCertificate(id)
	if err != nil {
		t.Fatalf("clientCertificate: %v", err)
	}
	managing := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       []tls.Certificate{cert},
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := managing.Get(srv.URL)
	if err != nil {
		t.Fatalf("the panel that minted the authority was refused: %v", err)
	}
	resp.Body.Close()
}

// A different panel's certificate is refused, which is what makes the authority
// mean anything: trusting one is not trusting everybody who has a certificate.
func TestAnotherPanelsCertificateIsRefused(t *testing.T) {
	ours := mtlsDB(t)
	theirs := mtlsDB(t)

	mine, err := EnsureIdentity(ours)
	if err != nil {
		t.Fatal(err)
	}
	other, err := EnsureIdentity(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if mine.CACertPEM == other.CACertPEM {
		t.Fatal("two panels minted the same authority, so this proves nothing")
	}

	pool, err := TrustPool(mine.CACertPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := clientCertificate(other)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err == nil {
		t.Fatal("a node trusting one panel accepted another panel's certificate")
	}
}

// Nothing configured means the token alone, which is what an operator who has
// not set this up is relying on.
func TestNoAuthorityMeansNoRequirement(t *testing.T) {
	pool, err := TrustPool("")
	if err != nil {
		t.Fatalf("TrustPool(\"\"): %v", err)
	}
	if pool != nil {
		t.Error("an empty authority produced a pool, which would refuse every caller")
	}
}

// A bad paste is caught when it is stored rather than by every request after.
func TestSomethingThatIsNotACertificateIsRefused(t *testing.T) {
	if _, err := TrustPool("hello"); err == nil {
		t.Error("a value that is not a certificate was accepted as an authority")
	}
}
