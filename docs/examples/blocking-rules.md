---
description: "Block BitTorrent, private ranges, ad domains or whole countries for every customer, in the kernel."
---

# Block ads and BitTorrent

**Routing → Basic.**

- **Block BitTorrent** is on by default — the well-known ports are dropped in `wui_block`, before any counting, so nothing dropped is billed.
- **Blocked addresses** carries `geoip:private` by default, so customers cannot reach your LAN.
- Add to **Blocked domains**: the names to drop, one per line (an ad-list's domains paste straight in). Names are resolved and refreshed; the addresses go into the `blocked4` / `blocked6` sets.
- Add to **Blocked addresses**: a CIDR, an address, `geoip:xx` for a country.

Save; the ruleset is applied at once. Check with:

```bash
nft list chain inet wui_policy wui_block
```

To block for one customer only, or one group, use a rule instead: Routing → Rules → source *the group*, destination *the domains*, outbound **blocked**.
