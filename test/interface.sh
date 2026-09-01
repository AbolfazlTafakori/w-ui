#!/usr/bin/env bash
set -e
export WUI_LISTEN=127.0.0.1:2096 WUI_DATA_DIR=/tmp/d WUI_DB_SOURCE=/tmp/d/wui.db WUI_COLLECT_INTERVAL=2s
rm -rf /tmp/d && mkdir -p /tmp/d

/src/bin/wui-linux-amd64 > /tmp/panel.log 2>&1 &
sleep 5
PW=$(grep -E '^ *password ' /tmp/panel.log | awk '{print $2}')
TOK=$(curl -fsS -X POST http://127.0.0.1:2096/api/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" | grep -oE '"token":"[^"]+' | cut -d'"' -f4)
H="Authorization: Bearer $TOK"

echo "===== 1. create a WireGuard interface through the API ====="
curl -fsS -X POST http://127.0.0.1:2096/api/interfaces -H "$H" -H 'Content-Type: application/json' \
  -d '{"name":"wg0","protocol":"wireguard","listenPort":51820,"subnet":"10.66.0.0/16","endpointHost":"vpn.test","mtu":1420,"dns":"1.1.1.1","natInterface":"eth0","mode":"standard"}' >/dev/null
echo "  interface created in the panel"

echo
echo "===== 2. restart so the driver opens it ====="
kill %1 2>/dev/null || true
sleep 1
/src/bin/wui-linux-amd64 > /tmp/panel2.log 2>&1 &
sleep 6
grep -E "wireguard interface ready|created interface|driver open|no driver" /tmp/panel2.log | head -5 || true

echo
echo "===== 3. did a real kernel interface appear? ====="
ip -brief link show wg0 || echo "  NO INTERFACE"
ip -brief addr show wg0 | head -2
echo "  link type: $(ip -d link show wg0 | grep -oE 'wireguard' | head -1)"

echo
echo "===== 4. add two clients (3 devices total) ====="
TOK=$(curl -fsS -X POST http://127.0.0.1:2096/api/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" | grep -oE '"token":"[^"]+' | cut -d'"' -f4)
H="Authorization: Bearer $TOK"
curl -fsS -X POST http://127.0.0.1:2096/api/clients -H "$H" -H 'Content-Type: application/json' \
  -d '{"name":"Ali","interfaceId":1,"quotaBytes":1073741824,"deviceLimit":2,"resetCycle":"none","deviceNames":["iPhone","Laptop"]}' >/dev/null
curl -fsS -X POST http://127.0.0.1:2096/api/clients -H "$H" -H 'Content-Type: application/json' \
  -d '{"name":"Sara","interfaceId":1,"quotaBytes":10485760,"deviceLimit":1,"resetCycle":"none","deviceNames":["PC"]}' >/dev/null
sleep 6

echo "===== 5. ARE THE PEERS ACTUALLY IN THE KERNEL? ====="
wg show wg0

echo
echo "===== 6. disable one client, check the peer leaves ====="
BEFORE=$(wg show wg0 peers | wc -l)
curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/2 -H "$H" -H 'Content-Type: application/json' \
  -d '{"status":"disabled"}' >/dev/null
sleep 6
AFTER=$(wg show wg0 peers | wc -l)
echo "  peers before disable: $BEFORE"
echo "  peers after  disable: $AFTER"

echo
echo "===== 7. re-enable, check it comes back ====="
curl -fsS -X PATCH http://127.0.0.1:2096/api/clients/2 -H "$H" -H 'Content-Type: application/json' \
  -d '{"status":"active"}' >/dev/null
sleep 6
echo "  peers after re-enable: $(wg show wg0 peers | wc -l)"

echo
echo "===== 8. client config the panel hands out ====="
curl -fsS "http://127.0.0.1:2096/api/devices/1/profile?download=1" -H "$H"

echo
echo "===== 9. does the server public key in it match the kernel? ====="
KERNEL_PUB=$(wg show wg0 public-key)
CONF_PUB=$(curl -fsS "http://127.0.0.1:2096/api/devices/1/profile?download=1" -H "$H" | grep '^PublicKey' | awk '{print $3}')
echo "  kernel: $KERNEL_PUB"
echo "  config: $CONF_PUB"
[ "$KERNEL_PUB" = "$CONF_PUB" ] && echo "  MATCH - the customer would reach this server" || echo "  MISMATCH - the tunnel would never come up"

echo
echo "===== 10. reconciler ====="
curl -fsS http://127.0.0.1:2096/api/system -H "$H" | grep -oE '"reconciler":\{[^}]*\}'
grep -E "reconcile failed|error" /tmp/panel2.log | tail -3 || echo "  no reconcile errors"
