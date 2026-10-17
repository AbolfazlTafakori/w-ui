// Package tunnelport is the ports of the protocols whose daemons hold a fixed
// one, and what a tunnel of one of them on a port of its own is redirected
// by. The tunnels' side and the routing side both need it.
package tunnelport

import (
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
	"github.com/abolfazl/w-ui/internal/routing"
)

// Standard is the port a protocol's built-in clients dial, for the
// protocols whose daemons hold a fixed port: L2TP's 1701, PPTP's 1723, and
// IKE's 500. Windows, iPhone, macOS and Android have nowhere to type another.
//
// A tunnel of one of these may still be given a port of its own. The daemon
// stays where it is and the port is redirected to it, so the built-in
// clients keep the standard port and the clients that take a port -- the
// strongSwan app, Linux, routers that ask -- can use the other, where the
// standard one is blocked.
func Standard(p model.Protocol) (int, bool) {
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

// Transport is "udp" or "tcp": what the daemon of a fixed-port protocol
// takes, and so what its tunnel's own port is redirected on.
func Transport(p model.Protocol) string {
	proto, _ := target(p)
	return proto
}

// target is where a tunnel's own port is sent: its daemon's port.
func target(p model.Protocol) (proto string, port int) {
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

// Redirects are the redirects a tunnel on a port of its own needs; none
// for one on its standard port, or of another protocol.
//
// IKEv2's is two. A client dialing a port that is not 500 starts as it
// would on 500, plain, and is sent to strongSwan's 500; seeing the port
// changed on the way, it takes the server for one behind NAT and moves to
// IKE with the non-ESP marker and ESP in UDP, a flow of its own, which is
// sent to 4500, where strongSwan and the kernel take those. A client that
// starts with the marker goes to 4500 at once.
func Redirects(i model.Interface) []routing.PortRedirect {
	std, ok := Standard(i.Protocol)
	if !ok || i.ListenPort == std || i.ListenPort <= 0 || i.ListenPort > 65535 {
		return nil
	}
	proto, to := target(i.Protocol)
	out := []routing.PortRedirect{{Proto: proto, From: uint16(i.ListenPort), To: uint16(to)}}
	if i.Protocol == model.ProtocolIKEv2 {
		out = append(out, routing.PortRedirect{Proto: "udp", From: uint16(i.ListenPort), To: l2tpconf.PortNATT, Marked: true})
	}
	return out
}
