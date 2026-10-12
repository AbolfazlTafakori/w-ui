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
| **Import an Interface** | Paste the JSON that **Export Interface** or **Export All Interfaces** produced, or **Choose a file** it was saved to, and the interface (or each one in a list) is created again: the same name, protocol, port, range, endpoint, MTU, DNS, egress interface, mode and, for OpenVPN, transport. An export from 2.6.6 or earlier imports too. New keys are made, so configurations from the old one do not carry over. One the panel refuses -- a name or port already taken here -- is named with the reason, and the rest are still created. |
| **Export All URLs** | The subscription link of every customer on every interface, one per line, in a window you can copy from or **Download** as a file. A customer on two interfaces is listed once. Every customer is included, however many there are. |
| **Export All Interfaces** | Every interface's settings as JSON, to copy or **Download**, for **Import an Interface** on this panel or another. Keys and customers are not included. |
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
| **Export All URLs** | always | The subscription link of every customer on this interface, one per line, to copy or download. |
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

### OpenConnect (Cisco AnyConnect)

An OpenConnect tunnel is what Cisco Secure Client (formerly AnyConnect) dials, and the openconnect client on Linux and OpenWrt. It is a VPN inside TLS on TCP, and once a device is in, DTLS on UDP on the same port, which is faster. 443 is the default, where it looks like any website; any free port works. One per server, on this server for now (not on a node).

- **Sign-in:** a username and password per device, as on OpenVPN and L2TP. The server checks each sign-in with the panel's agent. The agent knows each device's password and address, and it ends a device's other session before it lets the new one in, so a second device on one sign-in takes the connection over. A device that was cut off cannot come back on its old session either.
- **DTLS:** on by default. Off sends everything over TLS on TCP.
- **Camouflage:** on by default. The server answers like an ordinary website unless the address carries its secret, so a scanner or a filter that probes it sees nothing to block. Customers dial the address with `/?secret` on it, and their page gives it whole. It needs ocserv 1.2 or later (Ubuntu 24.04, Debian 13). On an older one the tunnel runs without camouflage, and the panel says so in its log.
- **Certificate:** when the panel has a certificate for the tunnel's address, the tunnel uses it, and devices trust it as they are. Otherwise the tunnel makes one of its own and renews it. In that case Cisco Secure Client has to be told to accept it (Block Untrusted Servers off), and the customer's page says how.

ocserv is installed the first time a tunnel is made, run by the panel's agent from its own files, and left running across the panel's updates.

### AmneziaWG obfuscation

Choosing AmneziaWG opens the obfuscation values in the form, already filled in. The panel draws them for the server the tunnel is on: valid as AmneziaWG checks them, fitted to the tunnel's MTU, and unlike every other AmneziaWG tunnel on that server, with no shared identifier, no shared handshake padding and, while there are kinds left, a different packet before the handshake. They are kept small enough to cost a busy tunnel little: a few junk packets of at most 250 bytes, data padding of at most 20. Change any of them before creating, or later with **Edit**.

There are two kinds of profile. **Modern (AmneziaWG 2)** has identifier ranges and a disguised packet before each handshake; the current AmneziaWG and AmneziaVPN apps read it. **Compatible** is what every AmneziaWG app has ever read, for devices that cannot be updated. A node running W-UI before 3.0 always gets a compatible one.

The values:

- **Jc, Jmin, Jmax**: the junk packets sent before each handshake.
- **S1 to S4**: the padding on each message type. S2 must not be S1 + 56, and S4 has to fit the link: at most 1440 minus the MTU.
- **H1 to H4**: the value each message type is marked with, above 4. Give a second value to make it a range (AmneziaWG 2.0): each packet then carries a value of its own from it. The four must not overlap.
- **HeaderProtectionKey**: AmneziaWG 3 header protection, a key in base64. Needs S1 to S4 of at least 12.
- **I1 to I5**: packets sent before each handshake to look like another protocol. Pick a ready one (QUIC, a DNS query, STUN) or write your own from the tags `<b 0x…>`, `<r N>`, `<rc N>`, `<rd N>` and `<t>`, the ones both the kernel module and the phone apps read.

Every value is checked as AmneziaWG checks it, and a refusal names the field. **Draw new values** replaces the numbers and keeps I1 to I5. After any change every customer on the tunnel needs their file again. Ranges and I1 to I5 need AmneziaWG 1.5 or later on the device, and W-UI 3.0 or later on a node.

A customer's page offers each AmneziaWG file a second way as well: the **AmneziaVPN** key, to copy or scan into the AmneziaVPN app. It is the same file.

The installer keeps the AmneziaWG kernel module working across kernel upgrades. Each new kernel gets its headers, so DKMS rebuilds the module as the kernel installs. `wui-amnezia-kmod` builds it at boot if that did not happen. `amneziawg-go` is installed besides, so a kernel the module cannot be built for still carries the tunnels.

### IKEv2

An IKEv2 tunnel is the VPN built into Android 12 and newer, iPhone, Mac and
Windows. It is always on UDP 500 and 4500, one per server, and shares
strongSwan with an L2TP tunnel on the same server. Choose the ways a device
may sign in; all four are on by default:

| Way | Devices | What the device needs |
|---|---|---|
| Username and password (MSCHAPv2) | Android, iPhone, Mac, Windows | its sign-in, and the server's certificate installed once |
| Key (PSK) | Android, iPhone, Mac | its username as the identity, its password as the key |
| Certificate (RSA) | Android, iPhone, Mac | its own `.p12` file |
| Certificate in EAP (EAP-TLS) | Android | its own `.p12` file |

The panel makes the tunnel's certificate authority and the server's
certificate, issues each device its own, and renews them. The customer's
page offers the server's certificate and the device's `.p12`, which opens
only with the device's password. IKEv2 runs on this server only for now,
not on a node.

### Changing an interface later

Everything except the protocol and the server can be changed after creation.

- **Endpoint, DNS, MTU and egress interface** apply in place and disconnect nobody. Customers' subscription links carry the new values; a configuration already imported into an app keeps the old ones until it is fetched again.
- **Name, port, subnet, mode and transport** take the tunnel down and bring it up again as new. Every customer on it needs their configuration again; the subscription link already carries the new one, so a customer who uses the link only has to refresh it.
- **A new subnet** also gives every device a new address, and is refused when it is too small for the devices already on it.

## Good to know

- **Several interfaces, one customer.** A customer on WireGuard and OpenVPN gets a configuration for each, and spends one allowance and one number of users across both.
- **A second interface as a fallback.** An AmneziaWG clone, or OpenVPN over TCP 443, keeps customers connected where plain WireGuard is filtered.
- **Interfaces on nodes** are run by the node's own panel; this panel sends it the interface and the customers. See [Nodes](/panel/nodes).
