---
description: "Send every customer's traffic out through another server, so their egress address is that server's : and drop it rather than leak it when the hop is down."
---

# All traffic through a clean server

The situation: this server is where customers can reach; another server is where the internet is reachable from. Customers connect here, and their traffic should leave *there*.

**1. On the far server**, make a WireGuard peer for this one — any WireGuard server will do, including another W-UI: create an interface there and a customer with one user, and download their `.conf`.

**2. Outbounds → + (Outbounds)**, protocol *wireguard*, **Upload .conf** with that file (or fill in the private key, the address, the peer's public key and endpoint by hand). Save.

**3. Check** the row with **HTTP** chosen: the egress address should now be the far server's, and the country its country.

**4. Move it to the top** (row menu → **Move to top**) and **Save**. The outbound at the top is the default: every customer's traffic now leaves through it. From a customer's device, open a "what is my IP" page — it shows the far server.

**5. Block traffic while the default outbound is down** is on by default (Routing → Basic Routing): if the hop goes down, customers' traffic is dropped rather than sent from this server's own address. Turn it off only if you would rather they fall back to this server.

::: tip
Keep `direct` for the traffic that should not take the detour — your own country's addresses, say — with a rule: **Routing → Routing Rules → Add rule**, **IP** `geoip:xx`, **Outbound tag** `direct`. See [Split routing](/examples/split-routing).
:::
