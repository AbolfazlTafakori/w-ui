//go:build linux

package dnsproxy

import (
	"syscall"
)

// markControl stamps the socket with a routing mark, so the kernel sends
// the query the way it sends customer traffic -- through the default
// outbound when there is one.
func markControl(mark uint32) func(network, address string, c syscall.RawConn) error {
	if mark == 0 {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(mark))
		})
		if err != nil {
			return err
		}
		return serr
	}
}
