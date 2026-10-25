//go:build linux

package linkmtu

import (
	"net"
	"os"
	"strings"
)

// Uplink is the device this server's default route leaves by, and its MTU.
// With no default route to read, the name is empty and the MTU Default.
func Uplink() (string, int) {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", Default
	}
	return fromRoutes(string(b), func(name string) int {
		i, err := net.InterfaceByName(name)
		if err != nil {
			return 0
		}
		return i.MTU
	})
}

// fromRoutes reads /proc/net/route: the device of the first default route
// (destination and mask both 0) that is up, and the MTU mtuOf gives it.
func fromRoutes(table string, mtuOf func(string) int) (string, int) {
	for i, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 {
			continue
		}
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		if f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		if m := mtuOf(f[0]); m >= Lowest {
			return f[0], m
		}
	}
	return "", Default
}
