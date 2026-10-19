---
description: "Which ports W-UI uses, per protocol, and ready-made ufw and nftables rules to open only those."
---

# Ports and firewall

Open only what you use.

## Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `22` | TCP | SSH. Keep it open. |
| *panel port* | TCP | The panel. Random at install; `w-ui settings` prints it. |
| `80` | TCP | The ACME challenge, at issue time and at every renewal. |
| *subscription port* | TCP | Only when the subscription service has a listener of its own. |
| *per interface* | UDP | Each WireGuard or AmneziaWG interface's listen port. |
| `443`, or as chosen | TCP or UDP | OpenVPN, per interface. |
| `443`, or as chosen | TCP and UDP | OpenConnect: TLS on TCP, and DTLS on UDP on the same number. |
| `500`, `4500` | UDP | IKEv2, and the IPsec under L2TP. |
| `1701` | UDP | L2TP, inside IPsec. |
| ESP (IP protocol 50) | | IPsec for a client not behind NAT. Behind NAT it travels inside UDP 4500. |
| `1723` | TCP | PPTP's control connection. |
| GRE (IP protocol 47) | | PPTP's data. Without it, a PPTP client connects and then carries nothing. |
| *own port* | as the protocol | An L2TP, PPTP or IKEv2 tunnel given a port of its own. The standard port stays needed too; see [Interfaces](/panel/interfaces#ports-of-their-own-l2tp-pptp-and-ikev2). |

## ufw

Open the lines for the protocols you run:

```bash
ufw allow 22/tcp
ufw allow 41234/tcp          # the panel port
ufw allow 80/tcp             # certificate renewal
ufw allow 51820/udp          # each WireGuard interface
ufw allow 443/tcp            # OpenVPN over TCP, or OpenConnect
ufw allow 443/udp            # OpenConnect's DTLS
ufw allow 500,4500/udp       # IKEv2 and L2TP/IPsec
ufw allow 1701/udp           # L2TP
ufw allow proto esp from any # IPsec without NAT
ufw allow 1723/tcp           # PPTP
ufw allow proto gre from any # PPTP's data
ufw --force enable
```

`w-ui` → **23. Firewall** does the same from a menu, and can read the tunnel ports from the running interfaces.

::: warning
Allow SSH before enabling a default-deny policy, and test from a second session so you do not lock yourself out.
:::

## nftables

```nft [/etc/nftables.conf]
table inet filter {
  chain input {
    type filter hook input priority 0; policy drop;
    ct state established,related accept
    iif lo accept
    tcp dport { 22, 80, 443, 1723, 41234 } accept
    udp dport { 443, 500, 1701, 4500, 51820 } accept
    meta l4proto { esp, gre } accept
    icmp type echo-request accept
  }
}
```

The panel keeps its own tables (`inet wui`, `inet wui_policy`) beside yours and never touches `filter`.

## Cloud firewalls

Hetzner, DigitalOcean, AWS and the rest filter *before* the packet reaches the server. Open the same ports there, and GRE and ESP where the provider lists them. The installer cannot do that for you, and says so.
