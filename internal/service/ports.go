package service

import (
	"context"
	"fmt"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/routing"
)

// StandardPort is the port a protocol's built-in clients dial, for the
// protocols whose daemons hold a fixed port: L2TP's 1701, PPTP's 1723, and
// IKE's 500. Windows, iPhone, macOS and Android have nowhere to type another.
//
// A tunnel of one of these may still be given a port of its own. The daemon
// stays where it is and the port is redirected to it, so the built-in
// clients keep the standard port and the clients that take a port -- the
// strongSwan app, Linux, routers that ask -- can use the other, where the
// standard one is blocked.
func StandardPort(p model.Protocol) (int, bool) {
	switch p {
	case model.ProtocolL2TP:
		return l2tpconf.PortL2TP, true
	case model.ProtocolPPTP:
		return l2tpconf.PortPPTP, true
	case model.ProtocolIKEv2:
		return l2tpconf.PortIKE, true
	}
	return 0, false
}

// redirectTarget is where a tunnel's own port is sent: its daemon's port.
func redirectTarget(p model.Protocol) (proto string, port int) {
	switch p {
	case model.ProtocolPPTP:
		return "tcp", l2tpconf.PortPPTP
	case model.ProtocolL2TP:
		return "udp", l2tpconf.PortL2TP
	case model.ProtocolIKEv2:
		return "udp", l2tpconf.PortIKE
	}
	return "", 0
}

// PortRedirects are the redirects a tunnel on a port of its own needs; none
// for one on its standard port, or of another protocol.
//
// IKEv2's is two. A client dialing a port that is not 500 starts as it
// would on 500, plain, and is sent to strongSwan's 500; seeing the port
// changed on the way, it takes the server for one behind NAT and moves to
// IKE with the non-ESP marker and ESP in UDP, a flow of its own, which is
// sent to 4500, where strongSwan and the kernel take those. A client that
// starts with the marker goes to 4500 at once.
func PortRedirects(i model.Interface) []routing.PortRedirect {
	std, ok := StandardPort(i.Protocol)
	if !ok || i.ListenPort == std || i.ListenPort <= 0 || i.ListenPort > 65535 {
		return nil
	}
	proto, to := redirectTarget(i.Protocol)
	out := []routing.PortRedirect{{Proto: proto, From: uint16(i.ListenPort), To: uint16(to)}}
	if i.Protocol == model.ProtocolIKEv2 {
		out = append(out, routing.PortRedirect{Proto: "udp", From: uint16(i.ListenPort), To: l2tpconf.PortNATT, Marked: true})
	}
	return out
}

// checkAlternatePort refuses, for a tunnel of a fixed-port protocol, a port
// of its own that is one of the ports IPsec, L2TP or PPTP hold anyway.
func checkAlternatePort(p model.Protocol, port int) error {
	std, ok := StandardPort(p)
	if !ok || port == std {
		return nil
	}
	proto, _ := redirectTarget(p)
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
