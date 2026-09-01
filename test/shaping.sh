#!/usr/bin/env bash
# Does a rate limit actually slow a customer down?
#
#   [client ns] --wg--> [root ns: panel + wg0] --nat--> [inet ns: web server]
#
set -u
export WUI_LISTEN=127.0.0.1:2096 WUI_DATA_DIR=/tmp/d WUI_DB_SOURCE=/tmp/d/wui.db WUI_COLLECT_INTERVAL=2s
rm -rf /tmp/d; mkdir -p /tmp/d
sysctl -qw net.ipv4.ip_forward=1

ip netns add inet
ip link add v-in type veth peer name v-in-p
ip link set v-in-p netns inet
ip addr add 192.168.99.1/24 dev v-in; ip link set v-in up
ip netns exec inet ip addr add 192.168.99.2/24 dev v-in-p
ip netns exec inet ip link set v-in-p up; ip netns exec inet ip link set lo up
ip netns exec inet ip route add default via 192.168.99.1
head -c 30000000 /dev/urandom > /tmp/blob.bin
ip netns exec inet bash -c 'cd /tmp && python3 -m http.server 8080 >/dev/null 2>&1 &'

ip netns add client
ip link add v-cl type veth peer name v-cl-p
ip link set v-cl-p netns client
ip addr add 192.168.50.1/24 dev v-cl; ip link set v-cl up
ip netns exec client ip addr add 192.168.50.2/24 dev v-cl-p
ip netns exec client ip link set v-cl-p up; ip netns exec client ip link set lo up

/src/bin/wui-linux-amd64 > /tmp/p.log 2>&1 &
PANEL=$!
sleep 5
PW=$(grep -E '^ +password ' /tmp/p.log | awk '{print $2}')
login() { curl -fsS -X POST http://127.0.0.1:2096/api/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" | grep -oE '"token":"[^"]+' | cut -d'"' -f4; }
H="Authorization: Bearer $(login)"

curl -fsS -X POST http://127.0.0.1:2096/api/interfaces -H "$H" -H 'Content-Type: application/json' \
 -d '{"name":"wg0","protocol":"wireguard","listenPort":51820,"subnet":"10.66.0.0/16","endpointHost":"192.168.50.1","mtu":1420,"dns":"1.1.1.1","natInterface":"v-in","mode":"standard"}' >/dev/null
kill $PANEL 2>/dev/null; wait $PANEL 2>/dev/null; sleep 1
/src/bin/wui-linux-amd64 > /tmp/p2.log 2>&1 &
PANEL=$!
sleep 6
H="Authorization: Bearer $(login)"

# Unlimited to begin with, so there is a baseline to compare against.
curl -fsS -X POST http://127.0.0.1:2096/api/clients -H "$H" -H 'Content-Type: application/json' \
 -d '{"name":"Ali","interfaceId":1,"deviceLimit":1,"resetCycle":"none","deviceNames":["Laptop"]}' >/dev/null
sleep 5

nft add table ip nat 2>/dev/null
nft add chain ip nat post '{ type nat hook postrouting priority srcnat; }' 2>/dev/null
nft add rule ip nat post oifname v-in masquerade

curl -fsS "http://127.0.0.1:2096/api/devices/1/profile?download=1" -H "$H" > /tmp/c.conf
CPRIV=$(grep '^PrivateKey' /tmp/c.conf | awk '{print $3}')
SPUB=$(grep '^PublicKey'  /tmp/c.conf | awk '{print $3}')
PSK=$(grep '^PresharedKey' /tmp/c.conf | awk '{print $3}')
CADDR=$(grep '^Address' /tmp/c.conf | awk '{print $3}')
echo "$CPRIV" > /tmp/k; echo "$PSK" > /tmp/psk
ip netns exec client ip link add wgc type wireguard
ip netns exec client ip addr add "$CADDR" dev wgc
ip netns exec client wg set wgc private-key /tmp/k peer "$SPUB" preshared-key /tmp/psk \
  endpoint 192.168.50.1:51820 allowed-ips 0.0.0.0/0
ip netns exec client ip link set wgc up
ip netns exec client ip route add 10.66.0.0/16 dev wgc
ip netns exec client ip route add 192.168.99.0/24 dev wgc
ip netns exec client ping -c2 -W3 10.66.0.1 >/dev/null 2>&1
sleep 3

speed() { ip netns exec client curl -s --max-time 40 -o /dev/null -w '%{speed_download}' \
  http://192.168.99.2:8080/blob.bin; }

echo
echo "===== 1. BASELINE (no limit) ====="
B=$(speed); printf "  %.1f Mbit/s\n" "$(echo "$B*8/1000000" | bc -l)"
echo "  tc classes on wg0: $(tc class show dev wg0 2>/dev/null | grep -c htb)"

echo
echo "===== 2. APPLY A 10 Mbit/s LIMIT ====="
curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/1 -H "$H" -H 'Content-Type: application/json' \
  -d '{"rateBitsPerSec":10000000}' >/dev/null
sleep 6
echo "  panel log:"
grep -E "shaping updated|rate limits" /tmp/p2.log | tail -2 | sed 's/^/    /'
echo "  nft stamp:"
nft list table inet wui 2>/dev/null | grep -E "priority" | sed 's/^/    /'
echo "  tc on wg0:"
tc -s class show dev wg0 2>/dev/null | grep -E "^class htb" | sed 's/^/    /'

echo
echo "===== 3. MEASURED SPEED UNDER THE 10 Mbit/s LIMIT ====="
L=$(speed); printf "  %.1f Mbit/s\n" "$(echo "$L*8/1000000" | bc -l)"

echo
echo "===== 4. TIGHTEN TO 2 Mbit/s ====="
curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/1 -H "$H" -H 'Content-Type: application/json' \
  -d '{"rateBitsPerSec":2000000}' >/dev/null
sleep 6
tc class show dev wg0 2>/dev/null | grep -E "^class htb 1:1 " | sed 's/^/    /'
T=$(speed); printf "  %.1f Mbit/s\n" "$(echo "$T*8/1000000" | bc -l)"

echo
echo "===== 5. REMOVE THE LIMIT ====="
curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/1 -H "$H" -H 'Content-Type: application/json' \
  -d '{"rateBitsPerSec":0}' >/dev/null
sleep 6
echo "  classes left (1 = only the default): $(tc class show dev wg0 2>/dev/null | grep -c '^class htb')"
R=$(speed); printf "  %.1f Mbit/s\n" "$(echo "$R*8/1000000" | bc -l)"

echo
echo "===== VERDICT ====="
printf "  baseline    %8.1f Mbit/s\n" "$(echo "$B*8/1000000" | bc -l)"
printf "  10 Mbit cap %8.1f Mbit/s\n" "$(echo "$L*8/1000000" | bc -l)"
printf "  2 Mbit cap  %8.1f Mbit/s\n" "$(echo "$T*8/1000000" | bc -l)"
printf "  uncapped    %8.1f Mbit/s\n" "$(echo "$R*8/1000000" | bc -l)"
