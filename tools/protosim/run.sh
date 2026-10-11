#!/usr/bin/env bash
# Every protocol end to end: a server running the panel's own drivers -- and
# for L2TP and PPTP its root agent -- clients set up with only what the panel
# hands a customer, and a host beyond the server standing in for the
# internet. Each device is checked part by part (see client.sh): it gets in
# with its own file and only that, it reaches the internet through the
# server, one it was never given is refused, one taken away is cut off, and
# one sign-in used on a second device leaves only that second one on.
#
# The device-compatibility matrix for L2TP and PPTP (which proposals, which
# authentication, which routers) is tools/l2tpsim, run after these.
#
# Needs Docker; run from the repository root:  tools/protosim/run.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
cleanup() {
	docker rm -f protosim-server protosim-inet-host protosim-hold >/dev/null 2>&1 || true
	docker network rm protosim protosim-inet >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

SERVER=172.31.66.10
INTERNET=172.31.88.20
img=wui-protosim

hostpath() { if command -v cygpath >/dev/null; then cygpath -w "$1"; else echo "$1"; fi; }

mkdir -p "$work/bin" "$work/context"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$work/bin/harness" ./tools/protosim/harness
# The image carries both clients: this one, and tools/l2tpsim's for L2TP
# and PPTP, which client.sh drives.
cp "$here/Dockerfile" "$here/client.sh" "$here/systemctl" "$work/context/"
cp "$here/../l2tpsim/client.sh" "$work/context/pppclient.sh"
docker build -q -t "$img" "$work/context" >/dev/null
# Whatever an interrupted run left behind.
docker rm -f protosim-server protosim-inet-host protosim-hold >/dev/null 2>&1 || true
docker network rm protosim protosim-inet >/dev/null 2>&1 || true
docker network create --subnet 172.31.66.0/24 protosim >/dev/null
docker network create --subnet 172.31.88.0/24 protosim-inet >/dev/null

# The internet: a host the clients cannot reach except through the server.
docker run -d --name protosim-inet-host --network protosim-inet --ip "$INTERNET" "$img" sleep infinity >/dev/null

start_server() { # harness flags...
	docker rm -f protosim-server >/dev/null 2>&1 || true
	rm -rf "$work/out" "$work/state"; mkdir -p "$work/out" "$work/state"
	MSYS_NO_PATHCONV=1 docker create --name protosim-server --privileged \
		--sysctl net.ipv4.ip_forward=1 --network protosim --ip "$SERVER" \
		-v "$(hostpath "$work/bin"):/sim/bin:ro" -v "$(hostpath "$work/out"):/out" \
		"$img" /sim/bin/harness -endpoint "$SERVER" "$@" >/dev/null
	docker network connect --ip 172.31.88.10 protosim-inet protosim-server
	docker start protosim-server >/dev/null
	for _ in $(seq 1 600); do
		docker logs protosim-server 2>&1 | grep -q "server ready" && return 0
		if [ "$(docker inspect -f '{{.State.Running}}' protosim-server)" != true ]; then break; fi
		sleep 0.2
	done
	docker logs protosim-server 2>&1 | tail -30; return 1
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
verdict() { # PASS|FAIL line
	echo "RESULT $1 $2"
	if [ "$1" = PASS ]; then pass=$((pass + 1)); else fail=$((fail + 1)); fi
}

# client_args NAME PROTOCOL DEVICE EXPECT sets CLIENT for one device: its
# own address on the network, and what it is.
client_args() {
	n=$((n + 1))
	CLIENT=(--privileged --network protosim --ip "172.31.66.$((100 + n))"
		-e NAME="$1" -e PROTOCOL="$2" -e DEVICE="$3" -e EXPECT="$4"
		-e GATEWAY=10.66.0.1 -e INTERNET="$INTERNET" -e DNS=1.1.1.1 -e MTU=1420
		-e VERBOSE="${VERBOSE:-}")
}
# client NAME PROTOCOL DEVICE EXPECT
client() {
	client_args "$@"
	if MSYS_NO_PATHCONV=1 docker run --rm "${CLIENT[@]}" \
		-v "$(hostpath "$work/out"):/out:ro" "$img" /sim/client.sh; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
	fi
}

# same_sign_in LABEL PROTOCOL: a device stays on; the same sign-in comes in
# from a second device; the second is in, and the first is cut off.
same_sign_in() {
	local label=$1 proto=$2
	client_args "$label: same sign-in, first device" "$proto" dev01 ok
	MSYS_NO_PATHCONV=1 docker run -d --rm --name protosim-hold "${CLIENT[@]}" -e HOLD=1 -e HOLD_KEY=first \
		-v "$(hostpath "$work/out"):/out:ro" -v "$(hostpath "$work/state"):/state" "$img" /sim/client.sh >/dev/null
	for _ in $(seq 1 60); do [ -f "$work/state/first.up" ] && break; sleep 1; done
	if [ ! -f "$work/state/first.up" ]; then
		docker logs protosim-hold 2>&1 | tail -20
		verdict FAIL "$label: same sign-in, first device did not get in"
		docker rm -f protosim-hold >/dev/null 2>&1 || true
		return
	fi
	verdict PASS "$label: same sign-in, first device in"
	client "$label: same sign-in on a second device" "$proto" dev01 ok
	for _ in $(seq 1 40); do [ -f "$work/state/first.ended" ] && break; sleep 1; done
	if [ -f "$work/state/first.ended" ]; then
		verdict PASS "$label: same sign-in, the first device was cut off"
	else
		verdict FAIL "$label: same sign-in, the first device is still on beside the second"
	fi
	docker rm -f protosim-hold >/dev/null 2>&1 || true
}

suite() { # label client-protocol harness-flags...
	local label=$1 proto=$2; shift 2
	echo "== $label"
	start_server "$@"
	client "$label: first device"                 "$proto" dev01 ok
	client "$label: second device"                "$proto" dev02 ok
	client "$label: third device"                 "$proto" dev03 ok
	client "$label: a device it never made"       "$proto" stranger fail
	drop_last
	client "$label: third device, taken away"     "$proto" dev03 fail
	client "$label: first device, still in"       "$proto" dev01 ok
	if [ "$proto" != wireguard ]; then
		# One WireGuard key on two devices is not the driver's to stop: the
		# kernel keeps one endpoint per peer, and the panel's reconciler
		# holds a fought-over file off (internal/reconciler, tested there).
		same_sign_in "$label" "$proto"
	fi
}

suite "WireGuard" wireguard -protocol wireguard
suite "OpenVPN over UDP" openvpn -protocol openvpn -transport udp
suite "OpenVPN over TCP" openvpn -protocol openvpn -transport tcp
suite "L2TP/IPsec" l2tp -protocol l2tp
suite "L2TP without IPsec" l2tp-plain -protocol l2tp -plain
suite "PPTP" pptp -protocol pptp

echo
echo "$pass passed, $fail failed"
ok=$([ "$fail" -eq 0 ] && echo 1 || echo 0)

echo
echo "== Devices and routers on L2TP and PPTP (tools/l2tpsim)"
bash "$here/../l2tpsim/run.sh" || ok=0
[ "$ok" = 1 ]
