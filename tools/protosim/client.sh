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
#   NAME PROTOCOL(wireguard|amneziawg|openvpn|l2tp|l2tp-plain|pptp|
#        ikev2-mschapv2|ikev2-psk|ikev2-rsa|ikev2-eap-tls) DEVICE
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
	# Disconnected the way a customer does it, so the server hears it leave
	# (or, for a file from before explicit-exit-notify, does not).
	if [ "$PROTOCOL" = openvpn ] && [ "${HOLD:-}" != 1 ] && [ -f /tmp/ovpn.pid ]; then
		kill -TERM "$(cat /tmp/ovpn.pid)" 2>/dev/null
		sleep 2
	fi
	[ "${HOLD:-}" = 1 ] && [ "$got" = ok ] && hold
	exit 0
}

# hold keeps the device on until the server takes its tunnel away.
hold() {
	: > "/state/${HOLD_KEY:-hold}.up"
	i=0
	misses=0
	while [ "$i" -lt "${HOLD_SECS:-90}" ]; do
		gone=0
		case "$PROTOCOL" in
		openvpn) kill -0 "$(cat /tmp/ovpn.pid 2>/dev/null)" 2>/dev/null || gone=1 ;;
		wireguard|amneziawg) ;;
		ikev2-*) swanctl --list-sas 2>/dev/null | grep -q ESTABLISHED || gone=1 ;;
		openconnect*) { kill -0 "$(cat /tmp/oc.pid 2>/dev/null)" 2>/dev/null && ip link show tun0 >/dev/null 2>&1; } || gone=1 ;;
		*) ip link show ppp0 >/dev/null 2>&1 || gone=1 ;;
		esac
		# A UDP OpenVPN client is not told its session was replaced; that
		# the server no longer carries its traffic is how it shows.
		if [ "$gone" = 0 ]; then
			if ping -c 1 -W 1 -I "$src" "$GATEWAY" >/dev/null 2>&1; then misses=0; else misses=$((misses + 1)); fi
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
amneziawg)
	# The AmneziaWG app's way: awg-quick with the file as it was handed out,
	# on amneziawg-go, as on a phone. The AmneziaVPN key is checked to be
	# that same file, and what goes on the wire before the handshake is
	# captured, to see the packets I1 and I2 describe really sent.
	dev=wg0
	mkdir -p /etc/amnezia/amneziawg
	cp "/out/$DEVICE.profile" /etc/amnezia/amneziawg/wg0.conf
	chmod 600 /etc/amnezia/amneziawg/wg0.conf
	if [ -f "/out/$DEVICE.avpn" ]; then
		python3 - "/out/$DEVICE.avpn" "/out/$DEVICE.profile" <<'PYK' && check avpn yes || check avpn no
import base64, sys, zlib
key = open(sys.argv[1]).read().strip()
assert key.startswith("vpn://")
body = key[len("vpn://"):]
raw = base64.urlsafe_b64decode(body + "=" * (-len(body) % 4))
data = zlib.decompress(raw[4:])
assert int.from_bytes(raw[:4], "big") == len(data)
assert data == open(sys.argv[2], "rb").read()
PYK
	fi
	[ -x /usr/sbin/nft ] && mv /usr/sbin/nft /usr/sbin/nft.off
	for t in iptables-restore ip6tables-restore; do
		printf '#!/bin/sh\ncat >/dev/null\n' > "/usr/local/bin/$t"; chmod +x "/usr/local/bin/$t"
	done
	tcpdump -i eth0 -n -U -w /tmp/wire.pcap udp and dst host "$SERVER_IP" >/dev/null 2>&1 &
	cap=$!
	sleep 1
	if ! awg-quick up wg0 >/tmp/client.log 2>&1; then
		check connect no; finish fail
	fi
	i=0; hs=0
	while [ "$i" -lt 12 ]; do
		ping -c 1 -W 1 "$GATEWAY" >/dev/null 2>&1
		hs=$(awg show wg0 latest-handshakes | awk '{print $2}')
		[ "${hs:-0}" -gt 0 ] && break
		i=$((i + 1))
	done
	if [ "${hs:-0}" -eq 0 ]; then check connect no; finish fail; fi
	check connect yes
	if [ -n "${SPECIALS:-}" ]; then
		sleep 1; kill "$cap" 2>/dev/null; sleep 1
		# I1 is the QUIC Initial: 1250 bytes beginning c3 00000001 08.
		tcpdump -r /tmp/wire.pcap -n -x 2>/dev/null | python3 -c "
import re, sys
pkts, cur = [], []
for line in sys.stdin:
    if not line.startswith((' ', '\t')):
        if cur: pkts.append(bytes.fromhex(''.join(cur)))
        cur = []
    else:
        cur += re.findall(r'[0-9a-f]{4}', line.split(':', 1)[1])
if cur: pkts.append(bytes.fromhex(''.join(cur)))
payloads = [p[28:] for p in pkts]
sys.exit(0 if any(len(p) == 1250 and p[:6] == bytes.fromhex('c30000000108') for p in payloads) else 1)
" && check specials yes || check specials no
	fi
	;;
openconnect|openconnect-nodtls|openconnect-wrongurl)
	# OpenConnect as the customer's page gives it: the address to dial
	# (with the camouflage secret in it), the certificate's pin, and the
	# sign-in typed when it connects.
	dev=tun0
	# shellcheck disable=SC1090
	. "/out/$DEVICE.env"
	url=$URL
	# A scanner, or a device given the address without its secret: the
	# server has to look like nothing at all to it.
	[ "$PROTOCOL" = openconnect-wrongurl ] && url=${URL%%/\?*}
	printf '%s\n' "$PASS" | openconnect --protocol=anyconnect --user="$USER" --passwd-on-stdin \
		--servercert "$PIN" --non-inter --pid-file=/tmp/oc.pid -b "$url" >/tmp/client.log 2>&1
	i=0
	while [ "$i" -lt 20 ]; do
		ip -4 -o addr show dev tun0 2>/dev/null | grep -q inet && break
		grep -q -e "Failed to complete authentication" -e "Cookie was rejected" -e "Failed to open HTTPS" /tmp/client.log && break
		i=$((i + 1)); sleep 1
	done
	if ! ip -4 -o addr show dev tun0 2>/dev/null | grep -q inet; then
		check connect no; finish fail
	fi
	check connect yes
	sleep 2
	if [ "$PROTOCOL" = openconnect ]; then
		grep -q -e "Established DTLS" -e "DTLS connected" /tmp/client.log && check dtls yes || check dtls no
	fi
	;;
openvpn)
	dev=tun0
	# A held device does not dial again once it is cut off: the test is
	# whether the server cut it off, not what the device does next.
	again=""
	[ "${HOLD:-}" = 1 ] && again="--remap-usr1 SIGTERM"
	profile="/out/$DEVICE.profile"
	if [ "${OLDFILE:-}" = 1 ]; then
		# A file handed out before the connection options: what customers
		# already hold has to keep working against the server as it is now.
		grep -v -e 'block-outside-dns' -e 'explicit-exit-notify' -e '^sndbuf' -e '^rcvbuf' -e 'FRIENDLY_NAME' \
			"$profile" > /tmp/old.ovpn
		profile=/tmp/old.ovpn
	fi
	# shellcheck disable=SC2086
	openvpn --config "$profile" --auth-user-pass "/out/$DEVICE.creds" $again \
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
	if [ -n "${PORT:-}" ]; then
		# A tunnel on a port of its own; this client has no setting for one.
		case "$PROTOCOL" in pptp) tp=tcp std=1723 ;; *) tp=udp std=1701 ;; esac
		nft add table ip simport
		nft add chain ip simport out '{ type nat hook output priority -100; }'
		nft add rule ip simport out ip daddr "$SERVER" $tp dport $std dnat to "$SERVER:$PORT"
	fi
	if ! /sim/pppclient.sh 2>/tmp/client.log; then
		check connect no; finish fail
	fi
	check connect yes
	;;
ikev2-*)
	# strongSwan as a phone or a computer would be set up from the page:
	# the server, its identity, and the way in this device was given.
	# shellcheck disable=SC1090
	. "/out/$DEVICE.env"
	cp /out/ca.crt /etc/swanctl/x509ca/
	cp "/out/$DEVICE.crt" /etc/swanctl/x509/dev.crt
	cp "/out/$DEVICE.key" /etc/swanctl/rsa/dev.key
	chmod 600 /etc/swanctl/rsa/dev.key
	remote="auth = pubkey"
	secrets=""
	case "$PROTOCOL" in
	ikev2-mschapv2)
		# Windows announces its address, not its username: the server
		# finds the device by the EAP identity alone.
		local="auth = eap-mschapv2
      eap_id = \"$USER\""
		secrets="eap {
    id = \"$USER\"
    secret = \"$PASS\"
  }" ;;
	ikev2-psk)
		remote="auth = psk"
		local="auth = psk
      id = \"$USER\""
		secrets="ike {
    id = \"$USER\"
    secret = \"$PASS\"
  }" ;;
	ikev2-rsa)
		local="auth = pubkey
      certs = dev.crt
      id = \"$USER\"" ;;
	ikev2-eap-tls)
		local="auth = eap-tls
      certs = dev.crt
      id = \"$TLSID\"" ;;
	esac
	cat > /etc/swanctl/conf.d/c.conf <<EOF
connections {
  c {
    version = 2
    remote_addrs = $SERVER
    remote_port = ${PORT:-500}
    encap = ${ENCAP:-no}
    vips = 0.0.0.0
    local {
      $local
    }
    remote {
      $remote
      id = "$SERVERID"
    }
    children {
      c {
        remote_ts = 0.0.0.0/0
      }
    }
  }
}
secrets {
  $secrets
}
EOF
	printf 'charon {\n filelog {\n f {\n path = /tmp/sim/charon.log\n default = 1\n }\n }\n}\n' > /etc/strongswan.d/charon-logging.conf
	mkdir -p /tmp/sim
	/usr/lib/ipsec/charon >/dev/null 2>&1 &
	i=0
	until swanctl --stats >/dev/null 2>&1; do i=$((i + 1)); [ "$i" -gt 50 ] && break; sleep 0.2; done
	swanctl --load-all --noprompt >/tmp/client.log 2>&1
	if ! swanctl --initiate --child c --timeout 20 >>/tmp/client.log 2>&1; then
		check connect no; finish fail
	fi
	check connect yes
	dev=ike
	;;
*)
	log "unknown protocol $PROTOCOL"; exit 2
	;;
esac

if [ "$dev" = ike ]; then
	# No device of its own: the address strongSwan was given sits on the
	# network card, and the kernel's policies carry what it sends.
	got_ip=$(ip -4 -o addr | grep -o "${want_ip%.*}\.[0-9]*" | head -1)
else
	got_ip=$(ip -4 -o addr show dev "$dev" | awk '{print $4}' | cut -d/ -f1)
fi
src=$dev
[ "$dev" = ike ] && src=$got_ip
[ "$got_ip" = "$want_ip" ] && check address yes || check "address($got_ip!=$want_ip)" no

ping -c 2 -W 3 -I "$src" "$GATEWAY" >/dev/null 2>&1 && check gateway yes || check gateway no
ping -c 2 -W 3 -I "$src" "$INTERNET" >/dev/null 2>&1 && check internet yes || check internet no

case "$PROTOCOL" in
wireguard|amneziawg|openvpn|openconnect*)
	# Where a packet for anywhere would go.
	via=$(ip route get 8.8.8.8 2>/dev/null | grep -o "dev [^ ]*" | awk '{print $2}')
	[ "$via" = "$dev" ] && check route yes || check "route(via=$via)" no
	;;
ikev2-*)
	# Everything is sent from the tunnel's address, so into the tunnel.
	from=$(ip route get 8.8.8.8 2>/dev/null | grep -o "src [^ ]*" | awk '{print $2}')
	[ "$from" = "$got_ip" ] && check route yes || check "route(src=$from)" no
	;;
esac

case "$PROTOCOL" in
wireguard|amneziawg)
	grep -q "nameserver $DNS" /etc/resolv.conf && check dns yes || check dns no
	mtu=$(cat /sys/class/net/wg0/mtu)
	[ "$mtu" = "$MTU" ] && check mtu yes || check "mtu($mtu)" no
	;;
openvpn)
	grep "PUSH_REPLY" /tmp/client.log | grep -q "dhcp-option DNS $DNS" && check dns yes || check dns no
	;;
ikev2-*)
	grep -q "DNS.*$DNS\|$DNS.*DNS" /tmp/sim/charon.log /tmp/client.log 2>/dev/null && check dns yes || check dns no
	;;
openconnect*)
	grep -q "nameserver $DNS" /etc/resolv.conf && check dns yes || check dns no
	;;
*)
	grep -q "primary   DNS address $DNS" /tmp/sim/syslog && check dns yes || check dns no
	;;
esac

[ "$bad" = 0 ] && finish ok
finish fail
