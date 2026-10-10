#!/usr/bin/env bash
# Simulates the devices the L2TP guide lists against a server running the
# panel's own configuration, in containers: each scenario is one device's
# IPsec proposals, PFS, PPP authentication, MPPE and tunnel password, and
# says whether that device should get in. Needs Docker; run from the
# repository root:  tools/l2tpsim/run.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'docker rm -f l2tpsim-server >/dev/null 2>&1 || true; docker network rm l2tpsim >/dev/null 2>&1 || true; rm -rf "$work"' EXIT

PSK=K7rTq2mWx9ZbV4nLs8Pd
SERVER=172.31.77.10
img=wui-l2tpsim

docker build -q -t "$img" "$here" >/dev/null
docker network rm l2tpsim >/dev/null 2>&1 || true
docker network create --subnet 172.31.77.0/24 l2tpsim >/dev/null

# A Windows path for Docker Desktop when run from Git Bash.
hostpath() { if command -v cygpath >/dev/null; then cygpath -w "$1"; else echo "$1"; fi; }

start_server() {
	docker rm -f l2tpsim-server >/dev/null 2>&1 || true
	rm -f "$work"/*
	go run ./tools/l2tpsim/render -out "$work" -psk "$PSK" $1
	MSYS_NO_PATHCONV=1 docker run -d --name l2tpsim-server --privileged --network l2tpsim --ip "$SERVER" \
		-v "$(hostpath "$work"):/conf:ro" "$img" /sim/server.sh >/dev/null
	for _ in $(seq 1 50); do
		docker logs l2tpsim-server 2>&1 | grep -q "server ready" && return 0
		sleep 0.2
	done
	docker logs l2tpsim-server; return 1
}

pass=0; fail=0; n=0
# scenario NAME EXPECT PSK IKE ESP TUNNEL PPPOPTS
scenario() {
	n=$((n + 1))
	local user; user=$(printf 'dev%02d' "$n")
	local pass_; pass_=$(printf 'Pw%02dxk9Lq2' "$n")
	# An address of its own: one that an earlier device used would still
	# have that device's IPsec policy on the server, as a phone that went
	# away without saying so does until dead-peer detection clears it.
	if MSYS_NO_PATHCONV=1 docker run --rm --privileged --network l2tpsim --ip "172.31.77.$((100 + n))" \
		-e NAME="$1" -e EXPECT="$2" -e PSK="$3" -e IKE="$4" -e ESP="$5" -e TUNNEL="$6" -e PPPOPTS="$7" \
		-e SERVER="$SERVER" -e WANT_IP="10.77.0.$((n + 1))" -e USER="$user" -e PASS="$pass_" -e VERBOSE="${VERBOSE:-}" -e PPTP="${PPTP:-}" \
		"$img" /sim/client.sh; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
	fi
}

ms='refuse-pap\nrefuse-eap'

echo "== server: IPsec only"
start_server ""
scenario "Windows 10/11 (3DES, SHA-1, group 2)"           ok "$PSK" 3des-sha1-modp1024 3des-sha1 "" "$ms"
scenario "iPhone, iPad, macOS (AES-256, SHA-256, group 14)" ok "$PSK" aes256-sha256-modp2048 aes256-sha256 "" "$ms"
scenario "Android 11 and older"                           ok "$PSK" aes256-sha1-modp1024 aes256-sha1 "" "$ms"
scenario "Linux NetworkManager (AES-GCM)"                 ok "$PSK" aes256-sha256-modp2048 aes256gcm16 "" "$ms"
scenario "MikroTik use-ipsec (PFS group 2)"               ok "$PSK" aes128-sha1-modp2048,aes128-sha1-modp1024 aes256-sha1-modp1024,aes128-sha1-modp1024 "" "$ms"
scenario "D-Link L2TP over IPsec, PFS on"                 ok "$PSK" aes256-sha1-modp2048 aes256-sha1-modp2048 "" "$ms"
scenario "TP-Link VPN Client L2TP/IPSec"                  ok "$PSK" aes256-sha256-modp2048,aes128-sha1-modp1024 aes256-sha256,aes128-sha1 "" "$ms"
scenario "Keenetic L2TP/IPsec"                            ok "$PSK" aes256-sha1-modp2048,3des-sha1-modp1024 aes256-sha1,3des-sha1 "" "$ms"
scenario "Omada ER605 L2TP client, IPsec encrypted"       ok "$PSK" aes256-sha256-modp2048,aes128-sha1-modp1024 aes128-sha1,aes256-sha256 "" "$ms"
scenario "A key that is not the tunnel's"                 fail "wrong-key-123" aes256-sha256-modp2048 aes256-sha256 "" "$ms"
scenario "A router without IPsec, refused here"           fail "" "" "" "" ""
scenario "PAP only, never accepted"                       fail "$PSK" aes256-sha256-modp2048 aes256-sha256 "" 'refuse-chap\nrefuse-mschap\nrefuse-mschap-v2\nrefuse-eap'

echo "== server: IPsec, and routers without it"
start_server "-plain"
scenario "Windows still over IPsec"                       ok "$PSK" 3des-sha1-modp1024 3des-sha1 "" "$ms"
scenario "MikroTik use-ipsec still over IPsec"            ok "$PSK" aes128-sha1-modp1024 aes128-sha1-modp1024 "" "$ms"
scenario "TP-Link / Mercusys L2TP WAN (pppd defaults)"    ok "" "" "" "" ""
scenario "Router whose PPP has only CHAP"                 ok "" "" "" "" 'refuse-mschap\nrefuse-mschap-v2\nrefuse-pap\nrefuse-eap'
scenario "D-Link, AUTO and No encryption, as the guide says" ok "" "" "" "" "$ms"
scenario "D-Link left on MPPE 128 bit: refused, guide says off" fail "" "" "" "" 'require-mppe-128\nrefuse-pap\nrefuse-chap\nrefuse-mschap\nrefuse-eap'
scenario "MikroTik without IPsec (mschap2, chap)"         ok "" "" "" "" 'refuse-pap\nrefuse-eap\nrefuse-mschap'
scenario "Huawei / TF-i60 with a Tunnel password"         ok "" "" "" "$PSK" "$ms"
scenario "Tunnel password that is not the key"            fail "" "" "" "not-the-key" "$ms"
scenario "PAP only, still never accepted"                 fail "" "" "" "" 'refuse-chap\nrefuse-mschap\nrefuse-mschap-v2\nrefuse-eap'

echo "== server: PPTP"
start_server "-pptp"
export PPTP=1
scenario "Windows PPTP (MS-CHAP v2, MPPE required)"       ok "" "" "" "" 'require-mppe-128\nrefuse-pap\nrefuse-eap\nrefuse-chap\nrefuse-mschap'
scenario "TP-Link VPN Client PPTP, Encryption Max (MPPE)" ok "" "" "" "" 'require-mppe-128'
scenario "D-Link PPTP, MS-CHAPv2 and MPPE 128 bit"        ok "" "" "" "" 'require-mppe-128\nrefuse-pap\nrefuse-chap\nrefuse-mschap\nrefuse-eap'
scenario "MikroTik pptp-client, mschap2, default-encryption" ok "" "" "" "" 'require-mppe-128\nrefuse-pap\nrefuse-chap\nrefuse-mschap\nrefuse-eap'
scenario "Router with encryption off: refused"            fail "" "" "" "" 'nomppe'
scenario "CHAP only, never accepted"                      fail "" "" "" "" 'refuse-mschap\nrefuse-mschap-v2\nrefuse-pap\nrefuse-eap'
scenario "PAP only, never accepted"                       fail "" "" "" "" 'refuse-chap\nrefuse-mschap\nrefuse-mschap-v2\nrefuse-eap'
unset PPTP

echo
echo "$pass passed, $fail failed, of $n"
[ "$fail" -eq 0 ]
