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

## Always current

Nothing is stored for the link: every fetch renders the configurations from the records at that moment. Whatever changed in the panel — an endpoint or port, the DNS, a host added, a device renamed, an OpenVPN username or password, a customer put on another server — is in the next fetch, and the answer is sent with `Cache-Control: no-store` so nothing in between keeps an old copy. The customer's page keeps itself current while it is open: it holds a stream to the panel, and the instant anything is changed on the panel — through the API, or by the reconciler ending or starting a plan — the panel writes into that stream and the page asks for its figures and a fingerprint of the files it was made from; the moment the fingerprint differs it loads itself again. (Every three seconds it asks on its own as well, for a network that will not hold a stream open.) So — a device renamed, an address moved, a server added or a username changed is in front of the customer within seconds, with no tap. Usage and the online dot move without a reload; an app refetches on the interval it is told (`Profile-Update-Interval`, **1 hour** by default, set under Settings → Subscription), so a change reaches every app within the hour without anyone being sent a new file.

## Formats

The same token serves apps too: `?format=conf` for a WireGuard file, `base64`, `zip` for every device at once, and the per-device, per-host download the page links to.
