package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
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
			// Cleanup on the way out: nothing is left to do if it fails, and the error that brought us here is the one reported.
			_ = l.Close()
		}
	} else {
		var c net.PacketConn
		if c, err = net.ListenPacket("udp", addr); err == nil {
			// Cleanup on the way out: nothing is left to do if it fails, and the error that brought us here is the one reported.
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

// portReleaseWait is how long removing a tunnel waits for its ports to be let
// go. Long enough for a gateway's pass and the agent's step; short enough that
// a port held by something else does not hold up the removal.
var portReleaseWait = 3 * time.Second

// tunnelPorts is every port a tunnel listens on: its own, and its stealth
// transport's.
func tunnelPorts(iface *model.Interface) []int {
	var out []int
	if iface.ListenPort > 0 {
		out = append(out, iface.ListenPort)
	}
	if p := iface.Stealth.V.Port; p > 0 && p != iface.ListenPort {
		out = append(out, p)
	}
	return out
}

// awaitPortsFree waits up to max for nothing to listen on these ports, over
// TCP or UDP. A port the panel may not bind cannot be asked about, and is not
// waited on.
func awaitPortsFree(ctx context.Context, ports []int, max time.Duration) {
	held := func() bool {
		for _, p := range ports {
			for _, network := range []string{"tcp", "udp"} {
				if st, _ := probePort(p, network); st == portTaken {
					return true
				}
			}
		}
		return false
	}
	deadline := time.Now().Add(max)
	for held() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
