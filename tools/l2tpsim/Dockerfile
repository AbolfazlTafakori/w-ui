# One image for both ends: the packages the agent installs on a customer's
# server (strongSwan with swanctl, xl2tpd, pppd), and the same three as the
# client every scenario imitates a device with.
FROM debian:12
RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
      strongswan-charon strongswan-swanctl libstrongswan-extra-plugins \
      xl2tpd ppp iproute2 iputils-ping nftables procps busybox \
 && rm -rf /var/lib/apt/lists/*
COPY server.sh client.sh /sim/
RUN chmod +x /sim/*.sh
