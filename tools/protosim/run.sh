#!/usr/bin/env bash
# Every protocol end to end: a server running the panel's own WireGuard and
# OpenVPN drivers, clients set up with only the files the panel hands out,
# and a host beyond the server standing in for the internet. Each device is
# checked part by part (see client.sh). L2TP, L2TP without IPsec and PPTP
# follow, from tools/l2tpsim.
#
# Needs Docker; run from the repository root:  tools/protosim/run.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
cleanup() {
	docker rm -f protosim-server protosim-inet-host >/dev/null 2>&1 || true
	docker network rm protosim protosim-inet >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

SERVER=172.31.66.10
INTERNET=172.31.88.20
img=wui-protosim

hostpath() { if command -v cygpath >/dev/null; then cygpath -w "$1"; else echo "$1"; fi; }

mkdir -p "$work/bin"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$work/bin/harness" ./tools/protosim/harness
docker build -q -t "$img" "$here" >/dev/null
cleanup_nets() { docker network rm protosim protosim-inet >/dev/null 2>&1 || true; }
cleanup_nets
docker network create --subnet 172.31.66.0/24 protosim >/dev/null
docker network create --subnet 172.31.88.0/24 protosim-inet >/dev/null

# The internet: a host the clients cannot reach except through the server.
docker run -d --name protosim-inet-host --network protosim-inet --ip "$INTERNET" "$img" sleep infinity >/dev/null

start_server() { # protocol [transport]
	docker rm -f protosim-server >/dev/null 2>&1 || true
	rm -rf "$work/out"; mkdir -p "$work/out"
	MSYS_NO_PATHCONV=1 docker create --name protosim-server --privileged \
		--sysctl net.ipv4.ip_forward=1 --network protosim --ip "$SERVER" \
		-v "$(hostpath "$work/bin"):/sim/bin:ro" -v "$(hostpath "$work/out"):/out" \
		"$img" /sim/bin/harness -protocol "$1" -transport "${2:-udp}" -endpoint "$SERVER" >/dev/null
	docker network connect --ip 172.31.88.10 protosim-inet protosim-server
	docker start protosim-server >/dev/null
	for _ in $(seq 1 75); do
		docker logs protosim-server 2>&1 | grep -q "server ready" && return 0
		if [ "$(docker inspect -f '{{.State.Running}}' protosim-server)" != true ]; then break; fi
		sleep 0.2
	done
	docker logs protosim-server; return 1
}

drop_last() {
	: > "$work/out/ctl/drop"
	for _ in $(seq 1 50); do
		[ -f "$work/out/ctl/dropped" ] && return 0
		sleep 0.2
	done
	echo "the server did not take the device away"; docker logs protosim-server 2>&1 | tail -20; return 1
}

pass=0; fail=0; n=0
# client NAME PROTOCOL DEVICE EXPECT
client() {
	n=$((n + 1))
	if MSYS_NO_PATHCONV=1 docker run --rm --privileged --network protosim --ip "172.31.66.$((100 + n))" \
		-v "$(hostpath "$work/out"):/out:ro" \
		-e NAME="$1" -e PROTOCOL="$2" -e DEVICE="$3" -e EXPECT="$4" \
		-e GATEWAY=10.66.0.1 -e INTERNET="$INTERNET" -e DNS=1.1.1.1 -e MTU=1420 \
		-e VERBOSE="${VERBOSE:-}" "$img" /sim/client.sh; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
	fi
}

suite() { # label protocol [transport]
	echo "== $1"
	start_server "$2" "${3:-}"
	client "$1: first device"                 "$2" dev01 ok
	client "$1: second device"                "$2" dev02 ok
	client "$1: third device"                 "$2" dev03 ok
	client "$1: a device it never made"       "$2" stranger fail
	drop_last
	client "$1: third device, taken away"     "$2" dev03 fail
	client "$1: first device, still in"       "$2" dev01 ok
}

suite "WireGuard" wireguard
suite "OpenVPN over UDP" openvpn udp
suite "OpenVPN over TCP" openvpn tcp

echo
echo "$pass passed, $fail failed, of $n"
ok=$([ "$fail" -eq 0 ] && echo 1 || echo 0)

echo
echo "== L2TP/IPsec, L2TP without IPsec, PPTP (tools/l2tpsim)"
bash "$here/../l2tpsim/run.sh" || ok=0
[ "$ok" = 1 ]
