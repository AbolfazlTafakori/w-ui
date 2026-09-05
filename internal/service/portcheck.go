package service

import (
	"errors"
	"fmt"
	"net"
	"os"
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

// portStatus is what an attempt to bind the port found.
type portStatus int

const (
	portFree portStatus = iota
	portTaken
	portNotPermitted
)

// probePort reports whether anything is listening on a UDP or TCP port.
//
// A refusal is not always a busy port. The panel runs unprivileged, so
// anything below 1024 comes back denied whether or not it is free, and the two
// have to be told apart: 443 is the most useful port a tunnel can have on a
// network that blocks by port, and being told it is "already in use" sends an
// operator hunting for a program that is not there.
func probePort(port int, transport string) (portStatus, string) {
	network := "udp"
	if strings.EqualFold(transport, "tcp") {
		network = "tcp"
	}

	// Bound to every address rather than one: a service listening only on the
	// public address would still take the port from a tunnel that wants all of
	// them, and binding to a single address would not notice.
	addr := fmt.Sprintf(":%d", port)

	var err error
	if network == "tcp" {
		var l net.Listener
		if l, err = net.Listen("tcp", addr); err == nil {
			_ = l.Close()
		}
	} else {
		var c net.PacketConn
		if c, err = net.ListenPacket("udp", addr); err == nil {
			_ = c.Close()
		}
	}

	switch {
	case err == nil:
		return portFree, ""
	case errors.Is(err, os.ErrPermission):
		return portNotPermitted, err.Error()
	default:
		return portTaken, err.Error()
	}
}

// checkPortFree returns a readable error when a port cannot be claimed.
func checkPortFree(port int, transport string) error {
	switch status, reason := probePort(port, transport); status {
	case portFree:
		return nil
	case portNotPermitted:
		// Said as the thing to fix rather than as a refusal. A tunnel on 443 is
		// often the only one that gets through, and one line in the service
		// file is all that stands in the way.
		return fmt.Errorf("%w: the panel is not allowed to bind port %d (%s). "+
			"Ports below 1024 need CAP_NET_BIND_SERVICE, which this service does "+
			"not have. Add it to the panel's systemd unit and restart, or choose "+
			"a port above 1024",
			ErrInvalid, port, reason)
	default:
		return fmt.Errorf("%w: port %d is already in use on this server (%s). "+
			"Something else is listening there — another VPN, or another program. "+
			"Choose a different port",
			ErrInvalid, port, reason)
	}
}
