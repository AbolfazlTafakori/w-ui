---
description: "The core's own settings — how often traffic is read, what counts as online, how hops are tested, the log, the balancers' live state, the panel's DNS resolver, and the whole configuration as one document."
---

# Engine

The engine is the part of the panel that reads traffic, enforces limits and routes — done by the Linux kernel, not by a proxy process. **Engine** in the sidebar opens **Basics**, **Balancers**, **DNS** and **Advanced**, and two read-only pages, **Generated** and **Logs**.

**Save** at the top keeps the changes. The collection interval and the log format apply at the next restart; everything else at once.

## Basics

### General

| Setting | What it does |
|---------|--------------|
| **Overall Routing Strategy** | Which address families the names in routing lists resolve to: **AsIs** (both), **UseIPv4** or **UseIPv6**. |
| **Outbound Test URL** | What **HTTP** checks on the [Outbounds](/panel/outbounds) page fetch through a hop. Only Cloudflare's trace (the default) reports the exit address and country; any other URL measures latency alone. |

### Statistics

| Setting | What it does |
|---------|--------------|
| **Outbound Traffic Statistics** | Keeps a byte counter per outbound in the kernel, for the Traffic column on the Outbounds page. |
| **Collection Interval** | How often traffic counters are read and limits enforced, in seconds. Shorter cuts customers off closer to the byte and makes speeds livelier, at a little more work for the server. 0 keeps the value from the service's environment. At restart. |
| **Online Window** | How long after a device's last handshake it still counts as online on the Interfaces page and in notices, in seconds (180 by default). The customer list's *Online* uses live traffic instead — see [Clients](/panel/clients#online). |

### Health Checks

How the outbounds behind [balancers](/panel/routing#balancers) are measured. Leave the defaults unless the checks themselves cause trouble.

| Setting | What it does |
|---------|--------------|
| **Probe Interval** | How often each balancer member is measured, in seconds. |
| **Concurrent Probing** | Measures every member at once instead of one by one: faster, but more visible on the network. |

### Log

| Setting | What it does |
|---------|--------------|
| **Log Level** | The lowest level written: debug, info, warning or error; empty keeps the default. Applies at once. |
| **Log Format** | Plain text, or one JSON object per line for a log collector. At restart. |
| **Access Log** | Records every request to the panel's API. Off: only errors are logged. |
| **Mask Address** | Replaces the addresses in the log with a masked form, for logs that leave the server. |

### Reset to Default

Puts every setting on the engine pages back to what a fresh install has, the DNS servers and hosts included, after a confirmation. Outbounds, balancers and rules are not touched.

## Balancers

### Balancer Settings

Every balancer, with its **Live Target** — the member carrying traffic right now (↻ reads it again) — and **Override**: pin the balancer to one member by hand, or *Auto (strategy)* to let its strategy choose. A disabled balancer says so.

**Add** / **Edit** open the balancer form: **Tag**, **Strategy** (random or leastPing — see [Routing](/panel/routing#balancers)), **Selector** (the member outbounds) and **Fallback** — the hop that carries the traffic while no member is up. The fallback has to be an outbound of its own; a balancer cannot fall back to another balancer. A balancer that rules point at cannot be deleted until they are changed.

### Observatory

The checks that tell a leastPing balancer which member is fastest. They run by themselves once a balancer exists, and watch exactly the balancers' members. **Probe URL** is what they fetch (the Outbound Test URL); **Probe Interval** and **Concurrent Probing** are those of Health Checks.

## DNS

The panel runs its own resolver on every interface's gateway address, port 53, and names it as the customers' DNS, so their lookups are answered on the tunnel and leave by the right outbound.

### General

| Setting | What it does |
|---------|--------------|
| **Enable DNS** | On by default. Off: customers' files name each interface's own DNS setting instead. |
| **Answering On** | The tunnel addresses the resolver is bound to right now; *not listening* until saved with at least one server. A file issued while this is on names its tunnel's gateway as DNS. |
| **Query Strategy** | **UseIP** (both families), **UseIPv4** or **UseIPv6**. IPv4 only by default: the tunnels carry IPv4, and a name that also came back with an IPv6 address made a phone try that first, wait for it to fail and only then load the page — or not load it at all in an app that does not fall back. |
| **Disable cache** | Turns caching off. |
| **Serve Expired TTL** | For how many seconds a stale cached answer may be served while a fresh one is fetched; 0 never serves stale. |

DNS can leak through a device's old resolver, plain UDP or TCP, or fallback queries. Where privacy matters, use DNS-over-HTTPS servers, host pins, and **Skip Fallback**.

### Hosts

Pins: a domain (`example.com`, or `*.example.com` for every name under it) answered with the addresses you give, for a service you host or one you want to override.

### Servers

Where queries go. **Add Server**: an **Address** (`8.8.8.8`, or a DNS-over-HTTPS URL such as `https://cloudflare-dns.com/dns-query`), a **Port**, the **Domains** it answers for (empty: every name — so `geosite`-style splits, one resolver for some names and another for the rest, are possible), and **Skip Fallback** (not used when its own domains fail). **Use Preset** adds a known set: Google, Cloudflare, AdGuard, AdGuard Family, Cloudflare Family, Cloudflare DoH. **Delete All** empties the list. A fresh install starts with `1.1.1.1` and `8.8.8.8`.

Upstream queries carry the default outbound's mark, so DNS leaves the same way the traffic does. Every TCP connection through a tunnel also has its MSS clamped to the tunnel's MTU, so a site behind a load balancer that drops "fragmentation needed" does not stall on large responses; neither needs a setting.

## Advanced

**Advanced Configuration Template**: the whole panel configuration as one JSON document — **All**, or one part: **Interfaces**, **Outbounds**, **Routing Rules** — to copy, keep in version control, or edit and apply. Applying is atomic: every part is accepted or nothing changes. Rows are matched by name or tag; nothing is deleted by leaving it out. **Kernel Programs** shows, read-only and as text, the programs the panel is asking the kernel to run, rewritten from the database on every pass — for checking what is really in force.

## Generated

What the panel is asking the machine to do, as text — so a server can be debugged from what it is really running, not from the panel's own account of it. **Which program** picks one of three, each marked *active* or *not applied*:

| Program | What it is |
|---------|------------|
| **Quotas** | The nftables program that counts usage and applies limits in the kernel. |
| **Routing** | The marks and tables that steer traffic to its outbound. |
| **Speed limits** | The per-customer queues. |

The panel rewrites them from the database on every pass; nothing here can be edited. **Copy** takes the text (a browser refuses the clipboard on a plain-HTTP page).

## Logs

The panel's last 300 lines, filtered by **Level**, with **Refresh**. The **Logs** window on the [Overview](/panel/overview#the-log) has more: search, the system journal and following live.
