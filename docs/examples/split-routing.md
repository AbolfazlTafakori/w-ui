---
description: "Local destinations straight out of this server, everything else through the hop : the rule most operators in a filtered country want."
---

# Local traffic direct, the rest through the hop

With a hop at the top of Outbounds ([all traffic through a clean server](/examples/hop-all-traffic)), local sites take the detour too — slower, and sometimes they refuse foreign addresses.

**Routing → Rules → + Rule:**

| Field | Value |
|-------|-------|
| Destination | `geoip:ir` (your country) |
| Outbound | `direct` |

Save. Routing → **Route tester**: enter a local address and a foreign one; the first should say `direct`, the second the hop.

For domains rather than addresses — a local bank whose addresses move — put the names themselves in the destination; the panel resolves them through its DNS proxy and keeps the sets fresh.

Order matters: rules are tried top to bottom, and the first match wins.
