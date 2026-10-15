package ikev2driver

import (
	"net/netip"
	"strings"

	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/devguide"
	"github.com/abolfazl/w-ui/internal/ikev2conf"
)

// GuideValues are a customer's own values for the device guides: the
// server, the identity the device announces, its password (and key), and
// the ways the tunnel allows.
func GuideValues(acc *model.Account, iface *model.Interface) devguide.Values {
	p := iface.IKEv2.V
	v := devguide.Values{
		Protocol: string(model.ProtocolIKEv2),
		Tunnel:   iface.Name,
		Username: acc.Username,
		Password: acc.Secret,
		ServerID: ServerID(iface),
		IKEv2: devguide.IKEv2Values{
			MSCHAPv2:    p.MSCHAPv2,
			PSK:         p.PSK,
			RSA:         p.RSA,
			EAPTLS:      p.EAPTLS,
			TLSIdentity: ikev2conf.TLSIdentity(acc.Username),
		},
	}
	if p.MSCHAPv2 || p.ByCertificate() {
		v.IKEv2.CAFile = CAFileName(iface)
	}
	if iface.ListenPort != 0 && iface.ListenPort != 500 {
		v.OwnPort = iface.ListenPort
	}
	if p.ByCertificate() {
		v.IKEv2.CertFile = CertFileName(acc)
	}
	servers := l2tpdriver.Servers(iface)
	if len(servers) > 0 {
		v.Server, v.Others = servers[0], servers[1:]
		if ips := l2tpdriver.ServerIPv4(v.Server); len(ips) > 0 {
			v.ServerIP = ips[0]
		}
	}
	return v
}

// CAFileName is the file a tunnel's authority is handed out as: what a
// device installs to know it is talking to this server.
func CAFileName(iface *model.Interface) string { return iface.Name + "-ca.crt" }

// CertFileName is the file a device's own certificate is handed out as.
func CertFileName(acc *model.Account) string { return acc.Username + ".p12" }

// CertBundle is a device's certificate, its key and the tunnel's authority,
// under the device's password; nothing when the tunnel signs in by none, or
// it has not been issued yet.
func CertBundle(acc *model.Account, iface *model.Interface) ([]byte, bool, error) {
	p := iface.IKEv2.V
	if !p.ByCertificate() || acc.CertPEM == "" || acc.CertKeyPEM == "" {
		return nil, false, nil
	}
	b, err := ikev2conf.PKCS12(acc.CertPEM, acc.CertKeyPEM, p.CACert, acc.Secret)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Profile is the sign-in as text for a person: IKEv2 is typed into the
// device's own VPN settings, or set up from a certificate file.
func Profile(acc *model.Account, iface *model.Interface) string {
	return devguide.PlainText(GuideValues(acc, iface))
}

// WithheldProfile is Profile without the sign-in, for what the subscription
// link hands to whoever fetches it.
func WithheldProfile(acc *model.Account, iface *model.Interface) string {
	return devguide.WithheldText(GuideValues(acc, iface))
}

func netipPrefix(subnet string) (string, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return "", err
	}
	return p.Masked().String(), nil
}

func netipParse(subnet string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(subnet))
	if err != nil {
		return netip.Prefix{}, err
	}
	return p.Masked(), nil
}
