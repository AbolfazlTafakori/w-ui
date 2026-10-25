---
description: "The core's own settings: how often traffic is read, what counts as online, how hops are tested, the log, the balancers' live state, the panel's DNS resolver, and the whole configuration as one document."
---

# Engine

The engine is the part of the panel that reads traffic, enforces limits and routes. The Linux kernel does that work, not a proxy process. **Engine** in the sidebar opens **Basics**, **Balancers**, **DNS** and **Advanced**, and two read-only pages, **Generated** and **Logs**.

**Save** at the top keeps the changes. The collection interval and the log format apply at the next restart; everything else applies at once.

## Basics

### General

| Setting | What it does |
|---------|--------------|
| **Overall Routing Strategy** | Which address families the names in routing lists resolve to: **AsIs** (both), **UseIPv4** or **UseIPv6**. |
| **Outbound Test URL** | What the **HTTP** checks on the [Outbounds](/panel/outbounds) page fetch through a hop. Only Cloudflare's trace, the default, reports the exit address and country; any other URL measures latency alone. |

### Statistics

| Setting | What it does |
|---------|--------------|
| **Outbound Traffic Statistics** | Keeps a byte counter per outbound in the kernel, for the Traffic column on the Outbounds page. |
| **Collection Interval** | How often, in seconds, traffic counters are read and limits enforced. A shorter interval cuts customers off closer to the byte and makes speeds livelier, at a little more work for the server. `0` keeps the value from the service's environment. Applies at restart. |
| **Online Window** | How long, in seconds, a device still counts as online on the Interfaces page and in notices after its last handshake; 180 by default. The customer list's *Online* uses live traffic instead; see [Clients](/panel/clients#online). |

### Health Checks

How the outbounds behind [balancers](/panel/routing#balancers) are measured. Leave the defaults unless the checks themselves cause trouble.

| Setting | What it does |
|---------|--------------|
| **Probe Interval** | How often, in seconds, each balancer member is measured. |
| **Concurrent Probing** | Measures every member at once instead of one by one. It is faster, and more visible on the network. |

### Log

| Setting | What it does |
|---------|--------------|
| **Log Level** | The lowest level written: debug, info, warning or error. Empty keeps the default. Applies at once. |
| **Log Format** | Plain text, or one JSON object per line for a log collector. Applies at restart. |
| **Access Log** | Records every request to the panel's API. When it is off, only errors are logged. |
| **Mask Address** | Writes the addresses in the log in a masked form, for logs that leave the server. |

### Reset to Default

After a confirmation, puts every setting on the engine pages back to what a fresh install has, the DNS servers and hosts included. Outbounds, balancers and rules are not touched.

## Balancers

### Balancer Settings

Lists every balancer with two things. **Live Target** is the member carrying traffic right now; ↻ reads it again. **Override** pins the balancer to one member by hand, or leaves it on *Auto (strategy)* so its strategy chooses. A disabled balancer says so.

**Add** and **Edit** open the balancer form:

- **Tag**: the balancer's name, which rules point at.
- **Strategy**: random or leastPing; see [Routing](/panel/routing#balancers).
- **Selector**: the member outbounds.
- **Fallback**: the hop that carries the traffic while no member is up. It has to be an outbound of its own; a balancer cannot fall back to another balancer.

A balancer that rules point at cannot be deleted until those rules are changed.

### Observatory

These are the checks that tell a leastPing balancer which member is fastest. They run by themselves once a balancer exists, and they watch exactly the balancers' members. **Probe URL** is what they fetch, the same as the Outbound Test URL. **Probe Interval** and **Concurrent Probing** are the ones under Health Checks.

## DNS

The panel runs its own resolver on every interface's gateway address, port 53, and gives it to customers as their DNS. Their lookups are answered on the tunnel and leave by the right outbound.

### General

| Setting | What it does |
|---------|--------------|
| **Enable DNS** | On by default. When it is off, customers' files name each interface's own DNS setting instead. |
| **Answering On** | The tunnel addresses the resolver is bound to right now. It reads *not listening* until the settings are saved with at least one server. A file issued while the resolver is on names its tunnel's gateway as DNS, unless the resolver could not open port 53 there (something else holds it, or the service lacks the right to bind it): that tunnel's files then name the interface's own DNS, so customers are never handed a resolver that does not answer. |
| **Query Strategy** | **UseIP** (both families), **UseIPv4** or **UseIPv6**. The default is IPv4 only, because the tunnels carry IPv4. A name that also came back with an IPv6 address made a phone try that first, wait for it to fail and only then load the page, or not load it at all in an app that does not fall back. |
| **Disable cache** | Turns caching off. |
| **Serve Expired TTL** | For how many seconds a stale cached answer may be served while a fresh one is fetched. `0` never serves a stale answer. |

DNS can leak through a device's old resolver, through plain UDP or TCP, or through fallback queries. Where privacy matters, use DNS-over-HTTPS servers, host pins and **Skip Fallback**.

### Hosts

Pins a domain to the addresses you give, for a service you host or one whose answer you want to override. The domain is `example.com`, or `*.example.com` for every name under it.

### Servers

Where queries go. **Add Server** asks for:

- an **Address**: `8.8.8.8`, or a DNS-over-HTTPS URL such as `https://cloudflare-dns.com/dns-query`;
- a **Port**;
- the **Domains** it answers for. Empty means every name, so you can send some names to one resolver and the rest to another;
- **Skip Fallback**: the server is not tried when its own domains fail.

**Use Preset** adds a known set: Google, Cloudflare, AdGuard, AdGuard Family, Cloudflare Family or Cloudflare DoH. **Delete All** empties the list. A fresh install starts with `1.1.1.1` and `8.8.8.8`.

Two things need no setting. Upstream queries carry the default outbound's mark, so DNS leaves the same way the traffic does. And every TCP connection through a tunnel has its MSS clamped to the tunnel's MTU, so a site behind a load balancer that drops "fragmentation needed" messages does not stall on large responses.

## Advanced

**Advanced Configuration Template** shows the whole panel configuration as one JSON document: **All**, or one part, **Interfaces**, **Outbounds** or **Routing Rules**. Copy it, keep it in version control, or edit it and apply it. Applying is atomic: every part is accepted, or nothing changes. Rows are matched by name or tag, and nothing is deleted by leaving it out of the document.

**Kernel Programs** shows, read-only and as text, the programs the panel asks the kernel to run. They are rewritten from the database on every pass, so this is where you check what is really in force.

## Generated

What the panel is asking the machine to do, as text, so that a server can be debugged from what it really runs and not from the panel's own account of it. **Which program** picks one of three, each marked *active* or *not applied*:

| Program | What it is |
|---------|------------|
| **Quotas** | The nftables program that counts usage and applies limits in the kernel. |
| **Routing** | The marks and tables that steer traffic to its outbound. |
| **Speed limits** | The per-customer queues. |

The panel rewrites them from the database on every pass, and nothing here can be edited. **Copy** takes the text; a browser refuses the clipboard on a plain-HTTP page.

## Logs

The panel's last 300 lines, filtered by **Level**, with **Refresh**. The **Logs** window on the [Overview](/panel/overview#the-log) has more: search, the system journal and following the log live.
