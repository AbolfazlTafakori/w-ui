---
description: "Coming from another panel : what each concept is called here and what to expect."
---

# Coming from another panel

The pages are in the same places. The words map like this:

| Xray panels | Marzban | W-UI |
|-------|---------|------|
| Inbound | Inbound (Xray config) | **Interface** — one WireGuard / AmneziaWG / OpenVPN tunnel |
| Client (email) | User | **Client** — a customer, with one **account** per device |
| Client's `id` / password | User's proxies | the device's key (WireGuard) or username + password (OpenVPN) |
| Total GB, Expiry, IP limit | Data limit, Expire, — | Total traffic, Expiry, **Device limit** |
| Subscription URL | Subscription URL | Subscription link — same idea, with a customer page |
| Hosts | Host settings | Hosts and host groups |
| Outbounds / Routing / Balancers | Core settings | Outbounds / Routing / Balancers — same tabs |
| Xray settings | Core settings | Engine (basics, balancers, DNS, advanced) |
| numbered menu | `marzban` CLI | `w-ui` menu, the same numbering |
| Restart Xray | Restart core | Restart tunnels |
| Nodes (multi-node) | Marzban-node | Nodes — other W-UI panels watched over the API |
| Telegram bot | Telegram bot | Telegram bot |

## What has no equivalent

- **REALITY, VLESS, Trojan, Shadowsocks** — not here; this panel runs WireGuard and OpenVPN. Where those protocols are needed for DPI, AmneziaWG is the obfuscated option.
- **Multiple admins** — one administrator per panel.

## What is new to you

- **Enforcement in the kernel** — quota overshoot is one packet, not a polling interval.
- **Fail-closed default outbound** — when the hop everything leaves through is down, traffic is dropped rather than leaked from the server's own address.
- **A certificate for the IP** — HTTPS without a domain, from the first minute.

## Moving customers

There is no importer for another panel's database: the credentials are different kinds of thing (Xray UUIDs vs WireGuard keys). Create the customers here — bulk creation takes a prefix and a count — and hand out new configs.
