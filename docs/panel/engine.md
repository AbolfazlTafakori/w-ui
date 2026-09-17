---
description: "The core's own knobs — basics, balancers, DNS, advanced — for a panel whose engine is the kernel rather than Xray. The sidebar entry Engine opens four pages."
---

# Engine

The core's own knobs — basics, balancers, DNS, advanced — for a panel whose engine is the kernel rather than Xray. The sidebar entry **Engine** opens four pages.

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

**On by default**, with `1.1.1.1` and `8.8.8.8` upstream and the query strategy **IPv4 only**. The tunnels carry IPv4; a name that also came back with an IPv6 address made a phone try that first, wait for it to fail, and only then load the page — or not load it at all in an app that does not fall back. Answering with IPv4 only is what makes every site open first time. Every customer's file names the tunnel's gateway as its DNS while the resolver is on; switch it off and the file names the interface's own DNS setting instead.

Beside it, every TCP connection through a tunnel has its MSS clamped to the tunnel's MTU at the handshake, so a site behind a load balancer that drops "fragmentation needed" does not stall on its large responses. Neither needs a setting.

## Advanced

The whole configuration as one document — export it, edit it, apply it — for moving a panel or keeping a copy in version control. Applied atomically: either every section is accepted or nothing changes.
