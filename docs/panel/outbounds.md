# Outbounds

Where customers' traffic leaves. Two built-ins, **direct** (this server's own address) and **blocked** (discarded), and any number of hops you add — a WireGuard peer, another server, a SOCKS or HTTP proxy.

## The rule that matters

**The outbound at the top of the list is the default.** Put a working hop there and all customer traffic leaves through it: every customer's egress address becomes that hop's address, which is what an operator in a filtered country does to move traffic through a clean server. Reorder with the row menu (move to top, up, down).

## Fail-closed

When the default outbound is down, customers' traffic is **dropped** rather than quietly sent from this server's own address — the address the outbound exists to hide. Routing → Basic → "Block traffic while the default outbound is down" turns this off.

## The table

`#`, tag, address, egress (the address the world sees, hidden behind the eye), country, traffic, latency, and **Check** — a probe over TCP, HTTP or a real delay through the hop. **Test all** probes every one; the mode selector applies to both.

## Subscriptions, WARP, NordVPN, PIA

An outbound subscription is a URL that yields outbounds; it is fetched on a schedule and the rows it produces are kept in step. **WARP** builds a Cloudflare WARP hop with one click; **NordVPN** and **PIA** take an account and pick a server.

## Balancers

Routing → Balancers groups outbounds and picks among them: round-robin, least load, least ping, random, or by observatory results, with a fallback for when they all fail and a manual override for when you want one in particular.
