---
description: "Which ports W-UI uses and ready-made ufw and nftables rules to open only those."
---

# Ports and firewall

Open only what you use.

## Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `22` | TCP | SSH — keep it open |
| *panel port* | TCP | the panel; random at install, `w-ui settings` prints it |
| *per interface* | UDP | each WireGuard / AmneziaWG interface's listen port |
| `443` or *chosen* | TCP or UDP | OpenVPN, per interface |
| `80` | TCP | the ACME challenge, at issue time and at every renewal |
| *subscription port* | TCP | only when the subscription service has a listener of its own |

## ufw

```bash
ufw allow 22/tcp
ufw allow 41234/tcp          # the panel port
ufw allow 51820/udp          # each WireGuard interface
ufw allow 443/tcp            # OpenVPN over TCP, if used
ufw allow 80/tcp             # certificate renewal
ufw --force enable
```

`w-ui` → **23. Firewall Management** does the same from a menu and can read the tunnel ports from the running interfaces.

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
    tcp dport { 22, 80, 41234 } accept
    udp dport { 51820 } accept
    icmp type echo-request accept
  }
}
```

The panel keeps its own tables (`inet wui`, `inet wui_policy`) beside yours and never touches `filter`.

## Cloud firewalls

Hetzner, DigitalOcean, AWS and the rest filter *before* the packet reaches the server. Open the same ports there; the installer cannot do that for you and says so.
