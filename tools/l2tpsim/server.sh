#!/bin/sh
# The server end: the files render wrote, put where the agent puts them, and
# the three daemons started the way the agent starts them.
set -eu

mkdir -p /run/wui-l2tp /etc/swanctl/conf.d /var/run/xl2tpd
cp /conf/options /conf/xl2tpd.conf /conf/l2tp-secrets /run/wui-l2tp/
cp /conf/chap-secrets /etc/ppp/chap-secrets
cp /conf/wui-l2tp.conf /etc/swanctl/conf.d/wui-l2tp.conf
chmod 600 /etc/ppp/chap-secrets /run/wui-l2tp/* /etc/swanctl/conf.d/wui-l2tp.conf

cat > /etc/strongswan.d/charon-logging.conf <<'EOF'
charon {
  filelog {
    sim {
      path = /run/wui-l2tp/charon.log
      default = 1
      ike = 2
      cfg = 2
    }
  }
}
EOF

# pppd says what it negotiated only to syslog.
busybox syslogd -O /run/wui-l2tp/syslog

/usr/lib/ipsec/charon >/dev/null 2>&1 &
i=0
until swanctl --stats >/dev/null 2>&1; do
	i=$((i + 1)); [ "$i" -gt 50 ] && { echo "charon did not start"; exit 1; }
	sleep 0.2
done
swanctl --load-all --noprompt

nft -f /conf/guard.nft

xl2tpd -D -c /run/wui-l2tp/xl2tpd.conf -p /run/wui-l2tp/xl2tpd.pid \
	-C /run/wui-l2tp/l2tp-control >/run/wui-l2tp/xl2tpd.log 2>&1 &

echo "server ready"
exec tail -f /dev/null
