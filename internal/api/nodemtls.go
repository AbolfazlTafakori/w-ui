package api

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/nodes"
)

// This panel, as a node, checking which panel is calling.
//
// The bearer token says the caller knows a secret. It travels in every request,
// so it can be read out of a log, a backup, or a proxy that was keeping bodies,
// and from then on it is full access to this node for as long as nobody
// notices. Requiring a client certificate as well closes that: the key never
// leaves the managing panel, and what crosses the wire is a signature over data
// unique to one connection, worth nothing to whoever captures it.
//
// Off unless an authority has been pasted in. A node reached only over a
// private link, or one where the token is enough, should not be made harder to
// set up for a protection its operator did not ask for.

// clientCertHeader is what a terminating proxy passes the certificate in.
//
// nginx writes it with $ssl_client_escaped_cert, which is the PEM with the
// characters a header cannot carry percent-encoded. The panel usually sits
// behind a proxy holding the certificate, so without this the feature would
// only work in the one deployment that terminates TLS itself.
const clientCertHeader = "X-Client-Cert"

// requireManagingPanel wraps the node endpoints with a client-certificate check.
//
// Applied on top of the token check rather than instead of it: both are
// required when this is switched on, which is the whole point of having two.
func (s *Server) requireManagingPanel(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pool := s.nodeTrust()
		if pool == nil {
			// Nothing configured: the token alone is the arrangement here.
			next(w, r)
			return
		}

		cert, err := presentedCertificate(r, s)
		if err != nil {
			s.log.Warn("a node request arrived without the client certificate this panel requires",
				"ip", clientIP(r), "reason", err)
			writeError(w, http.StatusUnauthorized,
				"this node only accepts a managing panel that presents a client certificate")
			return
		}

		if _, err := cert.Verify(x509.VerifyOptions{
			Roots:     pool,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}); err != nil {
			s.log.Warn("a node request presented a client certificate this panel does not trust",
				"ip", clientIP(r), "subject", cert.Subject.CommonName, "error", err)
			writeError(w, http.StatusUnauthorized,
				"that client certificate was not signed by the authority this node trusts")
			return
		}

		next(w, r)
	}
}

// presentedCertificate finds the caller's certificate, from the connection when
// this panel terminates TLS and from the proxy's header when it does not.
func presentedCertificate(r *http.Request, s *Server) (*x509.Certificate, error) {
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		return r.TLS.PeerCertificates[0], nil
	}

	// Only from a proxy we were told to trust. Believing this header from any
	// caller would let anybody hand us a certificate they did not have the key
	// for, which is worse than not checking at all.
	if !trusted(net.ParseIP(peerIP(r))) {
		return nil, errNoCert("no certificate on the connection, and the caller is not a trusted proxy")
	}
	raw := r.Header.Get(clientCertHeader)
	if raw == "" {
		return nil, errNoCert("the proxy passed no certificate")
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		decoded = raw
	}
	// nginx escapes newlines as tabs in some configurations.
	decoded = strings.ReplaceAll(decoded, "\t", "\n")

	block, _ := pem.Decode([]byte(decoded))
	if block == nil {
		return nil, errNoCert("what the proxy passed is not a certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errNoCert("what the proxy passed could not be read as a certificate")
	}
	return cert, nil
}

type errNoCert string

func (e errNoCert) Error() string { return string(e) }

// nodeTrust returns the authority this panel accepts from a managing panel, or
// nil when none is configured.
//
// Read per request rather than cached: an operator pasting an authority in
// should have it take effect without a restart, and this runs on a request that
// arrives every twenty seconds rather than on a hot path.
func (s *Server) nodeTrust() *x509.CertPool {
	pemText, ok, err := database.GetSetting(s.db, nodes.KeyMTLSTrustCA)
	if err != nil || !ok || strings.TrimSpace(pemText) == "" {
		return nil
	}
	pool, err := nodes.TrustPool(pemText)
	if err != nil {
		// Stored but unreadable. Refusing everything would take the node
		// offline over a bad paste; reporting it and falling back to the token
		// keeps it working and says so.
		s.log.Error("the stored managing-panel authority cannot be read; "+
			"client certificates are not being required", "error", err)
		return nil
	}
	return pool
}
