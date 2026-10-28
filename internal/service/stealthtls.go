package service

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
)

// wui-tls's certificate.
//
// A wui-tls gateway ends TLS itself, with one of two certificates:
//
//   - The tunnel's own, which the panel makes. Its key is drawn once and
//     kept for as long as the tunnel has wui-tls: devices trust the
//     certificate by that key's pin alone, so a new key would be a new pin,
//     and every app would need its profile again before it reached the
//     tunnel. The certificate is made again with the same key before it runs
//     out, which no device notices. It names Host when Host is a name, so
//     the name a device sends is the one the certificate carries; for an
//     address it names one drawn once and kept.
//   - The panel's own issued certificate, when the operator asks for it and
//     it is in date for Host on the panel's own server. Devices check it as
//     a browser does, by its chain and its name, so its renewals, which
//     bring new keys, need nothing from them. When it stops covering Host
//     the tunnel's own is served again, and profiles say so.

// tlsCertFor gives a wui-tls transport the certificate it should have: its
// own key when it has none, its own certificate when that is due, and the
// panel's when it is wanted and covers Host -- panel is the zero value for
// a tunnel that is not on the panel's server. It reports whether anything
// changed.
func tlsCertFor(p *model.StealthParams, panel PanelCert, now time.Time) (bool, error) {
	before := *p
	if p.CertKey == "" {
		k, err := stealth.NewCertificateKey()
		if err != nil {
			return false, err
		}
		p.CertKey, p.Cert = k, ""
	}
	name := ""
	if stealth.ValidCertificateName(p.Host) {
		name = p.Host
	}
	if stealth.CertificateDue(p.Cert, p.CertKey, name, now) {
		// A key that does not read is never replaced here: it is the one
		// every profile pins, and a new one is the operator's to ask for by
		// turning wui-tls off and on.
		if _, err := stealth.CertificateKeyPin(p.CertKey); err != nil {
			return false, fmt.Errorf("service: wui-tls's certificate key: %w", err)
		}
		cert, err := stealth.MakeCertificate(p.CertKey, name, p.Cert, now)
		if err != nil {
			return false, fmt.Errorf("service: make wui-tls's certificate: %w", err)
		}
		p.Cert = cert
	}
	p.PanelCert, p.PanelCertKey, p.CertSource = "", "", model.CertSourceOwn
	if p.UsePanelCert {
		if cert, key, ok := panelCertFor(panel, p.Host, now); ok {
			p.PanelCert, p.PanelCertKey, p.CertSource = cert, key, model.CertSourcePanel
		}
	}
	return *p != before, nil
}

// EnsureStealthCerts keeps every wui-tls transport this panel manages in
// its certificate: its own made again before it runs out, and the panel's
// taken up, renewed or let go. panel is the panel's own certificate, which
// only a tunnel on localNodeID takes. A tunnel another panel manages is
// that panel's to keep, and its certificate arrives with it.
func EnsureStealthCerts(ctx context.Context, db *gorm.DB, localNodeID uint, panel PanelCert, now time.Time) error {
	var ifaces []model.Interface
	if err := db.WithContext(ctx).Where("protocol = ? AND managed = ? AND stealth IS NOT NULL AND stealth <> ''", model.ProtocolWireGuard, false).
		Find(&ifaces).Error; err != nil {
		return fmt.Errorf("service: find wui-tls transports: %w", err)
	}
	for _, iface := range ifaces {
		p := iface.Stealth.V
		if p.Transport != model.TransportWuiTLS {
			continue
		}
		served := panel
		if iface.NodeID != localNodeID {
			served = PanelCert{}
		}
		changed, err := tlsCertFor(&p, served, now)
		if err != nil {
			return fmt.Errorf("service: %s: %w", iface.Name, err)
		}
		if !changed {
			continue
		}
		// Only where the transport is still as it was read, so a pass that
		// races an edit or another pass replaces nothing it did not see.
		if err := db.WithContext(ctx).Model(&model.Interface{}).Where("id = ? AND stealth = ?", iface.ID, iface.Stealth).
			UpdateColumn("stealth", model.JSON(p)).Error; err != nil {
			return fmt.Errorf("service: store %s's certificate: %w", iface.Name, err)
		}
	}
	return nil
}
