// Package nftstate reads what is actually in the kernel, for the parts of the
// panel that write to it and skip writing when nothing has changed.
//
// Skipping is right: a steady server should not reload its firewall every two
// seconds. But it assumes that what was written is still there, and the panel
// is not alone on the machine. A firewall reload that begins with
// `flush ruleset` -- Debian's own /etc/nftables.conf does, so a plain
// `systemctl restart nftables` is enough -- or another VPN resetting the
// routing rules takes the panel's away without a word. Every customer limit,
// every disabled customer and every route out of the tunnels went with them,
// and the panel went on believing they were in place until something else
// changed. So before skipping, each writer asks here whether its own part is
// still there.
package nftstate

import (
	"fmt"
	"strings"
)

// HasTable reports whether `nft list tables` output lists the table.
func HasTable(listTables []byte, family, name string) bool {
	for _, line := range strings.Split(string(listTables), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "table" && f[1] == family && f[2] == name {
			return true
		}
	}
	return false
}

// RuleMarks reads the firewall marks `ip rule show` output routes by, as they
// are written there (0xa7000b), each with the table it looks up.
func RuleMarks(ipRuleShow []byte) map[uint32]string {
	out := map[uint32]string{}
	for _, line := range strings.Split(string(ipRuleShow), "\n") {
		// "20011:	from all fwmark 0xa7000b lookup 47011"
		f := strings.Fields(line)
		mark, table := "", ""
		for i := 0; i+1 < len(f); i++ {
			switch f[i] {
			case "fwmark":
				mark = f[i+1]
			case "lookup":
				table = f[i+1]
			}
		}
		if mark == "" || table == "" {
			continue
		}
		var m uint64
		if _, err := fmt.Sscanf(strings.SplitN(mark, "/", 2)[0], "0x%x", &m); err != nil {
			continue
		}
		out[uint32(m)] = table
	}
	return out
}
