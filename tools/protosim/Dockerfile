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
      xl2tpd pptpd pptp-linux ppp busybox \
 && rm -rf /var/lib/apt/lists/*
COPY systemctl /usr/local/bin/systemctl
COPY client.sh pppclient.sh /sim/
RUN chmod +x /sim/*.sh /usr/local/bin/systemctl
