//go:build !linux

package linkmtu

// Uplink is Default on a system the panel does not serve tunnels from.
func Uplink() (string, int) { return "", Default }
