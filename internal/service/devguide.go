package service

import (
	"github.com/abolfazl/w-ui/internal/backend/ikev2driver"
	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/backend/occdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/devguide"
	"github.com/abolfazl/w-ui/internal/tunneldns"
	"github.com/abolfazl/w-ui/internal/wgconf"
)

// guideValues are one file's values for the device guides, whatever its
// protocol: what the fields of the device a customer picks are filled with.
// iface is the interface as this file sees it -- for a file written for one
// host, with that host's address and port -- and file the name it is handed
// out under, for the devices that import it.
func guideValues(acc *model.Account, iface *model.Interface, file string) devguide.Values {
	if iface.Protocol == model.ProtocolL2TP {
		v := l2tpdriver.GuideValues(acc, iface)
		v.File = file
		return v
	}
	if iface.Protocol == model.ProtocolIKEv2 {
		v := ikev2driver.GuideValues(acc, iface)
		v.File = file
		return v
	}
	if iface.Protocol == model.ProtocolOpenConnect {
		v := occdriver.GuideValues(acc, iface)
		v.File = file
		return v
	}
	v := devguide.Values{
		Protocol: string(iface.Protocol),
		Tunnel:   iface.Name,
		Server:   iface.EndpointHost,
		Port:     iface.ListenPort,
		File:     file,
	}
	if ips := l2tpdriver.ServerIPv4(v.Server); len(ips) > 0 {
		v.ServerIP = ips[0]
	}
	switch iface.Protocol {
	case model.ProtocolWireGuard:
		v.WG = devguide.WGValues{
			PrivateKey:   acc.PrivateKey,
			Address:      acc.IP + "/32",
			DNS:          tunneldns.For(iface),
			MTU:          iface.MTU,
			PublicKey:    iface.PublicKey,
			PresharedKey: acc.PresharedKey,
			AllowedIPs:   "0.0.0.0/0, ::/0",
			Keepalive:    wgconf.DefaultKeepalive,
			Amnezia:      iface.Mode == model.ModeAmnezia,
		}
	case model.ProtocolOpenVPN:
		v.Username, v.Password = acc.Username, acc.Secret
	}
	return v
}
