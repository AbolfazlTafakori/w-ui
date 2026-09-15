---
description: "The page a customer sees when they open their subscription link in a browser. It is server-rendered, so it works without JavaScript and loads fast on a bad connection."
---

# Subscription page

The page a customer sees when they open their subscription link in a browser. It is server-rendered, so it works without JavaScript and loads fast on a bad connection.

## What is on it

- how much of the plan is used, how much is left, when it ends, when they were last online;
- one block per device **per host** — the config text, a copy button, a download, and a QR code that opens centred over the page;
- the subscription link itself as a QR, for apps that speak subscriptions;
- a language switch (English / Persian) and a theme switch;
- the support link, the announcement and the profile title from Settings → Subscription.

## Templates

Settings → Subscription → **Template** picks one of twenty-one looks. **Preview** opens a sample page in the chosen template through a one-time link, so you see it before customers do.

| Template | Look |
| --- | --- |
| `classic` | the classic panel layout |
| `aurora` | red, blue and teal light drifting over dark navy |
| `waves` | slow gradient waves under a glass card |
| `network` | an animated canvas of connected points |
| `minimal` | white, flat, no decoration |
| `midnight` | pure black with a red glow |
| `ember` | charcoal with a warm fire glowing up from below |
| `ocean` | deep water, a slow cyan surface, a glass card |
| `forest` | dark moss with emerald light through the canopy |
| `sunset` | violet to amber across the sky, a sun on the horizon |
| `neon` | black with magenta and cyan edges that glow |
| `glass` | frosted white over a colourful blur (light) |
| `paper` | warm cream, serif headings, ruled lines (light) |
| `terminal` | green phosphor on black, scanlines, a prompt |
| `carbon` | woven carbon under brushed steel |
| `royal` | navy and gold, a gilded card edge |
| `sakura` | white and blossom pink, petals drifting down (light) |
| `frost` | ice blue and white (light) |
| `graphite` | flat grey surfaces, one blue, big radii |
| `mesh` | a slow multicolour mesh behind dark glass |
| `retro` | synthwave: a striped sun and a grid running to the horizon |

The page refreshes itself every few seconds while it is open: usage, remaining data, the bar and the rings, downloaded/uploaded, last online, and the dot in the header — **Online** while a device has a fresh handshake, **Idle** when the plan is active but nothing is connected, **Off** when the plan is not active.

Every look keeps the same content and the same controls — the theme toggle, the language, the download and copy buttons — only the dress changes.

## The link

`https://host:port/sub/TOKEN`. The token is per customer and can be rotated from the client's row; the old link stops working at once. When the subscription service sits on its own port, the link carries that port and scheme.

## Formats

The same token serves apps too: `?format=conf` for a WireGuard file, `base64`, `zip` for every device at once, and the per-device, per-host download the page links to.
