---
description: "A Cloudflare WARP outbound in one click, for destinations that block your server's address."
---

# A Cloudflare WARP outbound

**Outbounds → more → WARP.** The panel registers a WARP identity with Cloudflare, builds a WireGuard outbound from it, and adds it to the list. Check it: the egress country becomes Cloudflare's.

Use it as the default (move it to the top) or only for some destinations:

Routing → Rules → **+ Rule**: destination the domains you need (one per line), outbound `warp`. Everything else keeps its route.

WARP's free tier is meant for a person, not a server; for heavy use, a paid hop of your own.
