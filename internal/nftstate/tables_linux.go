//go:build linux

package nftstate

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// ListTables is what `nft list tables` prints, asked of the kernel over
// netlink. The writers ask it every tick whether their table is still
// there, and the nft tool reads the whole ruleset back before it lists a
// table: on a server with ten thousand customers that was most of a second,
// each time.
func ListTables() ([]byte, error) {
	c, err := netlink.Dial(unix.NETLINK_NETFILTER, nil)
	if err != nil {
		return nil, fmt.Errorf("netlink: %w", err)
	}
	defer c.Close()
	// NFT_MSG_GETTABLE in the nf_tables subsystem, every family.
	msgs, err := c.Execute(netlink.Message{
		Header: netlink.Header{
			Type:  netlink.HeaderType(10<<8 | 1),
			Flags: netlink.Request | netlink.Dump,
		},
		Data: []byte{unix.NFPROTO_UNSPEC, 0, 0, 0},
	})
	if err != nil {
		return nil, fmt.Errorf("netlink: %w", err)
	}
	var b strings.Builder
	for _, m := range msgs {
		if len(m.Data) < 4 {
			continue
		}
		family, ok := familyNames[m.Data[0]]
		if !ok {
			continue
		}
		ad, err := netlink.NewAttributeDecoder(m.Data[4:])
		if err != nil {
			return nil, err
		}
		ad.ByteOrder = binary.BigEndian
		for ad.Next() {
			if ad.Type() == 1 { // NFTA_TABLE_NAME
				fmt.Fprintf(&b, "table %s %s\n", family, ad.String())
			}
		}
		if err := ad.Err(); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

// familyNames are the families as nft names them.
var familyNames = map[byte]string{
	unix.NFPROTO_INET: "inet", unix.NFPROTO_IPV4: "ip", unix.NFPROTO_ARP: "arp",
	unix.NFPROTO_NETDEV: "netdev", unix.NFPROTO_BRIDGE: "bridge", unix.NFPROTO_IPV6: "ip6",
}
