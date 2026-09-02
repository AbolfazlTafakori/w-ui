#!/bin/sh
# Run the panel on a real Linux kernel and check the parts Windows cannot run.
#
# Everything the panel claims about quotas, routing and subscriptions is a claim
# about a kernel it is not running on during development. This starts it where
# those are real, creates a customer, and checks the three things that can only
# be checked here: that the nftables programs load, that the accounting chain
# for a customer appears, and that a subscription renders a configuration a
# client could actually use.
set -e

apk add --no-cache nftables iproute2 wireguard-tools curl >/dev/null 2>&1

export WUI_LISTEN=127.0.0.1:2096
export WUI_DATA_DIR=/data
export WUI_DB_SOURCE=/data/wui.db
mkdir -p /data

/work/wui-linux >/data/out.log 2>/data/err.log &
PANEL=$!
sleep 6

say() { printf '\n=== %s ===\n' "$1"; }

PW=$(awk '/^ *password /{print $2; exit}' /data/out.log)
TOKEN=$(curl -s -X POST -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PW\"}" \
  http://127.0.0.1:2096/api/auth/login | sed 's/.*"token":"\([^"]*\)".*/\1/')
[ -n "$TOKEN" ] || { echo "could not sign in"; sed -n '1,40p' /data/out.log; exit 1; }

api() { # method path [body]
  if [ -n "$3" ]; then
    curl -s -X "$1" -H 'Content-Type: application/json' \
      -H "Authorization: Bearer $TOKEN" -d "$3" "http://127.0.0.1:2096$2"
  else
    curl -s -X "$1" -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:2096$2"
  fi
}

say "what the panel says it can do on this kernel"
curl -s http://127.0.0.1:2096/api/meta | tr ',' '\n' | grep -E 'Active|Message' | cut -c1-150

say "an interface and a customer"
api POST /api/interfaces '{"name":"wg0","protocol":"wireguard","listenPort":51820,"subnet":"10.66.0.0/16","endpointHost":"vpn.example.com","mtu":1420,"enabled":true}' | cut -c1-110
api POST /api/clients '{"name":"Roya","interfaceId":1,"quotaBytes":53687091200,"deviceLimit":1,"deviceNames":["Phone"]}' | cut -c1-160
sleep 5

say "the accounting table the kernel is actually holding"
nft list table inet wui 2>/dev/null | grep -E 'chain|quota|counter |vmap|elements' | head -12 || echo "(not created)"

say "the routing policy, after blocking bogons and BitTorrent"
api PUT /api/routing '{"blockBitTorrent":true,"blockIps":["bogon"],"defaultOutbound":"direct"}' >/dev/null
sleep 4
nft list table inet wui_policy 2>/dev/null | grep -E 'chain|hook|drop|saddr !=' | head -10 || echo "(not created)"

say "a subscription, fetched the way a customer's app would"
api PUT /api/subscription '{"enabled":true,"path":"/mylink/","title":"Demo","updateHours":6}' >/dev/null
CID=$(api GET '/api/clients?limit=1' | sed 's/.*"items":\[{"id":\([0-9]*\).*/\1/')
LINK=$(api GET "/api/clients/$CID/subscription" | sed 's/.*"link":"\([^"]*\)".*/\1/')
echo "link: $LINK"
curl -s -D /data/head.txt -o /data/sub.txt "$LINK"
grep -iE '^HTTP/|^Subscription-Userinfo|^Profile-' /data/head.txt | tr -d '\r'
echo "--- what the customer receives ---"
head -12 /data/sub.txt

kill $PANEL 2>/dev/null || true
