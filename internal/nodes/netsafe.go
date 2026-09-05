package nodes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// Refusing to carry the panel's credentials to an address on its own network.
//
// A node's address is typed by an operator, and every request to it carries a
// bearer token that is full access to a panel. Nothing stopped that address
// being one inside the server: 127.0.0.1, the 10.x network the machine sits on,
// or 169.254.169.254 — the address a cloud instance answers its own metadata
// on, which on most providers hands out credentials to whatever asks.
//
// The realistic way that happens is not malice. It is a typo, or a node moved
// behind a proxy and the old address left in place, and the result is the
// panel's token posted to whatever is listening there.
//
// So it is checked, and checked after the name is resolved rather than before.
// Looking at the text of a hostname proves nothing: a name an operator does not
// control can resolve to a loopback address, and it is the address the
// connection actually goes to that matters.
//
// A node genuinely on a private network — two machines on the same VPN, which
// is a sensible way to run this — says so on the node, and then it is allowed.

// ErrPrivateAddress is returned when a node resolves somewhere it should not.
var ErrPrivateAddress = errors.New("that address is inside this server's own network")

// blocked reports whether an address is one the panel should not be sending
// credentials to unless told otherwise.
func blocked(ip net.IP) bool {
	return ip == nil ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// guardedDial builds a dialer that resolves first and refuses a blocked result.
func guardedDial(allowPrivate bool, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if allowPrivate {
			return dialer.DialContext(ctx, network, addr)
		}

		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		var ips []net.IPAddr
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IPAddr{{IP: ip}}
		} else {
			if ips, err = net.DefaultResolver.LookupIPAddr(ctx, host); err != nil {
				return nil, err
			}
		}

		var lastErr, refused error
		for _, a := range ips {
			if blocked(a.IP) {
				// Kept rather than returned immediately: a name can resolve to
				// several addresses, and one of them being private does not
				// mean the others are.
				refused = fmt.Errorf("%w (%s resolves to %s)", ErrPrivateAddress, host, a.IP)
				continue
			}
			// Dialled by address, not by name, so what was checked is what is
			// connected to. Resolving twice would leave room for the answer to
			// change in between.
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}

		if refused != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("%w; %w", refused, lastErr)
			}
			return nil, refused
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no usable address for %s", host)
	}
}

// checkPublic resolves a host and refuses it if it lands inside this server's
// own network. Used where a connection is made without the guarded dialer.
func checkPublic(hostPort string) error {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
	}
	if ip := net.ParseIP(host); ip != nil {
		if blocked(ip) {
			return fmt.Errorf("%w (%s)", ErrPrivateAddress, ip)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, a := range ips {
		if !blocked(a.IP) {
			return nil
		}
	}
	return fmt.Errorf("%w (%s)", ErrPrivateAddress, host)
}
