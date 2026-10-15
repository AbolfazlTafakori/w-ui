//go:build !linux

package fwsync

// Active is Linux's; elsewhere there is no firewall of the server's to open.
func Active() []Firewall { return nil }
