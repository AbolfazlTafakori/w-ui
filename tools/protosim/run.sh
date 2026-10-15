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
# A server with a newer ocserv, for camouflage.
cp "$here/Dockerfile.trixie" "$work/context/"
docker build -q -t "$img-trixie" -f "$work/context/Dockerfile.trixie" "$work/context" >/dev/null
# Whatever an interrupted run left behind.
docker rm -f protosim-server protosim-inet-host protosim-hold >/dev/null 2>&1 || true
docker network rm protosim protosim-inet >/dev/null 2>&1 || true
docker network create --subnet 172.31.64.0/20 protosim >/dev/null
docker network create --subnet 172.31.88.0/24 protosim-inet >/dev/null

# The internet: a host the clients cannot reach except through the server.
docker run -d --name protosim-inet-host --network protosim-inet --ip "$INTERNET" "$img" sleep infinity >/dev/null

start_server() { # harness flags...
	docker rm -f protosim-server >/dev/null 2>&1 || true
	rm -rf "$work/out" "$work/state"; mkdir -p "$work/out" "$work/state"
	MSYS_NO_PATHCONV=1 docker create --name protosim-server --privileged \
		--sysctl net.ipv4.ip_forward=1 --network protosim --ip "$SERVER" \
		-v "$(hostpath "$work/bin"):/sim/bin:ro" -v "$(hostpath "$work/out"):/out" \
		"${SERVER_IMG:-$img}" /sim/bin/harness -endpoint "$SERVER" "$@" >/dev/null
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
	CLIENT=(--privileged --network protosim --ip "172.31.$((72 + n / 250)).$((n % 250 + 2))"
		-e NAME="$1" -e PROTOCOL="$2" -e DEVICE="$3" -e EXPECT="$4"
		-e GATEWAY=10.66.0.1 -e INTERNET="$INTERNET" -e DNS=1.1.1.1 -e MTU=1420 -e SERVER_IP="$SERVER" -e SPECIALS="${SPECIALS:-}" -e OLDFILE="${OLDFILE:-}"
		-e VERBOSE="${VERBOSE:-}" -e PORT="${PORT:-}" -e ENCAP="${ENCAP:-}")
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

# sessions_of USER: how many sessions the OpenVPN server holds for a user,
# asked of its management socket.
sessions_of() {
	docker exec protosim-server python3 -c "
import socket, sys
s = socket.socket(socket.AF_UNIX)
s.connect('/var/lib/wui/openvpn/ovpn0/management.sock')
s.sendall(b'status 2\n')
buf = b''
while b'END' not in buf:
    buf += s.recv(65536)
print(sum(1 for l in buf.decode().splitlines() if l.startswith('CLIENT_LIST,$1,')))
"
}

# left_at_once LABEL WANT: a UDP device disconnects; seven seconds later the
# server still holds WANT sessions for it -- none with explicit-exit-notify,
# whose exit OpenVPN carries out five seconds after it is told, and one for a
# file from before it, held until the keepalive runs out two minutes on.
left_at_once() {
	local label=$1 want=$2 got
	client "$label: a device that disconnects" openvpn dev02 ok
	sleep 5
	got=$(sessions_of s1-dev02)
	if [ "$got" = "$want" ]; then
		verdict PASS "$label: sessions left 7s after it disconnected: $got"
	else
		verdict FAIL "$label: sessions left 7s after it disconnected: $got, want $want"
		docker exec protosim-server sh -c 'tail -n 25 /var/lib/wui/openvpn/ovpn0/*.log' 2>&1
	fi
}

suite() { # label client-protocol harness-flags...
	local label=$1 proto=$2; shift 2
	# ONLY=text runs only the suites whose label has it in.
	case "$label" in *"${ONLY:-}"*) ;; *) return ;; esac
	echo "== $label"
	start_server "$@"
	client "$label: first device"                 "$proto" dev01 ok
	client "$label: second device"                "$proto" dev02 ok
	client "$label: third device"                 "$proto" dev03 ok
	client "$label: a device it never made"       "$proto" stranger fail
	drop_last
	client "$label: third device, taken away"     "$proto" dev03 fail
	client "$label: first device, still in"       "$proto" dev01 ok
	# How long each sync of the whole tunnel took on the server.
	docker logs protosim-server 2>&1 | grep -o 'devices=[0-9]* took=[^ ]*' | sed 's/^/  sync: /'
	if [ "$label" = "OpenVPN over UDP" ]; then
		left_at_once "$label" 0
	elif [ "$label" = "OpenVPN over UDP, a file from before" ]; then
		left_at_once "$label" 1
	fi
	if [ "$proto" != wireguard ] && [ "$proto" != amneziawg ]; then
		# One WireGuard key on two devices is not the driver's to stop: the
		# kernel keeps one endpoint per peer, and the panel's reconciler
		# holds a fought-over file off (internal/reconciler, tested there).
		same_sign_in "$label" "$proto"
	fi
}

suite "WireGuard" wireguard -protocol wireguard
# AmneziaWG on amneziawg-go, as on a server without the module: a profile as
# the panel draws one, and one with identifier ranges and I1-I2 from the
# form's ready packets, whose QUIC packet has to be seen on the wire.
suite "AmneziaWG" amneziawg -protocol wireguard -awg classic
SPECIALS=1 suite "AmneziaWG 2 (ranges, I1-I2)" amneziawg -protocol wireguard -awg awg2
# The same with 3000 more devices on the tunnel: what a full server carries.
SPECIALS=1 suite "AmneziaWG 2 at scale (3000 devices)" amneziawg -protocol wireguard -awg awg2 -extra 3000
suite "OpenVPN over UDP" openvpn -protocol openvpn -transport udp
suite "OpenVPN over TCP" openvpn -protocol openvpn -transport tcp
# The files customers already hold, from before the connection options,
# against the server as it is now.
OLDFILE=1 suite "OpenVPN over UDP, a file from before" openvpn -protocol openvpn -transport udp
OLDFILE=1 suite "OpenVPN over TCP, a file from before" openvpn -protocol openvpn -transport tcp
suite "L2TP/IPsec" l2tp -protocol l2tp
suite "L2TP without IPsec" l2tp-plain -protocol l2tp -plain
suite "PPTP" pptp -protocol pptp
# OpenConnect, as Cisco Secure Client and openconnect dial it: over DTLS,
# and over TLS alone.
suite "OpenConnect" openconnect -protocol openconnect
suite "OpenConnect over TLS only" openconnect-nodtls -protocol openconnect -no-dtls
# Camouflage, on an ocserv that has it: the address with its secret gets in,
# the same server without the secret is nothing to whoever asks.
SERVER_IMG="$img-trixie" suite "OpenConnect with camouflage" openconnect -protocol openconnect -camouflage
# only LABEL: whether ONLY lets a suite of this label run.
only() { case "$1" in *"${ONLY:-}"*) return 0 ;; esac; return 1; }
if only "OpenConnect with camouflage"; then
	client "OpenConnect with camouflage: without the secret, as a scanner asks" openconnect-wrongurl dev01 fail
fi
# And on one that has not: devices still get in, and the operator is told.
suite "OpenConnect asking camouflage of an old ocserv" openconnect -protocol openconnect -camouflage
if only "OpenConnect asking camouflage of an old ocserv"; then
	if MSYS_NO_PATHCONV=1 docker exec protosim-server cat /var/lib/wui/openconnect/report.json 2>/dev/null |
		grep -q '"notice": *"[^"]*older than 1.2'; then
		verdict PASS "OpenConnect asking camouflage of an old ocserv: the operator is told"
	else
		verdict FAIL "OpenConnect asking camouflage of an old ocserv: no notice in the report"
	fi
fi
# One IKEv2 tunnel, entered every way a phone or a computer has built in.
suite "IKEv2 by password (MSCHAPv2)" ikev2-mschapv2 -protocol ikev2
suite "IKEv2 by key (PSK)" ikev2-psk -protocol ikev2
suite "IKEv2 by certificate (RSA)" ikev2-rsa -protocol ikev2
suite "IKEv2 by certificate in EAP (EAP-TLS)" ikev2-eap-tls -protocol ikev2

# A port of the tunnel's own, for where the standard one is blocked: the
# strongSwan app dials it directly; a client that cannot be given a port is
# pointed at it, as a router forwarding a port would. The panel redirects it
# to the daemon's own, and the redirect has to be what carried them.
redirected() { # LABEL
	local label=$1 n
	only "$label" || return 0
	n=$(MSYS_NO_PATHCONV=1 docker exec protosim-server nft list chain ip wui_nat prerouting 2>/dev/null |
		grep -o 'packets [0-9]*' | awk '{s+=$2} END {print s+0}')
	if [ "${n:-0}" -gt 0 ]; then
		verdict PASS "$label: came in by the tunnel's own port ($n packets redirected)"
	else
		verdict FAIL "$label: nothing came in by the tunnel's own port"
	fi
}
PORT=41500 suite "IKEv2 on a port of its own" ikev2-mschapv2 -protocol ikev2 -port 41500
redirected "IKEv2 on a port of its own"
# A phone behind its carrier's NAT: IKE with the marker and ESP in UDP.
ENCAP=yes PORT=41500 suite "IKEv2 on a port of its own, behind NAT" ikev2-mschapv2 -protocol ikev2 -port 41500
redirected "IKEv2 on a port of its own, behind NAT"
PORT=41723 suite "PPTP on a port of its own" pptp -protocol pptp -port 41723
redirected "PPTP on a port of its own"
PORT=41701 suite "L2TP without IPsec on a port of its own" l2tp-plain -protocol l2tp -plain -port 41701
redirected "L2TP without IPsec on a port of its own"

echo
echo "$pass passed, $fail failed"
ok=$([ "$fail" -eq 0 ] && echo 1 || echo 0)

if [ -z "${ONLY:-}" ]; then
	echo
	echo "== Devices and routers on L2TP and PPTP (tools/l2tpsim)"
	bash "$here/../l2tpsim/run.sh" || ok=0
fi
[ "$ok" = 1 ]
