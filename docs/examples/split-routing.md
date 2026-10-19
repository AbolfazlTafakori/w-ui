---
description: "Local destinations straight out of this server, everything else through the hop: the rule most operators in a filtered country want."
---

# Local traffic direct, the rest through the hop

With a hop at the top of Outbounds ([all traffic through a clean server](/examples/hop-all-traffic)), local sites take the detour too. That is slower, and some local sites refuse foreign addresses.

**Routing → Routing Rules → Add rule:**

| Field | Value |
|-------|-------|
| IP | `geoip:ir` (your country) |
| Outbound tag | `direct` |

Save. Then open **Routing → Route Tester** and test a local address and a foreign one: the first should go to `direct`, the second to the hop.

For domains rather than addresses, such as a local bank whose addresses move, put the names in the rule's **Domain**; the panel resolves them and keeps them fresh.

Order matters: rules are tried top to bottom, and the first match wins. Drag the rule above any rule that would catch the same traffic first.
