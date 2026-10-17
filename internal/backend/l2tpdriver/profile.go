package l2tpdriver

import (
	"github.com/abolfazl/w-ui/internal/backend/agentkit"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/devguide"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

// Profile is what a customer is handed for one device: there is no file an
// L2TP client imports, so it is what they type in, and where, on every system,
// router and modem the tunnel serves -- the same catalogue the customer's page
// shows one device of at a time.
func Profile(acc *model.Account, iface *model.Interface) string {
	return devguide.PlainText(GuideValues(acc, iface))
}

// WithheldProfile is Profile without the sign-in, for what the subscription
// link hands to whoever fetches it rather than to the customer's page.
func WithheldProfile(acc *model.Account, iface *model.Interface) string {
	return devguide.WithheldText(GuideValues(acc, iface))
}

// GuideValues are a customer's own values for the device guides: the
// tunnel's addresses and key, and this device's sign-in.
func GuideValues(acc *model.Account, iface *model.Interface) devguide.Values {
	v := devguide.Values{
		Protocol:   string(iface.Protocol),
		Tunnel:     iface.Name,
		PSK:        iface.L2TP.V.PSK,
		Username:   acc.Username,
		Password:   acc.Secret,
		AllowPlain: iface.L2TP.V.AllowPlain,
	}
	if std := standardPort(iface.Protocol); iface.ListenPort != 0 && iface.ListenPort != std {
		v.OwnPort = iface.ListenPort
	}
	servers := agentkit.Servers(iface)
	if len(servers) > 0 {
		v.Server = servers[0]
		v.Others = servers[1:]
		// Numbers for the routers whose field takes no name: Omada's, which
		// has IPsec and is listed on every tunnel, as well as the others.
		if ips := serverIPv4(v.Server); len(ips) > 0 {
			v.ServerIP = ips[0]
		}
	}
	return v
}

// serverIPv4 is agentkit's; a test of this package asks something else.
var serverIPv4 = agentkit.ServerIPv4

// standardPort is the port the protocol's daemon holds.
func standardPort(p model.Protocol) int {
	if p == model.ProtocolPPTP {
		return l2tpconf.PortPPTP
	}
	return l2tpconf.PortL2TP
}
