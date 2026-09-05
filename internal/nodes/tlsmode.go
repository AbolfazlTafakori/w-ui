package nodes

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// How this panel decides it is really talking to the node it thinks it is.
//
// The token in that request is a bearer credential for a whole panel: whoever
// receives it can read every customer's keys and write the node's ruleset. So
// the question of who is on the other end is not a detail.
//
// Ordinary certificate verification is the default and is right whenever the
// node has a real certificate. It is no help for the common case, though: a
// node is often reached by bare address with a certificate it signed itself,
// and verification refuses that outright — which historically leaves operators
// turning verification off entirely, everywhere, forever.
//
// Pinning is the answer for that case. The panel is told exactly which public
// key to expect and accepts nothing else, which is stronger than ordinary
// verification rather than weaker: a certificate authority mis-issuing for that
// address does not help an attacker, because the key would still be wrong.

// TLSMode is how a node's certificate is checked.
type TLSMode = model.NodeTLSMode

// pinPrefix marks a fingerprint as the SHA-256 of a public key rather than of a
// whole certificate, so a stored value cannot be mistaken for the other kind.
const pinPrefix = "sha256/"

// Fingerprint is the pin for a certificate: the SHA-256 of its public key, in
// the same "sha256/base64" shape browsers and curl use.
//
// The public key rather than the whole certificate, so a node that renews with
// the same key keeps working. Pinning the certificate would mean a renewal —
// which is automatic and unannounced — silently cutting the node off.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return pinPrefix + base64.StdEncoding.EncodeToString(sum[:])
}

// clientFor builds the HTTP client to use for one node.
//
// One per node rather than one shared: the whole point is that each carries a
// different idea of what certificate to accept, and a shared transport would
// pool a connection opened under one node's rules and hand it to another.
func clientFor(node model.Node, timeout time.Duration) (*http.Client, error) {
	cfg, err := tlsConfigFor(node)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: cfg,
			// Nothing is kept between rounds. A node's mode can change while
			// the panel runs, and a pooled connection opened under the old one
			// would go on being used after.
			DisableKeepAlives: true,
		},
		// Redirects are refused. A node that answers with one is not the panel
		// we configured, and following it would send the token somewhere
		// nobody chose.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func tlsConfigFor(node model.Node) (*tls.Config, error) {
	switch node.TLSMode {
	case model.TLSPin:
		pin := strings.TrimSpace(node.TLSPin)
		if pin == "" {
			return nil, fmt.Errorf("this node is set to accept one certificate, but no fingerprint is stored for it")
		}
		return &tls.Config{
			// Turned off because the check below replaces it, not because
			// nothing is checked: an unpinned certificate is refused, and a
			// pinned one is accepted whoever signed it.
			InsecureSkipVerify:    true,
			VerifyPeerCertificate: verifyPin(pin),
			MinVersion:            tls.VersionTLS12,
		}, nil

	case model.TLSSkip:
		// Deliberate and, on a network anybody else can reach, wrong. It exists
		// because refusing to offer it is how operators end up disabling
		// verification somewhere worse.
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil

	default:
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
}

// verifyPin accepts the connection only if some certificate in the chain has
// the expected public key.
//
// The whole chain rather than only the leaf, so a node behind a proxy that
// presents an intermediate the operator pinned still works.
func verifyPin(want string) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return fmt.Errorf("the node presented no certificate at all")
		}
		var saw []string
		for _, der := range raw {
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				continue
			}
			got := Fingerprint(cert)
			if got == want {
				return nil
			}
			saw = append(saw, got)
		}
		// The one it presented is named, because the usual cause is a node
		// whose certificate was replaced and the answer is to look at the new
		// fingerprint and decide whether to trust it.
		return fmt.Errorf(
			"the node's certificate is not the one pinned for it: expected %s, it presented %s",
			want, strings.Join(saw, " "))
	}
}

// FetchPin opens a connection purely to read what certificate is there.
//
// Nothing is verified during the fetch, which is the point and also the whole
// of its weakness: it records whatever answers right now. Somebody already
// standing in the middle at that moment is what gets pinned. It is worth having
// because the alternative — an operator running openssl and copying a hash by
// hand — is how verification ends up switched off instead, but the page that
// offers it has to say what it is.
func FetchPin(address string, timeout time.Duration) (string, error) {
	host, err := hostPort(address)
	if err != nil {
		return "", err
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config:    &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", host, err)
	}
	defer conn.Close()

	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("%s answered but presented no certificate", host)
	}
	return Fingerprint(state.PeerCertificates[0]), nil
}

// hostPort turns a node's address into something to dial, defaulting the port
// the way a browser would.
func hostPort(address string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not an address this panel can dial", address)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("there is no certificate to read: %s is not an https address", address)
	}
	if u.Port() == "" {
		return net.JoinHostPort(u.Hostname(), "443"), nil
	}
	return u.Host, nil
}
