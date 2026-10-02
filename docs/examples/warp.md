---
description: "A Cloudflare WARP outbound in one click, for destinations that block your server's address."
---

# A Cloudflare WARP outbound

**Outbounds → more → WARP → Create WARP account**, then **Add outbound**. The panel registers a WARP device with Cloudflare and turns it into a WireGuard outbound. **Check** it with **HTTP**: the egress country becomes Cloudflare's. **Change IP** asks Cloudflare for another address; a WARP+ key goes in **WARP / WARP+ license key**.

Use it as the default (row menu → **Move to top**), or only for some destinations: **Routing → Routing Rules → Add rule**, **Domain** the names you need, **Outbound tag** the WARP outbound. Everything else keeps its route.

WARP's free tier is meant for a person, not a server; for heavy use, a paid hop of your own.
