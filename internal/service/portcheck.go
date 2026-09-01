package service

import (
	"fmt"
	"net"
	"strings"
)

// Checking that a port is free before claiming it.
//
// This panel is rarely the only thing on a server. A tunnel configured on a
// port something else already holds does not fail loudly: the interface is
// created, the panel reports it as configured, and customers simply cannot
// connect — with nothing in the panel to say why. Finding out at the moment the
// port is chosen turns a silent outage into a sentence.
//
// The check is a hint, not a lock. Something can bind the port a moment later,
// and a port held by a process that is currently stopped looks free. It catches
// the ordinary case, which is a port already in use right now.

// portInUse reports whether anything is listening on a UDP or TCP port.
func portInUse(port int, transport string) (bool, string) {
	network := "udp"
	if strings.EqualFold(transport, "tcp") {
		network = "tcp"
	}

	// Bound to every address rather than one: a service listening only on the
	// public address would still take the port from a tunnel that wants all of
	// them, and binding to a single address would not notice.
	addr := fmt.Sprintf(":%d", port)

	if network == "tcp" {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return true, err.Error()
		}
		_ = l.Close()
		return false, ""
	}

	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return true, err.Error()
	}
	_ = c.Close()
	return false, ""
}

// checkPortFree returns a readable error when a port is already taken.
func checkPortFree(port int, transport string) error {
	inUse, reason := portInUse(port, transport)
	if !inUse {
		return nil
	}
	return fmt.Errorf("%w: port %d is already in use on this server (%s). "+
		"Something else is listening there — another VPN, or another program. "+
		"Choose a different port",
		ErrInvalid, port, reason)
}
