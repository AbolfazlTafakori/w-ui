package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend/ikev2driver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ikev2conf"
)

// ikev2Modes reads the ways in an operator chose for an IKEv2 tunnel. None
// chosen is all four: every one is a type a phone or a computer has built in,
// and a tunnel that lets nobody in is not one anybody means to make.
func ikev2Modes(modes []string) (model.IKEv2Params, error) {
	var p model.IKEv2Params
	for _, m := range modes {
		switch strings.ToLower(strings.TrimSpace(m)) {
		case ikev2conf.ModeMSCHAPv2:
			p.MSCHAPv2 = true
		case ikev2conf.ModePSK:
			p.PSK = true
		case ikev2conf.ModeRSA:
			p.RSA = true
		case ikev2conf.ModeEAPTLS:
			p.EAPTLS = true
		default:
			return p, invalidField("ikev2Modes", "an IKEv2 way in is %q, %q, %q or %q, not %q",
				ikev2conf.ModeMSCHAPv2, ikev2conf.ModePSK, ikev2conf.ModeRSA, ikev2conf.ModeEAPTLS, m)
		}
	}
	if !p.AnyMode() {
		p.MSCHAPv2, p.PSK, p.RSA, p.EAPTLS = true, true, true, true
	}
	return p, nil
}

// newIKEv2Params is a new tunnel's: its ways in, its own authority, and the
// server's certificate for what devices dial.
func newIKEv2Params(name, host string, modes []string) (model.IKEv2Params, error) {
	p, err := ikev2Modes(modes)
	if err != nil {
		return p, err
	}
	ca, err := ikev2conf.NewAuthority(name)
	if err != nil {
		return p, err
	}
	p.CACert, p.CAKey = ca.Cert, ca.Key
	if err := renewIKEv2Server(&p, host, time.Now()); err != nil {
		return p, err
	}
	return p, nil
}

// renewIKEv2Server gives the server a new certificate when it has none, it
// nears its end, or what devices dial has changed: a device that checks it
// refuses one that does not name the server it dialled.
func renewIKEv2Server(p *model.IKEv2Params, host string, now time.Time) error {
	names := []string{strings.TrimSpace(host)}
	if !ikev2conf.NeedsRenewal(p.ServerCert, names, now) {
		return nil
	}
	srv, err := ikev2conf.Authority{Cert: p.CACert, Key: p.CAKey}.IssueServer(names)
	if err != nil {
		return err
	}
	p.ServerCert, p.ServerKey, p.ServerNames = srv.Cert, srv.Key, names
	return nil
}

// deviceCertStale reports whether a device's certificate has to be issued
// again: none yet, not for its username, not from its tunnel's authority, or
// near its end.
func deviceCertStale(certPEM, username string, ca *x509.Certificate, now time.Time) bool {
	b, _ := pem.Decode([]byte(certPEM))
	if b == nil {
		return true
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil || c.Subject.CommonName != username || c.CheckSignatureFrom(ca) != nil {
		return true
	}
	return now.Add(ikev2conf.RenewBefore).After(c.NotAfter) && c.NotAfter.Before(ca.NotAfter)
}

// EnsureIKEv2Certs keeps this server's IKEv2 tunnels' certificates current:
// the server's, and one for every device on a tunnel that lets devices sign
// in by certificate. Done here, on the reconciler's pass, rather than in
// each place an account is made or renamed, so no path that changes a
// username or a tunnel can leave a device with a certificate for somebody
// else. A new key is slow to make, which is why a certificate is only
// issued when it has to be.
func EnsureIKEv2Certs(ctx context.Context, db *gorm.DB, nodeID uint, now time.Time) error {
	var ifaces []model.Interface
	// Not a tunnel a panel manages here: its authority stays with that
	// panel, and what it needs arrives with it.
	if err := db.WithContext(ctx).Where("protocol = ? AND node_id = ? AND managed = ?", model.ProtocolIKEv2, nodeID, false).
		Find(&ifaces).Error; err != nil {
		return fmt.Errorf("service: load IKEv2 tunnels: %w", err)
	}
	for i := range ifaces {
		f := &ifaces[i]
		p := f.IKEv2.V
		if p.CAKey == "" {
			continue
		}
		before := p.ServerCert
		if err := renewIKEv2Server(&p, f.EndpointHost, now); err != nil {
			return fmt.Errorf("service: renew %s's certificate: %w", f.Name, err)
		}
		if p.ServerCert != before {
			if err := db.WithContext(ctx).Model(&model.Interface{}).Where("id = ?", f.ID).
				Update("ikev2", model.JSON(p)).Error; err != nil {
				return fmt.Errorf("service: store %s's certificate: %w", f.Name, err)
			}
		}
		if !p.ByCertificate() {
			continue
		}
		cb, _ := pem.Decode([]byte(p.CACert))
		if cb == nil {
			continue
		}
		ca, err := x509.ParseCertificate(cb.Bytes)
		if err != nil {
			return fmt.Errorf("service: %s's authority: %w", f.Name, err)
		}
		var accs []model.Account
		if err := db.WithContext(ctx).Where("interface_id = ?", f.ID).Find(&accs).Error; err != nil {
			return fmt.Errorf("service: load %s's devices: %w", f.Name, err)
		}
		auth := ikev2conf.Authority{Cert: p.CACert, Key: p.CAKey}
		for _, a := range accs {
			if a.Username == "" || !deviceCertStale(a.CertPEM, a.Username, ca, now) {
				continue
			}
			dev, err := auth.IssueDevice(a.Username)
			if err != nil {
				return fmt.Errorf("service: issue a certificate for %s: %w", a.Username, err)
			}
			if err := db.WithContext(ctx).Model(&model.Account{}).Where("id = ?", a.ID).
				Updates(map[string]any{"cert_pem": dev.Cert, "cert_key_pem": dev.Key}).Error; err != nil {
				return fmt.Errorf("service: store %s's certificate: %w", a.Username, err)
			}
		}
	}
	return nil
}

// IKEv2 file kinds a customer's page hands out.
const (
	IKEv2FileCA   = "ca"
	IKEv2FileCert = "p12"
)

// IKEv2FileFor is one of a customer's IKEv2 files: the tunnel's authority,
// which says nothing secret, or a device's certificate, which opens only
// with that device's password. accountID must be the customer's own.
func (s *Subscriptions) IKEv2FileFor(ctx context.Context, token string, accountID uint, what string) (string, []byte, error) {
	c, err := s.byToken(ctx, token)
	if err != nil {
		return "", nil, err
	}
	var acc model.Account
	if err := s.db.WithContext(ctx).Where("id = ? AND client_id = ?", accountID, c.ID).First(&acc).Error; err != nil {
		return "", nil, fmt.Errorf("%w: file", ErrNotFound)
	}
	var iface model.Interface
	if err := s.db.WithContext(ctx).First(&iface, acc.InterfaceID).Error; err != nil || iface.Protocol != model.ProtocolIKEv2 {
		return "", nil, fmt.Errorf("%w: file", ErrNotFound)
	}
	p := iface.IKEv2.V
	switch what {
	case IKEv2FileCA:
		if p.CACert == "" {
			break
		}
		return ikev2driver.CAFileName(&iface), []byte(p.CACert), nil
	case IKEv2FileCert:
		b, ok, err := ikev2driver.CertBundle(&acc, &iface)
		if err != nil {
			return "", nil, err
		}
		if ok {
			return ikev2driver.CertFileName(&acc), b, nil
		}
	}
	return "", nil, fmt.Errorf("%w: file", ErrNotFound)
}
