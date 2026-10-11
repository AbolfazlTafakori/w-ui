#!/bin/sh
# One customer's device, set up with nothing but what the panel handed it:
# the file (WireGuard, OpenVPN) or the sign-in on the customer's page (L2TP,
# PPTP). Each part of a working tunnel is checked on its own, so a failure
# says which part is missing rather than just "did not work":
#
#   connect   the tunnel comes up (handshake, sign-in, PPP link)
#   address   the device got the address the panel gave it
#   gateway   the server's end of the tunnel answers
#   internet  a host beyond the server answers: the panel lets customers out
#   route     all traffic goes through the tunnel (WireGuard, OpenVPN: the
#             file says so; a router on L2TP or PPTP decides that itself)
#   dns       the server's DNS reaches the device
#   mtu       the tunnel's MTU is the one the panel set (WireGuard)
#
#   NAME PROTOCOL(wireguard|openvpn|l2tp|l2tp-plain|pptp) DEVICE
#   EXPECT(ok|fail) GATEWAY INTERNET DNS MTU
#   HOLD     1: once in, stay, and note in /state when the tunnel is taken
#            away (HOLD_KEY.up, then HOLD_KEY.ended); the device does not dial again
set -u

log() { echo "[$NAME] $*"; }
checks=""
bad=0
check() { # name result
	checks="$checks $1=$2"
	[ "$2" = yes ] || bad=1
}

finish() {
	got=$1
	if [ "$got" = "$EXPECT" ]; then verdict=PASS; else verdict=FAIL; fi
	echo "RESULT $verdict $NAME expected=$EXPECT got=$got$checks"
	if [ "$verdict" = FAIL ] || [ "${VERBOSE:-}" = 1 ]; then
		echo "---- client log"; tail -n 40 /tmp/client.log 2>/dev/null
		echo "---- ppp"; tail -n 40 /tmp/sim/syslog 2>/dev/null
		echo "---- addresses"; ip -4 -o addr 2>/dev/null
		echo "---- routes"; ip route 2>/dev/null; ip rule 2>/dev/null
	fi
	[ "$verdict" = PASS ] || exit 1
	[ "${HOLD:-}" = 1 ] && [ "$got" = ok ] && hold
	exit 0
}

# hold keeps the device on until the server takes its tunnel away.
hold() {
	: > "/state/${HOLD_KEY:-hold}.up"
	i=0
	misses=0
	while [ "$i" -lt 90 ]; do
		gone=0
		case "$PROTOCOL" in
		openvpn) kill -0 "$(cat /tmp/ovpn.pid 2>/dev/null)" 2>/dev/null || gone=1 ;;
		wireguard) ;;
		*) ip link show ppp0 >/dev/null 2>&1 || gone=1 ;;
		esac
		# A UDP OpenVPN client is not told its session was replaced; that
		# the server no longer carries its traffic is how it shows.
		if [ "$gone" = 0 ]; then
			if ping -c 1 -W 1 -I "$dev" "$GATEWAY" >/dev/null 2>&1; then misses=0; else misses=$((misses + 1)); fi
			[ "$misses" -ge 3 ] && gone=1
		fi
		if [ "$gone" = 1 ]; then
			: > "/state/${HOLD_KEY:-hold}.ended"
			exit 0
		fi
		i=$((i + 1)); sleep 1
	done
	exit 0
}

want_ip=$(cat "/out/$DEVICE.ip")

case "$PROTOCOL" in
wireguard)
	dev=wg0
	mkdir -p /etc/wireguard
	cp "/out/$DEVICE.profile" /etc/wireguard/wg0.conf
	chmod 600 /etc/wireguard/wg0.conf
	# wg-quick's anti-spoofing rule needs nftables' fib, which the WSL2
	# kernel this runs on lacks. It guards the client, not the tunnel, so
	# wg-quick is left to go without it, as it does where nft is absent.
	[ -x /usr/sbin/nft ] && mv /usr/sbin/nft /usr/sbin/nft.off
	for t in iptables-restore ip6tables-restore; do
		printf '#!/bin/sh\ncat >/dev/null\n' > "/usr/local/bin/$t"; chmod +x "/usr/local/bin/$t"
	done
	if ! wg-quick up wg0 >/tmp/client.log 2>&1; then
		check connect no; finish fail
	fi
	# A WireGuard tunnel is "up" before it has spoken to anyone: the
	# handshake is what says the server took this device's key.
	i=0; hs=0
	while [ "$i" -lt 12 ]; do
		ping -c 1 -W 1 "$GATEWAY" >/dev/null 2>&1
		hs=$(wg show wg0 latest-handshakes | awk '{print $2}')
		[ "${hs:-0}" -gt 0 ] && break
		i=$((i + 1))
	done
	if [ "${hs:-0}" -eq 0 ]; then check connect no; finish fail; fi
	check connect yes
	;;
openvpn)
	dev=tun0
	# A held device does not dial again once it is cut off: the test is
	# whether the server cut it off, not what the device does next.
	again=""
	[ "${HOLD:-}" = 1 ] && again="--remap-usr1 SIGTERM"
	# shellcheck disable=SC2086
	openvpn --config "/out/$DEVICE.profile" --auth-user-pass "/out/$DEVICE.creds" $again \
		--log /tmp/client.log --daemon --writepid /tmp/ovpn.pid
	i=0
	while [ "$i" -lt 25 ]; do
		grep -q "Initialization Sequence Completed" /tmp/client.log 2>/dev/null && break
		grep -q "AUTH_FAILED" /tmp/client.log 2>/dev/null && break
		i=$((i + 1)); sleep 1
	done
	if ! grep -q "Initialization Sequence Completed" /tmp/client.log; then
		check connect no; finish fail
	fi
	check connect yes
	;;
l2tp|l2tp-plain|pptp)
	dev=ppp0
	# shellcheck disable=SC1090
	. "/out/$DEVICE.env"
	export SERVER USER PASS
	export NAME CONNECT_ONLY=1 TUNNEL=""
	case "$PROTOCOL" in
	l2tp)
		# Windows' own client: what most customers dial with.
		export PSK IKE=3des-sha1-modp1024,aes256-sha256-modp2048 ESP=3des-sha1,aes256-sha256 PPPOPTS='refuse-pap\nrefuse-eap' PPTP="" ;;
	l2tp-plain)
		# A router without IPsec: pppd's defaults.
		export PSK="" IKE="" ESP="" PPPOPTS="" PPTP="" ;;
	pptp)
		export PSK="" IKE="" ESP="" PPTP=1 PPPOPTS='require-mppe-128\nrefuse-pap\nrefuse-eap\nrefuse-chap\nrefuse-mschap' ;;
	esac
	if ! /sim/pppclient.sh 2>/tmp/client.log; then
		check connect no; finish fail
	fi
	check connect yes
	;;
*)
	log "unknown protocol $PROTOCOL"; exit 2
	;;
esac

got_ip=$(ip -4 -o addr show dev "$dev" | awk '{print $4}' | cut -d/ -f1)
[ "$got_ip" = "$want_ip" ] && check address yes || check "address($got_ip!=$want_ip)" no

ping -c 2 -W 3 -I "$dev" "$GATEWAY" >/dev/null 2>&1 && check gateway yes || check gateway no
ping -c 2 -W 3 -I "$dev" "$INTERNET" >/dev/null 2>&1 && check internet yes || check internet no

case "$PROTOCOL" in
wireguard|openvpn)
	# Where a packet for anywhere would go.
	via=$(ip route get 8.8.8.8 2>/dev/null | grep -o "dev [^ ]*" | awk '{print $2}')
	[ "$via" = "$dev" ] && check route yes || check "route(via=$via)" no
	;;
esac

case "$PROTOCOL" in
wireguard)
	grep -q "nameserver $DNS" /etc/resolv.conf && check dns yes || check dns no
	mtu=$(cat /sys/class/net/wg0/mtu)
	[ "$mtu" = "$MTU" ] && check mtu yes || check "mtu($mtu)" no
	;;
openvpn)
	grep "PUSH_REPLY" /tmp/client.log | grep -q "dhcp-option DNS $DNS" && check dns yes || check dns no
	;;
*)
	grep -q "primary   DNS address $DNS" /tmp/sim/syslog && check dns yes || check dns no
	;;
esac

[ "$bad" = 0 ] && finish ok
finish fail
