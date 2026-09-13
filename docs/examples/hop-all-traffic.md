---
description: "Send every customer's traffic out through another server, so their egress address is that server's : and drop it rather than leak it when the hop is down."
---

# All traffic through a clean server

The situation: this server is where customers can reach, another server is where the internet is reachable from. Customers connect here; their traffic should leave *there*.

**1. On the far server**, make a WireGuard peer for this one — any WireGuard server will do, including another W-UI: create an interface there and a client with one device; download its config.

**2. Outbounds → + Outbounds**, type *WireGuard*, paste the config's private key, address, the peer's public key and endpoint. Save.

**3. Check** the row: the egress address should now be the far server's, and the country its country.

**4. Move it to the top** (row menu → *Move to top*) and **Save**. The outbound at the top is the default: every customer's traffic now leaves through it. Check on the Overview that tunnels are up, then from a customer's device open a "what is my IP" page — it shows the far server.

**5. Fail-closed** is on by default (Routing → Basic): if the hop goes down, customers' traffic is dropped rather than sent from this server's own address. Turn it off only if you would rather they fall back to this server.

::: tip
Keep `direct` for the traffic that should not take the detour — your own country's addresses, say — with a rule in Routing → Rules: `geoip:xx` → `direct`. [Split routing](/examples/split-routing).
:::
