---
description: "What xray#basic, #balancer, #dns and #advanced are on 3x-ui, for a panel whose engine is the kernel rather than Xray. The sidebar entry Engine opens four pages."
---

# Engine

What `xray#basic`, `#balancer`, `#dns` and `#advanced` are on 3x-ui, for a panel whose engine is the kernel rather than Xray. The sidebar entry **Engine** opens four pages.

## Basics

Five inner tabs:

- **General** — domain strategy for rules that name domains, the outbound test URL, the collection interval, whether per-outbound counters are kept, the online window.
- **Statistics** — what is counted and how long it is kept.
- **Health checks** — how the outbounds behind balancers are measured: probe interval, concurrency, what counts as down.
- **Log** — level, format, whether an access log is written, whether addresses are masked in it.
- **Reset to default** — every engine setting back to the defaults.

## Balancers

See [Outbounds → Balancers](/panel/outbounds#balancers).

## DNS

The panel runs a resolver on every tunnel's gateway address, port 53, so customers' DNS is answered on the tunnel and leaves by the right outbound:

- **Hosts** — pins: a name to an address, for a service you host or one you want to override;
- **Upstreams** — where queries go, with per-domain routing (`geosite:ir` to one resolver, the rest to another), DNS-over-HTTPS supported;
- **Cache** — TTL handling and serving stale answers while an upstream is slow.

Upstream queries carry the default outbound's mark, so DNS goes out the same way the traffic does.

## Advanced

The whole configuration as one document — export it, edit it, apply it — for moving a panel or keeping a copy in version control. Applied atomically: either every section is accepted or nothing changes.
