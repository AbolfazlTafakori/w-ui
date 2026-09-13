//go:build !linux

package dnsproxy

import "syscall"

func markControl(uint32) func(network, address string, c syscall.RawConn) error { return nil }
