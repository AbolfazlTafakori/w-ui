# AmneziaWG's tools and its userspace daemon, from the release install.sh
# pins: a container has no amneziawg module, so the tunnel runs as it does
# on a server whose kernel has none.
FROM golang:1.26-bookworm AS awg
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends git make gcc libc6-dev >/dev/null \
 && git clone -q --depth 1 -b v3.1.20260812 https://github.com/amnezia-vpn/amneziawg-tools /src/tools \
 && make -s -C /src/tools/src && make -s -C /src/tools/src install DESTDIR=/out WITH_WGQUICK=yes \
 && git clone -q --depth 1 -b v3.1.20260812 https://github.com/amnezia-vpn/amneziawg-go /src/go \
 && cd /src/go && CGO_ENABLED=0 go build -trimpath -o /out/usr/bin/amneziawg-go .

# One image for every end: the server runs the panel's own drivers and PPP
# agent (the harness, mounted at /sim/bin), the clients the stock tools a
# customer's computer or router has, and the "internet" host only answers
# pings. A container has no systemd, so a stand-in answers the agent's
# questions about strongSwan.
FROM debian:12
RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
      wireguard-tools openvpn openresolv iproute2 iputils-ping nftables procps \
      strongswan-charon strongswan-swanctl libstrongswan-extra-plugins \
      libstrongswan-standard-plugins libcharon-extra-plugins libcharon-extauth-plugins \
      xl2tpd pptpd pptp-linux ppp busybox tcpdump python3 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=awg /out/ /
COPY systemctl /usr/local/bin/systemctl
COPY client.sh pppclient.sh /sim/
RUN chmod +x /sim/*.sh /usr/local/bin/systemctl
