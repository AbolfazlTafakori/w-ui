---
description: "Coming from another panel: what each concept is called here, what to expect, and how to bring a 3x-ui or Pasarguard panel's users along."
---

# Coming from another panel

The pages are in the places you expect. The words map like this:

| Xray panels (3x-ui) | Pasarguard, Marzban | W-UI |
|-------|---------|------|
| Inbound | Inbound (core config) | **Interface**: one tunnel, in WireGuard, AmneziaWG, OpenVPN, OpenConnect, IKEv2, L2TP/IPsec or PPTP |
| Client (email) | User | **Client**: a customer, with one **account** per device |
| Client's `id` or password | User's proxies | The device's key (WireGuard) or username and password (the other protocols) |
| Total GB, Expiry, IP limit | Data limit, Expire | Data allowance, Valid for, **Users** |
| Subscription URL | Subscription URL | Subscription link: the same idea, with a customer page |
| Hosts | Host settings | Hosts and host groups |
| Outbounds, Routing, Balancers | Core settings | Outbounds, Routing, Balancers: the same tabs |
| Xray settings | Core settings | Engine: basics, balancers, DNS, advanced |
| Numbered menu | CLI | The `w-ui` menu, with the same numbering |
| Restart Xray | Restart core | Restart tunnels |
| Nodes | Nodes | Nodes: other W-UI, 3x-ui or Pasarguard panels, managed over their API |
| Admins | Admins | Owner, panel administrators and [resellers](/panel/operators) |
| Telegram bot | Telegram bot | Telegram bot |

## What is different

- **The protocols.** W-UI runs VPN tunnels itself, not Xray. For VLESS, VMess, Trojan, Shadowsocks or REALITY, keep an Xray panel and add it as a [node](/panel/nodes): its inbounds appear here, and one subscription carries both. Where a plain VPN is filtered, AmneziaWG, OpenConnect on 443 and OpenVPN over TCP 443 are the options that run here.
- **Enforcement in the kernel.** A quota overshoots by one packet, not by a polling interval.
- **A default outbound that fails closed.** When the hop everything leaves through is down, traffic is dropped rather than leaked from the server's own address.
- **A certificate for the IP.** HTTPS without a domain, from the first minute.

## Bringing customers along

**From a 3x-ui or Pasarguard panel:** add it as a node, then use **Bring in users** on its row. Its users keep their credentials, their links and their subscription; nothing changes for them. Their allowance, usage and expiry are managed here from then on, and you can give them W-UI tunnels as well. See [Bringing in a panel's own users](/panel/nodes#bringing-in-a-panel-s-own-users).

**From anywhere else:** there is no importer for another panel's database, because the credentials are different kinds of thing. Create the customers here, where bulk creation takes a prefix and a count, and hand out their new subscription links.

**From another W-UI:** a [backup](/operations/backup-restore) moves everything, keys included, and **Import clients** moves a customer list with new keys.
