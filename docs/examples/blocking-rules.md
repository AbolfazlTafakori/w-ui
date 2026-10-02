---
description: "Block BitTorrent, private ranges, ad domains or whole countries for every customer, in the kernel."
---

# Block ads and BitTorrent

**Routing → Basic Routing.**

- **Block BitTorrent Protocol** is on by default — the ports BitTorrent clients use by default are dropped in the kernel, before any counting, so nothing dropped is billed. A client moved off them is not caught.
- **Block IPs** carries `geoip:private` by default, so customers cannot reach your LAN or the server's own private networks.
- Add to **Block Domains** the names to drop — an ad list's domains paste straight in. The panel resolves them and refreshes them every 15 minutes; it is the addresses that are blocked, so write each name you mean (`www.example.com` as well as `example.com`).
- Add to **Block IPs** an address, a range, or `geoip:xx` for a whole country.
- **Block Ports** stops a port for everyone — `25` keeps customers' spam from being reported against your address.

**Save**; it is applied at once. Check what the kernel holds with:

```bash
nft list chain inet wui_policy wui_block
```

To block for one customer or one group only, use a rule instead: **Routing → Routing Rules → Add rule** — **User** or **Group** the ones to block, **Domain** or **IP** the destinations, **Outbound tag** `blocked`. See [Routing](/panel/routing).
