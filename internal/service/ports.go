package service

import (
	"context"
	"fmt"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/tunnelport"
)

// StandardPort is the port a protocol's built-in clients dial, for the
// protocols whose daemons hold a fixed port (see tunnelport).
func StandardPort(p model.Protocol) (int, bool) { return tunnelport.Standard(p) }

// PortRedirects are the redirects a tunnel on a port of its own needs.
func PortRedirects(i model.Interface) []routing.PortRedirect { return tunnelport.Redirects(i) }

// checkAlternatePort refuses, for a tunnel of a fixed-port protocol, a port
// of its own that is one of the ports IPsec, L2TP or PPTP hold anyway.
func checkAlternatePort(p model.Protocol, port int) error {
	std, ok := StandardPort(p)
	if !ok || port == std {
		return nil
	}
	proto := tunnelport.Transport(p)
	reserved := map[int]bool{l2tpconf.PortIKE: true, l2tpconf.PortNATT: true, l2tpconf.PortL2TP: true}
	if proto == "tcp" {
		reserved = map[int]bool{l2tpconf.PortPPTP: true}
	}
	if reserved[port] {
		return invalidField("listenPort", "%s %d is held by IPsec, L2TP or PPTP themselves; choose another port, or %d for the standard one", proto, port, std)
	}
	return nil
}

// transportsOf is what a tunnel takes on its port.
func transportsOf(p model.Protocol, openvpnTransport string, dtls bool) []string {
	switch p {
	case model.ProtocolPPTP:
		return []string{"tcp"}
	case model.ProtocolOpenVPN:
		if openvpnTransport == "tcp" {
			return []string{"tcp"}
		}
		return []string{"udp"}
	case model.ProtocolOpenConnect:
		if dtls {
			return []string{"tcp", "udp"}
		}
		return []string{"tcp"}
	}
	return []string{"udp"}
}

func ifaceTransports(i model.Interface) []string {
	return transportsOf(i.Protocol, i.OpenVPN.V.Transport, i.OpenConnect.V.DTLS)
}

// checkPortClash refuses a port another tunnel on the same server already
// takes on the same transport. Asking the machine what listens there is not
// enough: a redirected port has nothing listening on it, and a tunnel that
// is switched off holds nothing until it is switched on again.
func (s *Interfaces) checkPortClash(ctx context.Context, nodeID, selfID uint, port int, transports []string) error {
	var others []model.Interface
	q := s.db.WithContext(ctx).Where("node_id = ? AND listen_port = ?", nodeID, port)
	if selfID != 0 {
		q = q.Where("id <> ?", selfID)
	}
	if err := q.Find(&others).Error; err != nil {
		return fmt.Errorf("service: check port: %w", err)
	}
	for _, o := range others {
		for _, a := range ifaceTransports(o) {
			for _, b := range transports {
				if a == b {
					return invalidField("listenPort", "%s %d is already the port of the tunnel %q on this server", b, port, o.Name)
				}
			}
		}
	}
	return nil
}
