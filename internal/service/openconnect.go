package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/occonf"
)

// PanelCertFiles are where the panel's own certificate and key are, when it
// has them (WUI_TLS_CERT, WUI_TLS_KEY): an OpenConnect tunnel that dials the
// same name is given the panel's certificate rather than one of its own,
// since a device trusts that without being told to. Set by the panel.
var PanelCertFiles = func() (cert, key string) { return "", "" }

// OpenConnectInput is what the form says of an OpenConnect tunnel. A field
// left out keeps what the tunnel has, or for a new tunnel, the default:
// DTLS on, camouflage on with a secret drawn here, and the panel's own
// certificate when it covers the tunnel's address.
type OpenConnectInput struct {
	DTLS             *bool   `json:"dtls"`
	Camouflage       *bool   `json:"camouflage"`
	CamouflageSecret *string `json:"camouflageSecret"`
	UsePanelCert     *bool   `json:"usePanelCert"`
}

const camouflageAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// newCamouflageSecret draws a secret of letters and digits.
func newCamouflageSecret() (string, error) {
	b := make([]byte, 16)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(camouflageAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = camouflageAlphabet[n.Int64()]
	}
	return string(b), nil
}

// applyOpenConnect puts what the form said onto a tunnel's settings, and
// draws a camouflage secret when camouflage is on and there is none.
func applyOpenConnect(p *model.OpenConnectParams, in *OpenConnectInput) error {
	if in != nil {
		if in.DTLS != nil {
			p.DTLS = *in.DTLS
		}
		if in.Camouflage != nil {
			p.Camouflage = *in.Camouflage
		}
		if in.CamouflageSecret != nil {
			s := strings.TrimSpace(*in.CamouflageSecret)
			if s != "" {
				if err := occonf.CheckCamouflage(s); err != nil {
					return invalidField("openconnect.camouflageSecret", "the camouflage secret is %d to %d letters and digits", occonf.MinCamouflage, occonf.MaxCamouflage)
				}
			}
			p.CamouflageSecret = s
		}
		if in.UsePanelCert != nil {
			p.UsePanelCert = *in.UsePanelCert
		}
	}
	if p.Camouflage && p.CamouflageSecret == "" {
		s, err := newCamouflageSecret()
		if err != nil {
			return err
		}
		p.CamouflageSecret = s
	}
	return nil
}

// newOpenConnectParams is a new tunnel's settings, with its certificate.
func newOpenConnectParams(host string, in *OpenConnectInput) (model.OpenConnectParams, error) {
	p := model.OpenConnectParams{DTLS: true, Camouflage: true, UsePanelCert: true}
	if err := applyOpenConnect(&p, in); err != nil {
		return p, err
	}
	if _, err := ocCertFor(&p, host, time.Now()); err != nil {
		return p, err
	}
	return p, nil
}

// ocCertFor gives a tunnel the certificate it should have: the panel's,
// when it is wanted and covers the address devices dial; otherwise one of
// its own, made again when it nears its end or the address changed. It
// reports whether anything changed.
func ocCertFor(p *model.OpenConnectParams, host string, now time.Time) (bool, error) {
	host = strings.TrimSpace(host)
	if p.UsePanelCert {
		if cert, key, ok := panelCertFor(host, now); ok {
			if p.Cert == cert && p.Key == key && p.CertSource == "panel" {
				return false, nil
			}
			p.Cert, p.Key, p.CertSource = cert, key, "panel"
			return true, nil
		}
	}
	if p.CertSource == "own" && !occonf.NeedsRenewal(p.Cert, host, now) {
		return false, nil
	}
	cert, key, err := occonf.NewServerCert([]string{host})
	if err != nil {
		return false, fmt.Errorf("service: make the OpenConnect certificate: %w", err)
	}
	p.Cert, p.Key, p.CertSource = cert, key, "own"
	return true, nil
}

// panelCertFor is the panel's certificate, when it has one in date for a
// name.
func panelCertFor(host string, now time.Time) (cert, key string, ok bool) {
	cf, kf := PanelCertFiles()
	if cf == "" || kf == "" {
		return "", "", false
	}
	c, err := os.ReadFile(cf)
	if err != nil {
		return "", "", false
	}
	k, err := os.ReadFile(kf)
	if err != nil {
		return "", "", false
	}
	if !occonf.Covers(string(c), host, now) {
		return "", "", false
	}
	return string(c), string(k), true
}

// EnsureOpenConnectCerts keeps this server's OpenConnect tunnels'
// certificates current on the reconciler's pass: the panel's, as it is
// renewed, or the tunnel's own, renewed before it ends.
func EnsureOpenConnectCerts(ctx context.Context, db *gorm.DB, nodeID uint, now time.Time) error {
	var ifaces []model.Interface
	if err := db.WithContext(ctx).Where("protocol = ? AND node_id = ?", model.ProtocolOpenConnect, nodeID).
		Find(&ifaces).Error; err != nil {
		return fmt.Errorf("service: load OpenConnect tunnels: %w", err)
	}
	for i := range ifaces {
		f := &ifaces[i]
		p := f.OpenConnect.V
		changed, err := ocCertFor(&p, f.EndpointHost, now)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		if err := db.WithContext(ctx).Model(&model.Interface{}).Where("id = ?", f.ID).
			Update("openconnect", model.JSON(p)).Error; err != nil {
			return fmt.Errorf("service: store %s's certificate: %w", f.Name, err)
		}
	}
	return nil
}
