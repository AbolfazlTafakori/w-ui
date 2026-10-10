# One image for every end: the server runs the panel's own drivers (the
# harness, mounted at /sim/bin), the clients the stock tools a customer's
# computer has, and the "internet" host only answers pings.
FROM debian:12
RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
      wireguard-tools openvpn openresolv iproute2 iputils-ping nftables procps \
 && rm -rf /var/lib/apt/lists/*
COPY client.sh /sim/
RUN chmod +x /sim/*.sh
