package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// Saying so before the certificate runs out.
//
// A certificate that expires takes the panel and the subscription service
// with it: the browser refuses the sign-in page, and every customer's app
// stops fetching. Renewal is the installer's job and acme.sh's, but a
// panel that watched it expire without a word is a panel that let an
// operator find out from a customer. The dates are in the file it was
// already given, so this costs a read.

// certWarnWithin is how long before the end the warning starts. Long
// enough to renew by hand after an unattended renewal has failed twice.
const certWarnWithin = 14 * 24 * time.Hour

// certEnds reads the not-after of the first certificate in a PEM file,
// and the name it was issued for.
func certEnds(path string) (time.Time, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, "", err
	}
	for len(raw) > 0 {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return time.Time{}, "", err
		}
		name := c.Subject.CommonName
		if len(c.DNSNames) > 0 {
			name = strings.Join(c.DNSNames, ", ")
		}
		return c.NotAfter, name, nil
	}
	return time.Time{}, "", fmt.Errorf("no certificate in %s", path)
}

// checkCertificates warns when a certificate this panel serves with is
// close to its end, or past it.
func (a *Audit) checkCertificates(ctx context.Context) []Warning {
	var out []Warning
	now := time.Now()

	seen := map[string]bool{}
	type named struct{ what, path string }
	files := []named{{"panel", a.tlsCert}}
	if cfg, err := a.subs.Settings(ctx); err == nil && cfg.CertFile != "" {
		files = append(files, named{"subscription service", cfg.CertFile})
	}

	for _, f := range files {
		if f.path == "" || seen[f.path] {
			continue
		}
		seen[f.path] = true
		ends, name, err := certEnds(f.path)
		if err != nil {
			// A certificate the panel cannot read is not a warning of its
			// own: it is either not in use, or the panel would not have
			// started. Said at most once, in the log, by whoever loaded it.
			continue
		}
		if name == "" {
			name = f.path
		}
		left := ends.Sub(now)
		switch {
		case left <= 0:
			out = append(out, Warning{
				ID:       "certificate-expired",
				Severity: "high",
				Title:    fmt.Sprintf("The %s certificate expired on %s", f.what, ends.Format("2006-01-02")),
				Detail: fmt.Sprintf(
					"Browsers refuse the panel and every customer's app has stopped "+
						"fetching its configuration. The certificate is for %s.", name),
				Fix: "Renew it -- `w-ui` -> SSL Certificate Management, or acme.sh " +
					"on the server -- and the panel picks the new file up without a restart.",
			})
		case left <= certWarnWithin:
			out = append(out, Warning{
				ID:       "certificate-expiring",
				Severity: "medium",
				Title: fmt.Sprintf("The %s certificate expires in %d day(s), on %s",
					// Rounded up: a certificate with a day and a half left
					// has two days on it, not one.
					f.what, int(math.Ceil(left.Hours()/24)), ends.Format("2006-01-02")),
				Detail: fmt.Sprintf(
					"When it does, the panel stops opening and customers stop fetching "+
						"their configuration. The certificate is for %s.", name),
				Fix: "Check that the unattended renewal is working: it should have run " +
					"by now. `w-ui` -> SSL Certificate Management renews it by hand.",
			})
		}
	}
	return out
}
