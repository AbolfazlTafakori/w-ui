---
description: "Why a quota enforced by the kernel overshoots by one packet where a polled counter overshoots by megabytes, and how the reconciler keeps kernel state equal to the database."
---

# How enforcement works

## The difference from a polling panel

Most panels poll a byte counter every couple of seconds and disable the customer once the number crosses their quota. Between two polls the customer keeps transferring at full speed. On a 100 Mbit link that is roughly 25 MB of overshoot; on a gigabit link, 250 MB. If you sell a 1 GB plan, a fast customer silently gets 1.25 GB.

W-UI programs the limit into the kernel itself, as an `nftables` quota object attached to the customer's address:

```
quota q_c14 { over 1073741824 bytes used 402653184 bytes }
```

The kernel drops the packet that crosses the ceiling. The panel is not in the data path, so the overshoot is one packet rather than one polling interval, and it stays that way whether the customer has a 10 Mbit or a 10 Gbit link.

The panel still polls — but only to *read* usage for the interface and to decide when to remove a peer, never to enforce the byte limit.

## The reconciler

The database is the source of truth. Kernel state is derived from it and rebuilt to match, rather than mutated as events arrive. Every two seconds:

1. **Collect** — atomically read and zero the nftables counters, then write usage to the database through a single serialised writer.
2. **Evaluate** — decide in SQL which customers are now over quota or past expiry.
3. **Apply** — push the desired peer set and the desired ruleset to the kernel.

So the panel is self-healing: if the machine reboots, someone flushes nftables by hand, or the panel is killed mid-write, the next tick rebuilds everything from the database. And a panel restart drops nobody — the driver never deletes or recreates a link or a peer that is already correct.

## Design notes

- **Peers are diffed, never replaced.** WireGuard keeps session state per peer; replacing the whole list on every tick tears down live handshakes, and the tunnel then looks connected from both ends while moving no traffic. The driver sends only what changed.
- **Limits cover traffic to the panel's own host, not just through it.** Packets that end on this machine never reach the forward hook; without an input rule a resolver running here would carry customer data for free and a cut-off customer would still reach every service on the box.
- **The quota drop comes before the counter.** Otherwise dropped bytes would be billed to the customer who never received them.
- **Speed limits classify by mark, not by filter.** The nftables chain that already exists per customer stamps the packet with its HTB class; the cost of classifying stays flat as customers are added.
- **Egress is policy routing.** Each outbound is a marked routing table; the default outbound's mark goes on everything unmatched, DNS included, and when that outbound is down the customers' traffic is dropped rather than leaked from the server's own address.

## What needs the kernel

`nft_quota` for exact limits, HTB (`sch_htb`) for speed limits, `wireguard` for kernel WireGuard; AmneziaWG falls back to a userspace implementation when its module cannot be built. The installer tests each and says so; the Overview page's **Enforcement** line says which mode you are in.
