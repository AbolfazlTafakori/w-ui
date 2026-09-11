package service

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// markedDialer returns a dialer whose sockets carry the hop's fwmark, so the
// kernel sends them into the hop's tunnel the same way it sends a customer's
// packets there. Needs CAP_NET_ADMIN, which the panel already holds to build
// the tunnel in the first place.
func markedDialer(base *net.Dialer, mark uint32) (*net.Dialer, error) {
	d := *base
	d.Control = func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark))
		})
		if err != nil {
			return err
		}
		return serr
	}
	return &d, nil
}
