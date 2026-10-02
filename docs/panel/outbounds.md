---
description: "Where customers' traffic leaves the server: this server's own address, the bin, or a hop you add — a WireGuard or OpenVPN server, a VLESS, VMess, Trojan, Shadowsocks, Hysteria, SOCKS or HTTP proxy, WARP, NordVPN or PIA."
---

# Outbounds

An outbound is a way out of the server. Every packet a customer sends leaves through exactly one: this server's own address, the bin, or a **hop** — another server the traffic is sent on to, so the customer appears from there.

Two outbounds are built in and cannot be deleted or switched off:

| Tag | What it is |
|-----|------------|
| **direct** | This server's own address. Where traffic goes when nothing else is set. |
| **blocked** | The bin: traffic sent here is discarded. For rules that block something. |

## The default outbound

**The outbound at the top of the list is the default**: every customer's traffic that no [routing rule](/panel/routing) sends elsewhere leaves through it. Put a working hop at the top and every customer appears from that hop's address — what an operator in a filtered country does to move traffic through a clean server. Use **Move to top** in the row menu, or choose the default on *Routing → Basic*.

### When the default is down

If the default is a hop and that hop is down, customers' unmatched traffic is **dropped** rather than quietly sent from this server's own address — the address the hop is there to hide. Their connections fail until the hop is back. *Routing → Basic → Block traffic while the default outbound is down* is on by default; turn it off and traffic falls back to **direct** instead.

## The Save bar

Changes on this page are stored the moment you make them and applied on the panel's next pass, within seconds. **Save** applies them now; it is only enabled once something has changed. No restart is needed.

## The toolbar

| Control | What it does |
|---------|--------------|
| **Outbounds** (+) | Opens the [outbound form](#the-outbound-form). |
| **Subscriptions** | The [outbound subscriptions](#outbound-subscriptions) window. |
| **more** | **WARP**, **NordVPN**, **PIA** (one-click hops, below), **Import Outbounds** and **Export Outbounds**. |
| **TCP / HTTP / Real delay** | How **Check** and **Test all** measure: **TCP** only connects to the hop (fast); **HTTP** makes a whole request through it and also finds the address the world sees and its country; **Real delay** times the whole request including connection setup. |
| **Test all** | Checks every outbound with the chosen measure. |
| ↺ | Resets every outbound's traffic counter, after a confirmation. |

**Import Outbounds** takes JSON — a list, or an object holding one — as **Export Outbounds** writes it, for moving hops between panels.

## The table

| Column | What it shows |
|--------|---------------|
| **#** | The position — #1 is the default — with ✎ to edit and ⋮ for the row menu. |
| **Tag** | The outbound's name, which [routing rules](/panel/routing) point at, and its kind. |
| **Address** | The server a hop connects to. |
| **Egress** | The address the world sees for traffic through it, once an **HTTP** check has run. Hidden behind the 👁 so a screenshot does not give it away. |
| **Country** | The egress address's country, from the same check. |
| **Traffic** | What it has carried up ↑ and down ↓, while *Engine → per-outbound counters* is on. |
| **Latency** | The last check's time; **no answer** in red when it failed (hover for why). |
| **Check** | Checks this one outbound with the chosen measure. |

On a phone the table becomes cards with the same information.

### The row menu

| Item | What it does |
|------|--------------|
| **Move to top** | Makes it the default. |
| **Move up** / **Move down** | Changes its place. |
| **Reset Traffic** | Sets its traffic counter back to zero. |
| **Disable** / **Enable** | A disabled hop is not used: a rule pointing at it is skipped, so that traffic goes on to the next rule and, failing that, the default. Nothing is dropped because a hop was switched off. Not for the built-ins. |
| **Delete** | Removes the hop, after a confirmation. Traffic that used it falls back to the default; a routing rule that points at it must be changed first. Not for the built-ins. |

## The outbound form

**Tag** is the name rules point at: unique, and refused when another outbound already has it. **Protocol** decides the rest.

| Protocol | What it needs |
|----------|---------------|
| **wireguard** | Our private key (↻ makes a new one; its public key is shown), the address the upstream gave us, MTU, and one peer: its public key, pre-shared key, endpoint, allowed IPs and keep-alive. **Upload .conf** fills everything from a wg-quick file. The MTU is lower than a normal tunnel's, because this one runs inside another. |
| **openvpn** | The client profile (**Upload file** for an `.ovpn`), and a username and password if the server asks for them. |
| **vless**, **vmess**, **trojan**, **shadowsocks**, **hysteria** | Address and port, the identity (UUID, password or method, encryption, flow), the transmission (RAW, mKCP, WebSocket, gRPC, HTTPUpgrade, XHTTP) with its path, host or service name, and the security (none, TLS, or — for VLESS over RAW, gRPC or XHTTP — Reality) with SNI and fingerprint. **Import** fills everything from a share link (`vless://…`, `vmess://…`, `trojan://…`, `ss://…`); a link the panel cannot read says *Wrong Link!*. |
| **socks**, **http** | Address and port, and a username and password if the proxy asks. |

A stored secret is shown as *(unchanged)*: leave it to keep it.

**What carries each kind.** A WireGuard hop is a network device the kernel runs. An OpenVPN hop needs `openvpn` on the server (the installer adds it with OpenVPN). The VLESS, VMess, Trojan, Shadowsocks, Hysteria, SOCKS and HTTP kinds are run by an **`xray`** process, joined to the routing by **`sing-box`**; both must be installed on the server, which the installer does not do. Without them the hop stays down, and its check says *xray is not installed on this server*.

## WARP, NordVPN and PIA

| Item | What it does |
|------|--------------|
| **WARP** | **Create WARP account** registers a free Cloudflare WARP device and shows its access token, device ID, license key and private key. **WARP / WARP+ license key** applies a 26-character WARP+ key. **Account info** shows the device, account type and WARP+ data. **Change IP** asks Cloudflare for a new address. **Add outbound** turns it into a WireGuard hop. **Delete account** forgets the device here; a hop made from it keeps working until Cloudflare drops the key. |
| **NordVPN** | Takes your NordVPN access token, lists servers by country and city with their load, and adds the one you pick as a WireGuard (NordLynx) hop. A server already in the list is refreshed rather than added twice. |
| **PIA** | Takes your PIA username and password, lists servers by region, and adds the one you pick as a WireGuard hop. A server already in the list has its key renewed. |

## Outbound subscriptions

A subscription is a URL that gives a list of share links (as a base64 list). The panel fetches it on a schedule and keeps the outbounds it produces in step.

| Field | What it is |
|-------|------------|
| **Remark** | Optional name, such as *HK nodes*. |
| **Subscription URL** * | Where the list is fetched from. |
| **Tag prefix** | Put before every tag it produces, such as `hk-`. |
| **Update interval** | How often to fetch it again; 10 minutes by default. |
| **Enabled** | Off stops fetching; its outbounds stay as they are. |
| **Allow private address** | Lets the URL point at localhost or a private network. Off by default, for safety; turn it on only for a source you trust. |
| **Before manual outbounds** | Places its outbounds above yours, so one of them can become the default. |

**Preview** shows what the URL gives before you add it. The list of active subscriptions shows each one's last fetch and state, with **Refresh now** for one and **Refresh all**. Outbounds from a subscription are applied on the panel's next pass; **Save** applies them now.

## Balancers

To spread customers over several hops, or fail over from one to another, group them in a balancer on *Routing → Balancers* and point a rule at it. See [Routing](/panel/routing#balancers).
