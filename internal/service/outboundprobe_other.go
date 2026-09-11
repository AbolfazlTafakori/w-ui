//go:build !linux

package service

import (
	"errors"
	"net"
)

// markedDialer needs a Linux socket mark; elsewhere there is no hop to route
// into either.
func markedDialer(*net.Dialer, uint32) (*net.Dialer, error) {
	return nil, errors.New("a WireGuard hop can only be probed on Linux")
}
