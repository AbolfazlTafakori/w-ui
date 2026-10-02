---
description: "Which traffic goes where: the default outbound, what is blocked or sent direct, ordered rules by customer, group, interface, address, domain and port, balancers over several hops, and a tester that tells you where a connection would go."
---

# Routing

Routing decides which [outbound](/panel/outbounds) each customer connection leaves through. It is done in the kernel, by address: the panel turns everything here — domains, countries, customers — into addresses and marks, so routing costs nothing per connection and needs no proxy in the way.

The page has four tabs: **Basic Routing**, **Routing Rules**, **Balancers** and **Route Tester**.

**Saving.** Basic Routing is stored and applied when you press **Save** at the top; the button is enabled once something has changed. Rules and balancers are stored when their own form is saved, and applied within seconds. No restart is needed.

When routing cannot be applied on this server — no nftables, say — a notice at the top says *Routing is stored but not applied*, and why.

## Basic Routing

| Setting | What it does |
|---------|--------------|
| **Default Outbound** | Where traffic no rule matches goes. Choosing one here moves it to the top of the outbound list. Balancers cannot be the default: point a rule at one instead. |
| **Block traffic while the default outbound is down** | On by default. While the default is a hop that is down, unmatched traffic is dropped rather than sent from this server's own address. Off: it falls back to **direct**. |
| **Block BitTorrent Protocol** | Blocks the ports BitTorrent clients use by default (6881–6889, 6969, 51413, 1337). A client moved off them is not caught — the panel does not inspect what is inside the traffic. |
| **Block IPs** | Addresses, ranges, countries (`geoip:ir`) or named groups that customers cannot reach. |
| **Block Domains** | Domains customers cannot reach. |
| **Block Ports** | Ports or ranges — `25`, `6881-6889` — customers cannot reach. Port 25 is a common choice: it stops customers' mail spam from being reported against your address. |
| **Direct IPs** | Addresses, ranges, countries or named groups always sent from this server's own address, whatever the default. |
| **Direct Domains** | Domains always sent direct. |
| **IPv4 Routing** | Domains resolved over IPv4 only and sent direct — for services that misbehave over IPv6. |

**What a list entry can be:**

- an address (`203.0.113.7`) or a range (`10.0.0.0/8`, `fc00::/7`);
- a country, `geoip:` and its two-letter code (`geoip:ir`, `geoip:cn`). The country's ranges are fetched from the regional registries' published lists, kept on disk and refreshed weekly;
- a named group: `private`, `loopback`, `link-local`, `cgnat`, `multicast`, `bogon`.

**Domains** are looked up by the panel and matched by the addresses they resolve to, refreshed every 15 minutes; under the lists the panel says how many names resolved to how many addresses. A subdomain is matched by its own addresses, not by its name: block `example.com` and `www.example.com` is caught only if it resolves to the same addresses. Write each name you mean.

## Routing Rules

Rules are evaluated top to bottom; **the first match decides**. Traffic that matches no rule goes to the default outbound.

| Column | What it shows |
|--------|---------------|
| **#** | The position; drag the ☰ handle to reorder. |
| **Actions** | ✎ edits; ⋮ opens the menu: edit, move up, move down, delete. |
| **Enabled** | A switch; a switched-off rule is kept but skipped. |
| **Source** | The customers, groups, source addresses and ports it matches. |
| **Comment** | The rule's note. |
| **Network** | TCP, UDP, ICMP, or any. |
| **Destination** | The addresses, countries, domains and ports it matches. |
| **Interfaces** | The interfaces it applies to. |
| **Outbounds** / **Balancers** | Where matching traffic goes. |

**more** has **Import Rules** and **Export Rules** (JSON), for moving rules between panels. On a phone each rule is a card: interfaces → outbound, with its criteria as chips.

Removing a rule sends what it matched to the rules below it.

### The rule form

Every field narrows the rule; a field left empty matches anything. Lists are comma-separated.

| Field | Matches |
|-------|---------|
| **Source IPs** | The customer's address inside the tunnel, ranges or named groups. |
| **Source Port** | `53,443,1000-2000`. |
| **Network** | TCP, UDP, ICMP, or any. |
| **IP** | Destination addresses, ranges, `geoip:` countries, named groups — `0.0.0.0/8, fc00::/7, geoip:ir`. |
| **Domain** | Destination domains, matched by the addresses they resolve to. |
| **User** | These customers, wherever they are going. |
| **Group** | Everyone in these groups. |
| **Port** | Destination ports and ranges. |
| **Interfaces** | Traffic arriving on these interfaces. |
| **Outbound tag** / **Balancer** | Where matching traffic goes: one outbound, or a balancer. A switched-off outbound or balancer cannot be chosen. |
| **Note** | What the rule is for, for whoever reads it later. |
| **Enabled** | Off keeps the rule and skips it. |

A rule pointing at an outbound or balancer that is switched off or gone is skipped — its traffic goes on to the next rule — rather than dropped.

## Balancers

A balancer is one tag over several outbounds; point a rule at it to spread traffic over them.

| Column / field | What it is |
|----------------|------------|
| **Tag** | The name rules point at. |
| **Strategy** | **random**: each connection goes to one member, chosen by a hash of the connection, so the load spreads evenly and a connection stays on the exit it started on; members that are down are left out. **leastPing**: every connection goes to the member that answered the last check fastest; while a leastPing balancer exists, its members are checked once a minute. |
| **Outbounds** | The members — at least one. |
| **Note**, **Enabled** | As for rules. |

From the API a balancer can also have a **fallback** outbound, used when every member is down, and can be pinned to one member with `POST /api/balancers/{id}/override`. A balancer that rules point at cannot be removed until they are changed.

## Route Tester

Asks the routing which outbound would carry a connection, without sending anything. Give a **Domain or IP**, and optionally a **Port**, a **Network** and the **Interface** it arrives on, then **Test Route**. The answer names the outbound — and the balancer, when it went through one — and **Show me that rule** jumps to the rule that decided. When no rule matched, it says so and names the default outbound.
