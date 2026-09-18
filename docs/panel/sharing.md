---
description: "Credentials reaching the server from several places at once. One customer is one place at a time; two live public addresses on the same key is the only signal there is, since two devices holding…"
---

# Sharing

Credentials reaching the server from several places at once. One customer is one place at a time; two live public addresses on the same key is the only signal there is, since two devices holding the same key are identical to the tunnel.

It is **reported, never acted on automatically** — a phone moving between wifi and mobile data changes address legitimately. The page lists each suspect with the addresses, when, and how often; you decide. The Telegram notifier can send the same report.

## Behind a relay

Nothing here depends on how traffic reaches the server. Direct, through a forwarder on another machine, a tunnel endpoint, a load balancer, or a relay on the server itself — the panel notices an address that is carrying five or more different customers at once, takes it for a relay, and keeps the port the relay gives each flow, so the report still tells the flows apart. A household behind one router is two or three customers, not five, and is read as one address as before. Nothing to configure.
