#!/bin/sh
# One customer's device, set up with nothing but the file the panel handed
# it. Each part of a working tunnel is checked on its own, so a failure says
# which part is missing rather than just "did not work":
#
#   connect   the tunnel comes up (WireGuard handshake, OpenVPN sign-in)
#   address   the device got the address the panel gave it
#   gateway   the server's end of the tunnel answers
#   internet  a host beyond the server answers: the panel lets customers out
#   route     all traffic goes through the tunnel, not around it
#   dns       the server's DNS reaches the device
#   mtu       the tunnel's MTU is the one the panel set (WireGuard)
#
#   NAME PROTOCOL DEVICE EXPECT(ok|fail) GATEWAY INTERNET DNS MTU
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
		echo "---- addresses"; ip -4 -o addr 2>/dev/null
		echo "---- routes"; ip route 2>/dev/null; ip rule 2>/dev/null
	fi
	[ "$verdict" = PASS ]
	exit $?
}

profile=/out/$DEVICE.profile
want_ip=$(cat "/out/$DEVICE.ip")

case "$PROTOCOL" in
wireguard)
	dev=wg0
	mkdir -p /etc/wireguard
	cp "$profile" /etc/wireguard/wg0.conf
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
	openvpn --config "$profile" --auth-user-pass "/out/$DEVICE.creds" \
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
*)
	log "unknown protocol $PROTOCOL"; exit 2
	;;
esac

got_ip=$(ip -4 -o addr show dev "$dev" | awk '{print $4}' | cut -d/ -f1)
[ "$got_ip" = "$want_ip" ] && check address yes || check "address($got_ip!=$want_ip)" no

ping -c 2 -W 3 "$GATEWAY" >/dev/null 2>&1 && check gateway yes || check gateway no
ping -c 2 -W 3 "$INTERNET" >/dev/null 2>&1 && check internet yes || check internet no

# Where a packet for anywhere would go.
via=$(ip route get 8.8.8.8 2>/dev/null | grep -o "dev [^ ]*" | awk '{print $2}')
[ "$via" = "$dev" ] && check route yes || check "route(via=$via)" no

case "$PROTOCOL" in
wireguard)
	grep -q "nameserver $DNS" /etc/resolv.conf && check dns yes || check dns no
	mtu=$(cat /sys/class/net/wg0/mtu)
	[ "$mtu" = "$MTU" ] && check mtu yes || check "mtu($mtu)" no
	;;
openvpn)
	grep "PUSH_REPLY" /tmp/client.log | grep -q "dhcp-option DNS $DNS" && check dns yes || check dns no
	;;
esac

[ "$bad" = 0 ] && finish ok
finish fail
