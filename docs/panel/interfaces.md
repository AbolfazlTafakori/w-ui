---
description: "The tunnels this server offers. One interface is one protocol on one port with one address range; every customer reaches the server through one or more of them."
---

# Interfaces

An interface is a tunnel the server listens on: one protocol (WireGuard, AmneziaWG or OpenVPN), on one port, handing out addresses from one range. Other panels call this an *inbound*. A customer can be on several interfaces at once and has one allowance across all of them.

Nothing can be sold until at least one interface exists: a customer always lives on an interface.

## The summary card

At the top of the page:

| Figure | What it is |
|--------|------------|
| **Total Sent/Received** | Everything every interface has carried, up ↑ and down ↓. |
| **Total Usage** | The two added together. |
| **Total Interfaces** | How many interfaces exist, switched on or off. |

The figures, the table and the speeds refresh by themselves every 5 seconds. The refresh pauses while a form, a dialog or a confirmation is open, so nothing moves under your cursor.

A yellow banner above the card says when quota enforcement or another part of the server's engine is not running on this machine (for example *"Quota enforcement is not active. Limits are recorded but not applied."*). Limits are then stored but not applied until the cause is fixed. See [Troubleshooting](/help/troubleshooting).

## The toolbar

| Control | What it does |
|---------|--------------|
| **Add Interface** | Opens the form below to create a new interface. |
| **General Actions** | A menu of actions on every interface at once (see the next table). |
| **Search** | Filters the table as you type, on the name, the port or the protocol. |
| *n* selected + **Delete** | Shown once rows are ticked. The × clears the selection; **Delete** deletes every selected interface (see [Deleting an interface](#deleting-an-interface)). |

**General Actions:**

| Item | What it does |
|------|--------------|
| **Import an Interface** | Paste the JSON that **Export Interface** or **Export All Interfaces** produced, and the interface (or each one in a list) is created again: the same name, protocol, port, range, endpoint, MTU, DNS, egress interface and mode. New keys are made, so configurations from the old one do not carry over. Import stops at the first one the panel refuses, and says why; the ones before it stay created. |
| **Export All URLs** | The subscription link of every customer on every interface, one per line, in a window you can copy from. A customer on two interfaces is listed once. |
| **Export All Interfaces** | Every interface's settings as JSON, for **Import an Interface** on this panel or another. Keys and customers are not included. |
| **Reset Traffic for All Interfaces** | Sets the used traffic of every customer on every interface back to zero, after a confirmation. Allowances, end dates and states are not touched. A customer who was stopped for running out of traffic is connected again at once. |

## The table

| Column | What it shows |
|--------|---------------|
| ☐ | Tick to select. The box in the header selects every row. |
| **ID** | The interface's number. Sortable. |
| **Menu** | ✎ opens the edit form; ⋮ opens the row menu (below). |
| **Enabled** | A switch. Off takes every customer of this interface out of the kernel without deleting anything: their devices stop connecting through it until it is switched on again. The switch shows a spinner until the server confirms; if the server refuses, it moves back and says why. |
| **Remark** | The interface's name. Sortable. |
| **Node** | Only when [nodes](/panel/nodes) exist. *Local panel* for this server, otherwise the node's name: blue while the node answers, red while it does not. Sortable. |
| **Port** | The port it listens on. Sortable. |
| **Protocol** | `wireguard` or `openvpn`; the transport (`UDP`, or for OpenVPN `UDP`/`TCP`); and **AmneziaWG** when that mode is on. Sortable. |
| **Clients** | Badges: 👥 every customer on it · green: active · grey: switched off · red: out of traffic or time · blue: online now. A customer counts as online when one of their devices has spoken to the server within the online window (*Engine → Online Window*, 3 minutes unless changed). Sortable by the total. |
| **Traffic** | What crossed *this* interface, from its own counters; hover for up ↑ and down ↓. A customer on two interfaces spends one allowance, but each interface is charged only with its own bytes, on this server and on nodes alike. Interfaces have no traffic limit of their own, so the limit always reads ∞. Sortable. |
| **Speed** | What the customers on this interface are moving right now: ↑ up / ↓ down, per second, averaged over the last few collections. "—" while nothing moves. For an interface on a node it comes from that node's reports, about every 20 seconds. Sortable. |
| **Duration** | Always ∞: interfaces do not expire. Customers do. |

The table is paged by *Settings → Pagination Size* (0 shows everything on one page).

### On a phone

The table becomes a list of cards. Each card shows the ID, the name, the switch and ⋮. Tap the card (or ⓘ) to unfold the protocol, port, customer badges, traffic and, while something moves, the speed. **Select all** at the top selects every card.

## The row menu (⋮)

| Item | When | What it does |
|------|------|--------------|
| **Export All URLs** | always | The subscription link of every customer on this interface, one per line. |
| **Export Interface** | always | This interface's settings as JSON, for **Import an Interface**. |
| **Download the .ovpn for this tunnel** | OpenVPN only | The one `.ovpn` profile every customer on this tunnel uses. It holds no personal key: each customer signs in with their own username and password, so the same file can be given to everyone, and removing a customer is enough to lock them out. |
| **Reset Traffic** | always | Sets the used traffic of every customer with a device on this interface back to zero. A customer also on other interfaces has one total, so this clears all of it for them. Anyone cut off for running out of traffic is connected again at once. Allowances and dates are unchanged. |
| **Clone** | always | A copy with its own name, port, address range and keys, and no customers. The protocol, MTU, DNS and mode are copied, and an AmneziaWG copy gets a fresh obfuscation profile, so one blocking rule cannot take both down. The form suggests the next free name (`wg0` → `wg1`), the next port and the next range (`10.66.0.0/24` → `10.67.0.0/24`); change them freely. |
| **Restart** | always | Takes the tunnel down and brings it up again. For a tunnel that would not come up when the panel started, for example because its port was taken or a tool was missing. If it still will not come up, the error says why. Customers on it are disconnected for a moment and reconnect by themselves. |
| **Attach Existing Clients…** | always | Pick customers who are not on this interface yet (search by name) and add it to them. Each gets a new device configuration for it; their subscription link picks it up by itself. |
| **Attach Clients To…** | the interface has customers | Adds another interface, chosen in the dialog, to every customer on this one. |
| **Detach Clients From…** | the interface has customers | Removes another interface, chosen in the dialog, from every customer on this one. A customer who would be left with no interface at all is refused and named. |
| **Add Clients To Group…** | the interface has customers | Puts every customer on this interface into a [group](/panel/groups); type a new name or pick an existing one. |
| **Take All Clients Off** | the interface has customers | Removes this interface from every customer on it. They keep their other interfaces. A customer who would be left with none is refused and named. This is what has to happen before the interface can be deleted. |
| **Delete** | always | See below. |

## Deleting an interface

Deleting destroys the interface's keys, and there is no undo: an interface made again with the same settings has new keys, and no configuration from the old one connects.

An interface that still has customers cannot be deleted. The panel says how many are still on it; take them off first with **Take All Clients Off**, which keeps each of them on their other interfaces, then delete. The same holds for **Delete** on several selected interfaces: if any of them still has customers, nothing is deleted and each one with customers is named.

## The form

**Add Interface** and ✎ open the same form. A field marked * is required. A value the server refuses is shown in red under its own field.

| Field | Default | What it is | Limits |
|-------|---------|------------|--------|
| **Enabled** | on | Off creates it (or keeps it) without serving anyone. | — |
| **Protocol** * | WireGuard | WireGuard or OpenVPN; only the protocols this server has installed are offered. Changing it resets the name, port, range and MTU to that protocol's defaults. | Cannot change after creation. |
| **Server** | this server | Shown only when [nodes](/panel/nodes) exist: which server runs it. | A switched-off node is refused. Cannot change after creation: every customer on it would be stranded. |
| **Interface** (name) * | `wg0` / `ovpn0` | Its name, which is also the network device's name on the server. | Up to 15 characters, no spaces or slashes; unique on its server. |
| **Endpoint** * | — | The hostname or IP customers connect to, written into every configuration. | A hostname or an IP; an IPv6 address goes in brackets, `[2001:db8::1]`. |
| **Port** * | 51820 / 1194 | The port it listens on. | 1–65535. Refused when something on this server already listens on it (checked for the right transport, UDP or TCP). Not checked for a tunnel on a node. |
| **Transport** | UDP | OpenVPN only. **UDP** is faster; **TCP** gets through networks that block UDP, and on port 443 looks like an ordinary HTTPS connection. | WireGuard is always UDP. |
| **Subnet** * | `10.66.0.0/16` / `10.8.0.0/16` | The range customer addresses come from. A /16 holds about 65,000 devices; freed addresses are reused. | IPv4, from /10 (about 4 million) to /30. It cannot overlap another interface on the same server, or a network another program uses there (Docker, another VPN, the server's own). |
| **MTU** | 1420 / 1500 | The largest packet. Lower it (to 1280, say) when some sites hang for some customers. | 576–9000. |
| **DNS** | `1.1.1.1` | The DNS servers handed to customers. | One or more IP addresses, separated by commas. |
| **Egress interface** | `eth0` | The server's network card customer traffic leaves through, to the internet. | A device name, up to 15 characters. |
| **Mode** | Standard | WireGuard only. **AmneziaWG** pads and disguises the handshake so it does not look like WireGuard to a filter; its obfuscation parameters are generated for you. Customers need an AmneziaWG-capable app (see [Client apps](/guide/client-apps)). | Needs AmneziaWG installed on the server. |

### Changing an interface later

Everything except the protocol and the server can be changed after creation.

- **Endpoint, DNS, MTU and egress interface** apply in place and disconnect nobody. Customers' subscription links carry the new values; a configuration already imported into an app keeps the old ones until it is fetched again.
- **Name, port, subnet, mode and transport** take the tunnel down and bring it up again as new. Every customer on it needs their configuration again; the subscription link already carries the new one, so a customer who uses the link only has to refresh it.
- **A new subnet** also gives every device a new address, and is refused when it is too small for the devices already on it.

## Good to know

- **Several interfaces, one customer.** A customer on WireGuard and OpenVPN gets a configuration for each, and spends one allowance and one device limit across both.
- **A second interface as a fallback.** An AmneziaWG clone, or OpenVPN over TCP 443, keeps customers connected where plain WireGuard is filtered.
- **Interfaces on nodes** are run by the node's own panel; this panel sends it the interface and the customers. See [Nodes](/panel/nodes).
