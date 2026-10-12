package occdriver

import (
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/devguide"
	"github.com/abolfazl/w-ui/internal/occonf"
)

// GuideValues are a customer's own values for the device guides: the
// address a device dials, the sign-in, and the certificate's pin.
func GuideValues(acc *model.Account, iface *model.Interface) devguide.Values {
	p := iface.OpenConnect.V
	v := devguide.Values{
		Protocol: string(model.ProtocolOpenConnect),
		Tunnel:   iface.Name,
		Port:     iface.ListenPort,
		Username: acc.Username,
		Password: acc.Secret,
	}
	servers := l2tpdriver.Servers(iface)
	if len(servers) > 0 {
		v.Server = servers[0]
		for _, s := range servers[1:] {
			v.Others = append(v.Others, occonf.URL(s, iface.ListenPort, p.Camouflage, p.CamouflageSecret))
		}
	} else {
		v.Server = iface.EndpointHost
	}
	v.OC = devguide.OCValues{
		URL:       occonf.URL(v.Server, iface.ListenPort, p.Camouflage, p.CamouflageSecret),
		Pin:       occonf.Pin(p.Cert),
		Untrusted: occonf.SelfSigned(p.Cert),
	}
	return v
}

// Profile is the sign-in as text for a person: OpenConnect is typed into
// Cisco Secure Client or another OpenConnect client.
func Profile(acc *model.Account, iface *model.Interface) string {
	return devguide.PlainText(GuideValues(acc, iface))
}

// WithheldProfile is Profile without the sign-in, for what the subscription
// link hands to whoever fetches it.
func WithheldProfile(acc *model.Account, iface *model.Interface) string {
	return devguide.WithheldText(GuideValues(acc, iface))
}
