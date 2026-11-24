//go:build linux

package linkmtu

import "testing"

func TestTheDefaultRoutesDeviceIsTheUplink(t *testing.T) {
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wg0\t0000420A\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		"ens3\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	mtus := map[string]int{"ens3": 1450, "wg0": 1420}
	name, mtu := fromRoutes(table, func(n string) int { return mtus[n] })
	if name != "ens3" || mtu != 1450 {
		t.Fatalf("uplink = %s %d, want ens3 1450", name, mtu)
	}
	if _, mtu := fromRoutes("Iface\n", func(string) int { return 0 }); mtu != Default {
		t.Fatalf("no default route: %d, want %d", mtu, Default)
	}
}

// A jumbo-frame uplink -- 9001 on AWS and Azure -- is taken as an Ethernet
// link's: the internet between the server and a customer carries no more.
func TestAJumboFrameUplinkIsTakenAsEthernet(t *testing.T) {
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	if name, mtu := fromRoutes(table, func(string) int { return 9001 }); name != "eth0" || mtu != Default {
		t.Fatalf("uplink = %s %d, want eth0 %d", name, mtu, Default)
	}
}
