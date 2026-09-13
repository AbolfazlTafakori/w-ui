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

Settings → Subscription → **Template** picks one of six looks: *classic* (3x-ui's own layout), *aurora*, *waves*, *network* (an animated canvas), *minimal*, *midnight*. **Preview** opens a sample page in the chosen template through a one-time link, so you see it before customers do.

## The link

`https://host:port/sub/TOKEN`. The token is per customer and can be rotated from the client's row; the old link stops working at once. When the subscription service sits on its own port, the link carries that port and scheme.

## Formats

The same token serves apps too: `?format=conf` for a WireGuard file, `base64`, `zip` for every device at once, and the per-device, per-host download the page links to.
