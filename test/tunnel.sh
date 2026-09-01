#!/usr/bin/env bash
# End-to-end: a real client, a real handshake, real forwarded traffic.
#
#   [client ns] --wg--> [root ns: panel + wg0] --nat--> [inet ns: web server]
#
set -u
export WUI_LISTEN=127.0.0.1:2096 WUI_DATA_DIR=/tmp/d WUI_DB_SOURCE=/tmp/d/wui.db WUI_COLLECT_INTERVAL=2s
rm -rf /tmp/d; mkdir -p /tmp/d
sysctl -qw net.ipv4.ip_forward=1

modprobe nft_quota 2>/dev/null
echo "kernel quota module: $(nft add table inet p 2>/dev/null; nft add quota inet p q '{ over 1000 bytes }' 2>/dev/null && echo yes || echo NO)"
nft delete table inet p 2>/dev/null

# --- the "internet" the customer is buying access to -------------------------
ip netns add inet
ip link add v-in type veth peer name v-in-p
ip link set v-in-p netns inet
ip addr add 192.168.99.1/24 dev v-in; ip link set v-in up
ip netns exec inet ip addr add 192.168.99.2/24 dev v-in-p
ip netns exec inet ip link set v-in-p up
ip netns exec inet ip link set lo up
ip netns exec inet ip route add default via 192.168.99.1
head -c 50000000 /dev/urandom > /tmp/blob.bin
ip netns exec inet bash -c 'cd /tmp && python3 -m http.server 8080 >/dev/null 2>&1 &'

# --- the transport the customer's UDP packets arrive over --------------------
ip netns add client
ip link add v-cl type veth peer name v-cl-p
ip link set v-cl-p netns client
ip addr add 192.168.50.1/24 dev v-cl; ip link set v-cl up
ip netns exec client ip addr add 192.168.50.2/24 dev v-cl-p
ip netns exec client ip link set v-cl-p up
ip netns exec client ip link set lo up

# --- the panel brings up its own interface -----------------------------------
/src/bin/wui-linux-amd64 > /tmp/p.log 2>&1 &
PANEL=$!
sleep 5
PW=$(grep -E '^ *password ' /tmp/p.log | awk '{print $2}')
login() { curl -fsS -X POST http://127.0.0.1:2096/api/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" | grep -oE '"token":"[^"]+' | cut -d'"' -f4; }
H="Authorization: Bearer $(login)"

curl -fsS -X POST http://127.0.0.1:2096/api/interfaces -H "$H" -H 'Content-Type: application/json' \
 -d '{"name":"wg0","protocol":"wireguard","listenPort":51820,"subnet":"10.66.0.0/16","endpointHost":"192.168.50.1","mtu":1420,"dns":"1.1.1.1","natInterface":"v-in","mode":"standard"}' >/dev/null
kill $PANEL 2>/dev/null; wait $PANEL 2>/dev/null; sleep 1
/src/bin/wui-linux-amd64 > /tmp/p2.log 2>&1 &
PANEL=$!
sleep 6
echo "--- panel: $(grep -cE 'level=ERROR' /tmp/p2.log) errors; wg0 up: $(ip link show wg0 >/dev/null 2>&1 && echo yes || echo NO)'"''
H="Authorization: Bearer $(login)"

# One customer, 10 MB of quota, buying access to a 50 MB file.
curl -fsS -X POST http://127.0.0.1:2096/api/clients -H "$H" -H 'Content-Type: application/json' \
 -d '{"name":"Ali","interfaceId":1,"quotaBytes":10485760,"deviceLimit":1,"resetCycle":"none","deviceNames":["Laptop"]}' >/dev/null
sleep 5

# NAT so the tunnel actually reaches the "internet".
nft add table ip nat 2>/dev/null
nft add chain ip nat post '{ type nat hook postrouting priority srcnat; }' 2>/dev/null
nft add rule ip nat post oifname v-in masquerade

# --- the customer installs the config the panel gave them --------------------
curl -fsS "http://127.0.0.1:2096/api/devices/1/profile?download=1" -H "$H" > /tmp/client.conf
CPRIV=$(grep '^PrivateKey' /tmp/client.conf | awk '{print $3}')
SPUB=$(grep '^PublicKey'  /tmp/client.conf | awk '{print $3}')
PSK=$(grep '^PresharedKey' /tmp/client.conf | awk '{print $3}')
CADDR=$(grep '^Address' /tmp/client.conf | awk '{print $3}')

ip netns exec client ip link add wgc type wireguard
ip netns exec client ip addr add "$CADDR" dev wgc
echo "$CPRIV" > /tmp/cpriv; echo "$PSK" > /tmp/psk
ip netns exec client wg set wgc private-key /tmp/cpriv \
  peer "$SPUB" preshared-key /tmp/psk endpoint 192.168.50.1:51820 allowed-ips 0.0.0.0/0
ip netns exec client ip link set wgc up
ip netns exec client ip route add 10.66.0.0/16 dev wgc
ip netns exec client ip route add 192.168.99.0/24 dev wgc
sleep 2

# WireGuard handshakes lazily: nothing happens until something is sent.
ip netns exec client ping -c2 -W3 10.66.0.1 >/dev/null 2>&1
sleep 2

echo "--- key comparison ---"
echo "server pub:        $(wg show wg0 public-key)"
echo "client's peer key: $(ip netns exec client wg show wgc peers)"
echo "client pub:        $(ip netns exec client wg show wgc public-key)"
echo "server's peer key: $(wg show wg0 peers)"
echo "server psk-set:    $(wg show wg0 dump | tail -n+2 | awk '{print ($2=="(none)")?"no":"yes"}')"
echo "client psk-set:    $(ip netns exec client wg show wgc dump | tail -n+2 | awk '{print ($2=="(none)")?"no":"yes"}')"
echo "--- diagnostics ---"
echo "server peers:   $(wg show wg0 peers | tr '
' ' ')"
echo "server port:    $(wg show wg0 listen-port)"
echo "client peer:    $(ip netns exec client wg show wgc peers)"
echo "client endpoint:$(ip netns exec client wg show wgc endpoints)"
echo "udp reachable:  $(ip netns exec client ping -c1 -W2 192.168.50.1 >/dev/null 2>&1 && echo yes || echo NO)"
echo "client addr:    $(ip netns exec client ip -br addr show wgc)"
ip netns exec client ping -c2 -W3 10.66.0.1 2>&1 | tail -2

echo "--- data path ---"
echo "server transfer: $(wg show wg0 transfer)"
echo "client transfer: $(ip netns exec client wg show wgc transfer)"
echo "server routes to 10.66:  $(ip route get 10.66.0.2 2>&1 | head -1)"
echo "rp_filter all/wg0: $(sysctl -n net.ipv4.conf.all.rp_filter)/$(sysctl -n net.ipv4.conf.wg0.rp_filter 2>/dev/null)"
echo "--- full nft ruleset ---"
nft list ruleset | sed -n '1,60p'

echo
echo "===== A. DID THE HANDSHAKE COMPLETE? ====="
ip netns exec client wg show wgc latest-handshakes | awk '{print ($2>0)?"  YES - handshake at epoch "$2:"  NO - the tunnel never came up"}'

echo
echo "===== B. DOES TRAFFIC ACTUALLY REACH THE INTERNET THROUGH THE TUNNEL? ====="
ip netns exec client curl -s --max-time 10 -o /dev/null -w "  fetched %{size_download} bytes at %{speed_download} B/s\n" \
  http://192.168.99.2:8080/blob.bin || echo "  FETCH FAILED"

echo
echo "===== C. IS THAT TRAFFIC BEING BILLED TO THE RIGHT CUSTOMER? ====="
sleep 4
curl -fsS http://127.0.0.1:2096/api/clients -H "$H" | grep -oE '"name":"Ali".*?"usedBytes":[0-9]+' | grep -oE '"usedBytes":[0-9]+' | head -1
nft list table inet wui 2>/dev/null | grep -E "counter|quota" | head -8

echo
echo "===== D. DID THE PANEL CUT THEM OFF WHEN THE QUOTA RAN OUT? ====="
sleep 3
STATUS=$(curl -fsS http://127.0.0.1:2096/api/clients -H "$H" | grep -oE '"status":"[a-z]+"' | head -1)
USED=$(curl -fsS http://127.0.0.1:2096/api/clients -H "$H" | grep -oE '"usedBytes":[0-9]+' | head -1 | cut -d: -f2)
echo "  client status: $STATUS"
echo "  peers on wg0:  $(wg show wg0 peers | wc -l)   (0 = cut off)"
echo "  quota:         10485760 bytes"
echo "  actually used: $USED bytes"
echo "  OVERSHOOT:     $((USED - 10485760)) bytes  <-- poll-based only; nft_quota is absent from this kernel"

echo
echo "===== E. IS THE CUT-OFF CUSTOMER ACTUALLY BLOCKED NOW? ====="
ip netns exec client curl -s --max-time 8 -o /dev/null -w "  bytes they can still pull: %{size_download}
"   http://192.168.99.2:8080/blob.bin || echo "  bytes they can still pull: 0 (blocked)"

echo
echo "===== F. DOES TOPPING THEM UP RESTORE SERVICE? ====="
curl -fsS -X POST http://127.0.0.1:2096/api/clients/1/reset -H "$H" >/dev/null 2>&1   || curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/1 -H "$H" -H 'Content-Type: application/json'        -d '{"quotaBytes":209715200,"status":"active"}' >/dev/null
sleep 6
echo "  peers on wg0: $(wg show wg0 peers | wc -l)   (1 = restored)"
ip netns exec client curl -s --max-time 15 -o /dev/null -w "  bytes they can pull again: %{size_download}
"   http://192.168.99.2:8080/blob.bin || echo "  still blocked"
