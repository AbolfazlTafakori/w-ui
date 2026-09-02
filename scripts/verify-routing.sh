#!/bin/sh
# Does a marked packet actually leave through the hop?
#
# Topology: a "customer" namespace behind the server, and two exits from the
# server -- ISP (direct) and HOP. A packet is marked, and we check which exit
# it came out of by looking at counters on each exit interface.
set -e
apk add --no-cache nftables iproute2 iputils >/dev/null 2>&1

ip netns add cust; ip netns add isp; ip netns add hop

# server <-> customer
ip link add s-cust type veth peer name c-srv
ip link set c-srv netns cust
ip addr add 10.66.0.1/24 dev s-cust; ip link set s-cust up
ip netns exec cust ip addr add 10.66.0.5/24 dev c-srv
ip netns exec cust ip link set c-srv up
ip netns exec cust ip route add default via 10.66.0.1

# server <-> isp (the "direct" exit)
ip link add s-isp type veth peer name i-srv
ip link set i-srv netns isp
ip addr add 192.0.2.1/24 dev s-isp; ip link set s-isp up
ip netns exec isp ip addr add 192.0.2.2/24 dev i-srv
ip netns exec isp ip link set i-srv up
ip netns exec isp ip route add default via 192.0.2.1

# server <-> hop (the second-hop exit)
ip link add s-hop type veth peer name h-srv
ip link set h-srv netns hop
ip addr add 198.51.100.1/24 dev s-hop; ip link set s-hop up
ip netns exec hop ip addr add 198.51.100.2/24 dev h-srv
ip netns exec hop ip link set h-srv up
ip netns exec hop ip route add default via 198.51.100.1

sysctl -qw net.ipv4.ip_forward=1
# default route for the server = the ISP
ip route replace default via 192.0.2.2

# The destination we will probe lives in both exits, so the only thing that
# decides which one answers is the routing table the mark selects.
ip netns exec isp ip addr add 203.0.113.9/32 dev lo
ip netns exec hop ip addr add 203.0.113.9/32 dev lo
ip netns exec isp ip link set lo up
ip netns exec hop ip link set lo up
ip route replace 203.0.113.9/32 via 192.0.2.2          # main table -> ISP
ip route replace 203.0.113.9/32 via 198.51.100.2 table 47001  # hop table

echo "=== policy: mark customer traffic for the hop ==="
cat >/tmp/p.nft <<'NFT'
add table inet wui_policy
delete table inet wui_policy
table inet wui_policy {
	set customers4 { type ipv4_addr; flags interval; elements = { 10.66.0.0/24 } }
	chain wui_steer {
		type filter hook prerouting priority -150; policy accept;
		ip saddr != @customers4 accept
		ct mark and 0x00ff0000 == 0x00a70000 meta mark set ct mark accept
		meta mark set 0x00a70001 ct mark set meta mark
	}
}
NFT
nft -f /tmp/p.nft
ip rule add fwmark 0x00a70001 table 47001 priority 20001

echo "=== probe from the customer ==="
ip netns exec isp sh -c 'nft add table inet t; nft add chain inet t c "{type filter hook input priority 0;}"; nft add rule inet t c ip saddr 10.66.0.5 counter' 2>/dev/null || true
ip netns exec hop sh -c 'nft add table inet t; nft add chain inet t c "{type filter hook input priority 0;}"; nft add rule inet t c ip saddr 10.66.0.5 counter' 2>/dev/null || true

ip netns exec cust ping -c 3 -W 1 203.0.113.9 >/dev/null 2>&1 || true

ISP_PKTS=$(ip netns exec isp nft list table inet t | grep -o 'packets [0-9]*' | head -1 | awk '{print $2}')
HOP_PKTS=$(ip netns exec hop nft list table inet t | grep -o 'packets [0-9]*' | head -1 | awk '{print $2}')
echo "packets that reached the ISP exit : ${ISP_PKTS:-0}"
echo "packets that reached the HOP exit : ${HOP_PKTS:-0}"

echo
echo "=== now disable the hop: the rule goes, traffic must fall back to the ISP ==="
ip rule del fwmark 0x00a70001 table 47001 2>/dev/null || true
ip netns exec isp nft delete table inet t; ip netns exec hop nft delete table inet t
ip netns exec isp sh -c 'nft add table inet t; nft add chain inet t c "{type filter hook input priority 0;}"; nft add rule inet t c ip saddr 10.66.0.5 counter'
ip netns exec hop sh -c 'nft add table inet t; nft add chain inet t c "{type filter hook input priority 0;}"; nft add rule inet t c ip saddr 10.66.0.5 counter'
ip netns exec cust ping -c 3 -W 1 203.0.113.9 >/dev/null 2>&1 || true
ISP2=$(ip netns exec isp nft list table inet t | grep -o 'packets [0-9]*' | head -1 | awk '{print $2}')
HOP2=$(ip netns exec hop nft list table inet t | grep -o 'packets [0-9]*' | head -1 | awk '{print $2}')
echo "packets that reached the ISP exit : ${ISP2:-0}"
echo "packets that reached the HOP exit : ${HOP2:-0}"
