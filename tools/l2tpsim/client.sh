#!/bin/sh
# One device. The environment says which: the IPsec proposals it offers (none
# for a router without IPsec), the tunnel password a modem may ask the server
# to prove, and the PPP options that make its sign-in what that device's is.
#
#   NAME     scenario name, for the result line
#   SERVER   the server's address
#   PSK      pre-shared key; empty is a client without IPsec
#   IKE ESP  the proposals offered, in strongSwan's notation
#   USER PASS
#   PPPOPTS  extra pppd options, one per line (\n-separated)
#   TUNNEL   a tunnel password the client asks the server to prove; empty for none
#   PPTP     1 for a PPTP client instead of L2TP
#   EXPECT   ok or fail
set -u

log() { echo "[$NAME] $*"; }
mkdir -p /var/run/xl2tpd /tmp/sim
busybox syslogd -O /tmp/sim/syslog

detail=""
finish() {
	got=$1
	if [ "$got" = "$EXPECT" ]; then verdict=PASS; else verdict=FAIL; fi
	echo "RESULT $verdict $NAME expected=$EXPECT got=$got $detail"
	if [ "$verdict" = FAIL ] || [ "${VERBOSE:-}" = 1 ]; then
		echo "---- charon"; tail -n 40 /tmp/sim/charon.log 2>/dev/null
		echo "---- syslog"; tail -n 60 /tmp/sim/syslog 2>/dev/null
	fi
	[ "$verdict" = PASS ]
	exit $?
}

if [ -n "$PSK" ]; then
	cat > /etc/strongswan.d/charon-logging.conf <<'EOF'
charon {
  filelog {
    sim {
      path = /tmp/sim/charon.log
      default = 1
      ike = 2
    }
  }
}
EOF
	cat > /etc/swanctl/conf.d/sim.conf <<EOF
connections {
  sim {
    version = 1
    remote_addrs = $SERVER
    proposals = $IKE
    local { auth = psk }
    remote { auth = psk }
    children {
      sim {
        mode = transport
        local_ts = dynamic[17/1701]
        remote_ts = dynamic[17/1701]
        esp_proposals = $ESP
      }
    }
  }
}
secrets {
  ike-sim { secret = "$PSK" }
}
EOF
	/usr/lib/ipsec/charon >/dev/null 2>&1 &
	i=0
	until swanctl --stats >/dev/null 2>&1; do
		i=$((i + 1)); [ "$i" -gt 50 ] && { detail="charon did not start"; finish fail; }
		sleep 0.2
	done
	swanctl --load-all --noprompt >/dev/null
	if ! swanctl --initiate --child sim --timeout 20 >/tmp/sim/initiate.log 2>&1; then
		detail="ipsec=refused"
		finish fail
	fi
	detail="ipsec=up($(swanctl --list-sas 2>/dev/null | grep -o -m1 -E '(AES_[A-Z0-9_]+|3DES_CBC)[^,]*' | head -1))"
fi

if [ "${PPTP:-}" = 1 ]; then
	{
		echo "pty \"pptp $SERVER --nolaunchpppd\""
		echo "name $USER"
		echo "user $USER"
		echo "password $PASS"
		echo "remotename wui-pptp"
		echo "noauth"
		echo "nodefaultroute"
		echo "noipdefault"
		echo "nobsdcomp"
		echo "nodeflate"
		echo "debug"
		printf '%b\n' "${PPPOPTS:-}"
	} > /tmp/sim/options
	pppd file /tmp/sim/options >/tmp/sim/pppd.log 2>&1 &
else
{
	echo "[global]"
	echo "port = 1701"
	if [ -n "$TUNNEL" ]; then echo "auth file = /tmp/sim/l2tp-secrets"; fi
	echo "[lac sim]"
	echo "lns = $SERVER"
	echo "pppoptfile = /tmp/sim/options"
	echo "length bit = yes"
	echo "require authentication = no"
	echo "redial = no"
	if [ -n "$TUNNEL" ]; then echo "challenge = yes"; fi
} > /tmp/sim/xl2tpd.conf
if [ -n "$TUNNEL" ]; then echo "* * $TUNNEL" > /tmp/sim/l2tp-secrets; chmod 600 /tmp/sim/l2tp-secrets; fi
{
	echo "name $USER"
	echo "user $USER"
	echo "password $PASS"
	echo "noauth"
	echo "nodefaultroute"
	echo "noipdefault"
	echo "mtu 1280"
	echo "mru 1280"
	echo "debug"
	printf '%b\n' "${PPPOPTS:-}"
} > /tmp/sim/options

xl2tpd -D -c /tmp/sim/xl2tpd.conf -p /tmp/sim/xl2tpd.pid -C /var/run/xl2tpd/l2tp-control >/tmp/sim/xl2tpd.log 2>&1 &
sleep 1
echo "c sim" > /var/run/xl2tpd/l2tp-control
fi

i=0
addr=""
while [ "$i" -lt 25 ]; do
	addr=$(ip -4 -o addr show dev ppp0 2>/dev/null | awk '{print $4}')
	[ -n "$addr" ] && break
	i=$((i + 1)); sleep 1
done
if [ -z "$addr" ]; then
	detail="$detail ppp=down"
	cat /tmp/sim/xl2tpd.log /tmp/sim/pppd.log >> /tmp/sim/syslog 2>/dev/null
	finish fail
fi
mppe=$(grep -o -m1 -E 'MPPE [0-9]+-bit[^"]*enabled' /tmp/sim/syslog || true)
detail="$detail ppp=$addr ${mppe:+mppe=\"$mppe\"}"
if ping -c 2 -W 3 -I ppp0 10.77.0.1 >/dev/null 2>&1; then
	finish ok
fi
detail="$detail ping=lost"
finish fail
