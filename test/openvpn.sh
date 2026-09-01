#!/usr/bin/env bash
# End-to-end OpenVPN: a real client logging in with a username and password.
#
#   [client ns] --ovpn--> [root ns: panel + tun0] --nat--> [inet ns: web server]
#
set -u
export WUI_LISTEN=127.0.0.1:2096 WUI_DATA_DIR=/tmp/d WUI_DB_SOURCE=/tmp/d/wui.db WUI_COLLECT_INTERVAL=2s
rm -rf /tmp/d; mkdir -p /tmp/d
sysctl -qw net.ipv4.ip_forward=1
modprobe tun 2>/dev/null || true

# --- the "internet" the customer is buying access to -------------------------
ip netns add inet
ip link add v-in type veth peer name v-in-p
ip link set v-in-p netns inet
ip addr add 192.168.99.1/24 dev v-in; ip link set v-in up
ip netns exec inet ip addr add 192.168.99.2/24 dev v-in-p
ip netns exec inet ip link set v-in-p up
ip netns exec inet ip link set lo up
ip netns exec inet ip route add default via 192.168.99.1
head -c 20000000 /dev/urandom > /tmp/blob.bin
ip netns exec inet bash -c 'cd /tmp && python3 -m http.server 8080 >/dev/null 2>&1 &'

# --- the transport the customer's packets arrive over ------------------------
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
PW=$(grep -E '^ +password ' /tmp/p.log | awk '{print $2}')
login() { curl -fsS -X POST http://127.0.0.1:2096/api/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" | grep -oE '"token":"[^"]+' | cut -d'"' -f4; }
H="Authorization: Bearer $(login)"

curl -fsS -X POST http://127.0.0.1:2096/api/interfaces -H "$H" -H 'Content-Type: application/json' \
 -d '{"name":"tun0","protocol":"openvpn","listenPort":1194,"subnet":"10.67.0.0/16","endpointHost":"192.168.50.1","mtu":1500,"dns":"1.1.1.1","natInterface":"v-in","mode":"standard"}' >/dev/null \
 && echo "interface created" || { echo "INTERFACE CREATE FAILED"; tail -5 /tmp/p.log; exit 1; }

kill $PANEL 2>/dev/null; wait $PANEL 2>/dev/null; sleep 1
/src/bin/wui-linux-amd64 > /tmp/p2.log 2>&1 &
PANEL=$!
sleep 8
H="Authorization: Bearer $(login)"

echo
echo "===== 1. DID THE PANEL START A REAL OPENVPN SERVER? ====="
grep -E "openvpn interface ready|started openvpn|adopted" /tmp/p2.log | head -3
echo "  process:   $(pgrep -a openvpn | head -1 || echo 'NOT RUNNING')"
echo "  tun link:  $(ip -brief link show tun0 2>/dev/null || echo 'NONE')"
echo "  listening: $(ss -lunp 2>/dev/null | grep -c 1194) socket(s) on 1194"

echo
echo "===== 2. CREATE A CUSTOMER (20 MB quota, 1 device) ====="
curl -fsS -X POST http://127.0.0.1:2096/api/clients -H "$H" -H 'Content-Type: application/json' \
 -d '{"name":"Ali","interfaceId":1,"quotaBytes":20971520,"deviceLimit":1,"resetCycle":"none","deviceNames":["Laptop"]}' >/dev/null
sleep 5

curl -fsS "http://127.0.0.1:2096/api/devices/1/profile?download=1" -H "$H" > /tmp/client.ovpn
USER=$(curl -fsS "http://127.0.0.1:2096/api/devices/1/profile" -H "$H" | grep -oE '"username":"[^"]+' | cut -d'"' -f4)
PASS=$(curl -fsS "http://127.0.0.1:2096/api/devices/1/profile" -H "$H" | grep -oE '"secret":"[^"]+' | cut -d'"' -f4)
echo "  username: $USER"
echo "  password: $PASS"
echo "  server files:"
ls -l /tmp/d/openvpn/tun0/ 2>/dev/null | awk 'NR>1 {printf "    %s %s\n", $1, $9}'
echo "  credentials file: $(cat /tmp/d/openvpn/tun0/credentials 2>/dev/null)"
echo "  pinned address:   $(cat /tmp/d/openvpn/tun0/ccd/$USER 2>/dev/null)"

# NAT so the tunnel actually reaches the "internet".
nft add table ip nat 2>/dev/null
nft add chain ip nat post '{ type nat hook postrouting priority srcnat; }' 2>/dev/null
nft add rule ip nat post oifname v-in masquerade

echo
echo "===== 3. DOES A REAL CLIENT LOG IN WITH THAT USERNAME AND PASSWORD? ====="
printf '%s\n%s\n' "$USER" "$PASS" > /tmp/creds.txt
chmod 600 /tmp/creds.txt
sed -i 's|^auth-user-pass$|auth-user-pass /tmp/creds.txt|' /tmp/client.ovpn
ip netns exec client openvpn --config /tmp/client.ovpn --log /tmp/client.log --daemon
sleep 12

if grep -q "Initialization Sequence Completed" /tmp/client.log; then
  echo "  CONNECTED"
else
  echo "  NOT CONNECTED"
fi
echo "  client tunnel: $(ip netns exec client ip -brief addr show tun0 2>/dev/null || echo none)"
grep -E "AUTH_FAILED|Peer Connection Initiated|VERIFY (OK|ERROR)" /tmp/client.log | tail -3

echo
echo "===== 4. DID IT GET THE ADDRESS THE PANEL PINNED? ====="
ASSIGNED=$(ip netns exec client ip -4 -brief addr show tun0 2>/dev/null | awk '{print $3}' | cut -d/ -f1)
WANTED=$(awk '{print $2}' /tmp/d/openvpn/tun0/ccd/$USER 2>/dev/null)
echo "  pinned by panel: $WANTED"
echo "  client received: $ASSIGNED"
[ -n "$ASSIGNED" ] && [ "$ASSIGNED" = "$WANTED" ] && echo "  MATCH - quota accounting will follow this customer" || echo "  MISMATCH"

echo
echo "===== 5. DOES TRAFFIC REACH THE INTERNET THROUGH THE TUNNEL? ====="
ip netns exec client curl -s --max-time 25 -o /dev/null -w "  fetched %{size_download} bytes at %{speed_download} B/s\n" \
  http://192.168.99.2:8080/blob.bin || echo "  FETCH FAILED"

echo
echo "===== 6. IS IT BILLED TO THE RIGHT CUSTOMER? ====="
sleep 10
echo "--- enforcement state ---"
curl -fsS http://127.0.0.1:2096/api/system -H "$H" | grep -oE '"enforcement[A-Za-z]*":("[^"]*"|[a-z]+)'
curl -fsS http://127.0.0.1:2096/api/system -H "$H" | grep -oE '"reconciler":\{[^}]*\}'
echo "--- nft ruleset ---"
nft list table inet wui 2>&1 | head -30
echo "--- packet path ---"
echo "  routes: $(ip route get 192.168.99.2 from 10.67.0.2 iif tun0 2>&1 | head -1)"
curl -fsS http://127.0.0.1:2096/api/clients -H "$H" | grep -oE '"usedBytes":[0-9]+' | head -1
echo "  panel sees the session as:"
curl -fsS http://127.0.0.1:2096/api/clients -H "$H" | grep -oE '"status":"[a-z]+"' | head -1

echo
echo "===== 7. A SECOND LOGIN ON THE SAME CREDENTIAL EVICTS THE FIRST ====="
echo "  session before: $(awk -F, '/^CLIENT_LIST/ {print $2" from "$3}' /tmp/d/openvpn/tun0/status)"
ip netns add client2
ip link add v-c2 type veth peer name v-c2-p
ip link set v-c2-p netns client2
ip addr add 192.168.51.1/24 dev v-c2; ip link set v-c2 up
ip netns exec client2 ip addr add 192.168.51.2/24 dev v-c2-p
ip netns exec client2 ip link set v-c2-p up
ip netns exec client2 ip link set lo up
sed 's|^remote .*|remote 192.168.51.1 1194|' /tmp/client.ovpn > /tmp/client2.ovpn
ip netns exec client2 openvpn --config /tmp/client2.ovpn --log /tmp/client2.log --daemon
sleep 14
grep -q "Initialization Sequence Completed" /tmp/client2.log && echo "  second device connected: yes" || echo "  second device connected: no"
echo "  session after:  $(awk -F, '/^CLIENT_LIST/ {print $2" from "$3}' /tmp/d/openvpn/tun0/status)"
echo "  concurrent sessions: $(grep -c '^CLIENT_LIST' /tmp/d/openvpn/tun0/status 2>/dev/null)  (1 = one credential cannot serve two people)"

echo
echo "===== 8. CUTTING THE CUSTOMER OFF ====="
echo "  live session before: $(awk -F, '/^CLIENT_LIST/ {print $2" from "$3}' /tmp/d/openvpn/tun0/status)"
curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/1 -H "$H" -H 'Content-Type: application/json'   -d '{"status":"disabled"}' >/dev/null
sleep 6
echo "  credentials file now: '$(cat /tmp/d/openvpn/tun0/credentials 2>/dev/null)'"
echo "  address files left:   $(ls /tmp/d/openvpn/tun0/ccd/ 2>/dev/null | wc -l)"

# The connected device is the second one. Whether IT gets dropped is the real
# question: a customer who is out of data must stop, not keep the session they
# already have.
echo "  did the LIVE device get dropped? $(grep -cE 'SIGTERM|SIGUSR1|Connection reset|restarting' /tmp/client2.log) event(s) in its log"
ip netns exec client2 curl -s --max-time 10 -o /dev/null -w "  bytes the live device can still pull: %{size_download}
"   http://192.168.99.2:8080/blob.bin || echo "  bytes the live device can still pull: 0 (blocked)"

# The status file is rewritten on a timer, so give it one cycle before reading.
sleep 12
echo "  sessions on the server now: $(grep -c '^CLIENT_LIST' /tmp/d/openvpn/tun0/status 2>/dev/null)  (0 = disconnected)"
echo "  can they log back in? $(ip netns exec client2 timeout 15 openvpn --config /tmp/client2.ovpn --log /tmp/client3.log 2>/dev/null; grep -qE 'AUTH_FAILED|auth-failure' /tmp/client3.log && echo 'no - AUTH_FAILED' || echo 'YES - THE CREDENTIAL STILL WORKS')"

echo
echo "===== 9. DOES THE SERVER SURVIVE A PANEL RESTART? ====="
OLDPID=$(pgrep openvpn | head -1)
kill $PANEL 2>/dev/null; wait $PANEL 2>/dev/null; sleep 2
echo "  openvpn still running after panel exit: $(pgrep -c openvpn 2>/dev/null || echo 0)"
/src/bin/wui-linux-amd64 > /tmp/p3.log 2>&1 &
PANEL=$!
sleep 8
NEWPID=$(pgrep openvpn | head -1)
echo "  pid before restart: $OLDPID"
echo "  pid after  restart: $NEWPID"
[ -n "$OLDPID" ] && [ "$OLDPID" = "$NEWPID" ] && echo "  ADOPTED - no customer was disconnected" || echo "  RESTARTED - customers were dropped"
grep -E "adopted|started openvpn" /tmp/p3.log | head -2
