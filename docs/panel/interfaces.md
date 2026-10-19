---
description: "The tunnels this server offers. One interface is one protocol on one port with one address range; every customer reaches the server through one or more of them."
---

# Interfaces

An interface is a tunnel the server listens on: one protocol on one port, handing out addresses from one range. The protocols are WireGuard (plain or AmneziaWG), OpenVPN, OpenConnect, IKEv2, L2TP/IPsec and PPTP. Other panels call this an *inbound*. A customer can be on several interfaces at once and has one allowance across all of them.

Nothing can be sold until at least one interface exists, because a customer always lives on an interface.

## The summary card

At the top of the page:

| Figure | What it is |
|--------|------------|
| **Total Sent/Received** | Everything every interface has carried, up ↑ and down ↓. |
| **Total Usage** | The two added together. |
| **Total Interfaces** | How many interfaces exist, switched on or off. |

The figures, the table and the speeds refresh by themselves every 5 seconds. The refresh pauses while a form, a dialog or a confirmation is open, so nothing moves under your cursor.

A yellow banner above the card says when quota enforcement or another part of the server's engine is not running on this machine, for example *"Quota enforcement is not active. Limits are recorded but not applied."*. Limits are then stored but not applied until the cause is fixed. See [Troubleshooting](/help/troubleshooting).

## The toolbar

| Control | What it does |
|---------|--------------|
| **Add Interface** | Opens the [form](#the-form) to create a new interface. |
| **General Actions** | A menu of actions on every interface at once; see the next table. |
| **Search** | Filters the table as you type, on the name, the port or the protocol. |
| *n* selected + **Delete** | Shown once rows are ticked. The × clears the selection, and **Delete** deletes every selected interface (see [Deleting an interface](#deleting-an-interface)). |

**General Actions:**

| Item | What it does |
|------|--------------|
| **Import an Interface** | Paste the JSON that **Export Interface** or **Export All Interfaces** produced, or use **Choose a file** for the file it was saved to, and the interface, or each one in a list, is created again: the same name, protocol, port, range, endpoint, MTU, DNS, egress interface, mode and, for OpenVPN, transport. An export from 2.6.6 or earlier imports too. New keys are made, so configurations from the old interface do not carry over. One the panel refuses, because a name or port is already taken here for example, is named with the reason, and the rest are still created. |
| **Export All URLs** | The subscription link of every customer on every interface, one per line, in a window you can copy from or **Download** as a file. A customer on two interfaces is listed once. Every customer is included, however many there are. |
| **Export All Interfaces** | Every interface's settings as JSON, to copy or **Download**, for **Import an Interface** on this panel or another. Keys and customers are not included. |
| **Reset Traffic for All Interfaces** | Sets the used traffic of every customer on every interface back to zero, after a confirmation. Allowances, end dates and states are not touched. A customer who was stopped for running out of traffic is connected again at once. |

## The table

| Column | What it shows |
|--------|---------------|
| ☐ | Tick to select. The box in the header selects every row. |
| **ID** | The interface's number. Sortable. |
| **Menu** | ✎ opens the edit form, and ⋮ opens the [row menu](#the-row-menu). |
| **Enabled** | A switch. Off takes every customer of this interface out of the kernel without deleting anything: their devices stop connecting through it until it is switched on again. The switch shows a spinner until the server confirms; if the server refuses, it moves back and says why. |
| **Remark** | The interface's name. Sortable. |
| **Node** | Shown only when [nodes](/panel/nodes) exist. *Local panel* for this server, otherwise the node's name, blue while the node answers and red while it does not. Sortable. |
| **Port** | The port it listens on. Sortable. |
| **Protocol** | The protocol, its transport (`UDP`, `TCP`, or both for OpenConnect), and **AmneziaWG** when that mode is on. An inbound on a 3x-ui or Pasarguard node shows its own kind, such as `vless` with its network and security. Sortable. |
| **Clients** | Badges: 👥 every customer on it, green active, grey switched off, red out of traffic or time, blue online now. A customer counts as online when one of their devices has spoken to the server within the online window (*Engine → Online Window*, 3 minutes unless changed). Sortable by the total. |
| **Traffic** | What crossed *this* interface, from its own counters; hover for up ↑ and down ↓. A customer on two interfaces spends one allowance, but each interface is charged only with its own bytes, on this server and on nodes alike. Interfaces have no traffic limit of their own, so the limit always reads ∞. Sortable. |
| **Speed** | What the customers on this interface are moving right now, ↑ up / ↓ down per second, averaged over the last few collections. "—" while nothing moves. For an interface on a node it comes from that node's reports, about every 20 seconds. Sortable. |
| **Duration** | Always ∞: interfaces do not expire; customers do. |

The table is paged by *Settings → Pagination Size*; 0 shows everything on one page.

### On a phone

The table becomes a list of cards. Each card shows the ID, the name, the switch and ⋮. Tap the card, or ⓘ, to unfold the protocol, port, customer badges, traffic and, while something moves, the speed. **Select all** at the top selects every card.

## The row menu

| Item | When | What it does |
|------|------|--------------|
| **Export All URLs** | Always | The subscription link of every customer on this interface, one per line, to copy or download. |
| **Export Interface** | Always | This interface's settings as JSON, for **Import an Interface**. |
| **Download the .ovpn for this tunnel** | OpenVPN only | The one `.ovpn` profile every customer on this tunnel uses. It holds no personal key: each customer signs in with their own username and password, so the same file can be given to everyone, and removing a customer is enough to lock them out. |
| **Reset Traffic** | Always | Sets the used traffic of every customer with a device on this interface back to zero. A customer also on other interfaces has one total, so this clears all of it for them. Anyone cut off for running out of traffic is connected again at once. Allowances and dates are unchanged. |
| **Clone** | Always | A copy with its own name, port, address range and keys, and no customers. The protocol, MTU, DNS and mode are copied, and an AmneziaWG copy gets a fresh obfuscation profile, so one blocking rule cannot take both down. The form suggests the next free name (`wg0` → `wg1`), the next port and the next range (`10.66.0.0/24` → `10.67.0.0/24`); change them freely. |
| **Restart** | Always | Takes the tunnel down and brings it up again. Use it for a tunnel that would not come up when the panel started, because its port was taken or a tool was missing for example. If it still will not come up, the error says why. Customers on it are disconnected for a moment and reconnect by themselves. |
| **Attach Existing Clients…** | Always | Pick customers who are not on this interface yet, searching by name, and add it to them. Each gets a new device configuration for it, and their subscription link picks it up by itself. |
| **Attach Clients To…** | The interface has customers | Adds another interface, chosen in the dialog, to every customer on this one. |
| **Detach Clients From…** | The interface has customers | Removes another interface, chosen in the dialog, from every customer on this one. A customer who would be left with no interface at all is refused and named. |
| **Add Clients To Group…** | The interface has customers | Puts every customer on this interface into a [group](/panel/groups); type a new name or pick an existing one. |
| **Take All Clients Off** | The interface has customers | Removes this interface from every customer on it. They keep their other interfaces. A customer who would be left with none is refused and named. This is what has to happen before the interface can be deleted. |
| **Delete** | Always | See below. |

## Deleting an interface

Deleting destroys the interface's keys, and there is no undo: an interface made again with the same settings has new keys, and no configuration from the old one connects.

An interface that still has customers cannot be deleted. The panel says how many are still on it. Take them off first with **Take All Clients Off**, which keeps each of them on their other interfaces, then delete. The same holds for **Delete** on several selected interfaces: if any of them still has customers, nothing is deleted, and each one with customers is named.

## The form

**Add Interface** and ✎ open the same form. A field marked * is required. A value the server refuses is shown in red under its own field.

| Field | Default | What it is | Limits |
|-------|---------|------------|--------|
| **Enabled** | On | Off creates the interface, or keeps it, without serving anyone. | |
| **Server** | This server | Shown only when [nodes](/panel/nodes) exist: which server runs it. On a 3x-ui or Pasarguard node, the form becomes that panel's own inbound form instead; see [Nodes](/panel/nodes). | A switched-off node is refused. It cannot change after creation, since every customer on it would be stranded. |
| **Protocol** * | WireGuard | WireGuard, OpenVPN, OpenConnect, IKEv2, L2TP/IPsec or PPTP; only the protocols the server has installed are offered. Changing it resets the name, port, range and MTU to that protocol's defaults. | It cannot change after creation. |
| **Interface** (name) * | `wg0` / `ovpn0` | Its name, which is also the network device's name on the server. | Up to 15 characters, no spaces or slashes; unique on its server. |
| **Endpoint** * | | The hostname or IP address customers connect to, written into every configuration. | A hostname or an IP; an IPv6 address goes in brackets, `[2001:db8::1]`. |
| **Port** * | 51820 / 1194 | The port it listens on. | 1 to 65535. Refused when something on this server already listens on it, checked for the right transport. Not checked for a tunnel on a node. |
| **Transport** | UDP | OpenVPN only. **UDP** is faster; **TCP** gets through networks that block UDP, and on port 443 looks like an ordinary HTTPS connection. | WireGuard is always UDP. |
| **Subnet** * | `10.66.0.0/16` / `10.8.0.0/16` | The range customer addresses come from. A /16 holds about 65,000 devices, and freed addresses are reused. | IPv4, from /10 (about 4 million) to /30. It cannot overlap another interface on the same server, or a network another program uses there, such as Docker, another VPN or the server's own. |
| **MTU** | 1420 / 1500 | The largest packet. Lower it, to 1280 for example, when some sites hang for some customers. | 576 to 9000. |
| **DNS** | `1.1.1.1` | The DNS servers handed to customers. | One or more IP addresses, separated by commas. |
| **Egress interface** | `eth0` | The server's network card that customer traffic leaves through to the internet. | A device name, up to 15 characters. |
| **Mode** | Standard | WireGuard only. **AmneziaWG** pads and disguises the handshake so it does not look like WireGuard to a filter; its obfuscation values are generated for you (see [below](#amneziawg-obfuscation)). Customers need an AmneziaWG app (see [Client apps](/guide/client-apps)). | Needs AmneziaWG installed on the server. |
| **Pre-shared key (IPsec)** | Generated | L2TP/IPsec only. Every device types it in beside its own username and password. | Left empty, a strong key is generated. Some routers take at most 32 characters, such as the TP-Link Archer AX23's VPN client. Changing it later means every device needs the new key, and every session on the tunnel ends. |

Each protocol's own settings follow.

### L2TP/IPsec and PPTP

An L2TP/IPsec tunnel is the VPN built into Windows, macOS, iPhone and many routers. Devices sign in with their own username and password, plus the tunnel's pre-shared key. A PPTP tunnel is for routers and modems that have nothing else. It requires MS-CHAPv2 with 128-bit MPPE and refuses a device without encryption.

The form warns about PPTP, and the warning stands: PPTP's encryption has been broken for years, and a recording of a session can be read. Offer it only to a device that has nothing better, and prefer WireGuard, OpenVPN or L2TP/IPsec wherever a device has one.

A server carries one tunnel of each of these kinds, because the devices' built-in clients use fixed ports. The panel runs both through its own agent: strongSwan and `xl2tpd` for L2TP, and a PPTP server of the panel's own with `pppd`, started when a tunnel is made and stopped when it goes.

### OpenConnect (Cisco AnyConnect)

An OpenConnect tunnel is what Cisco Secure Client (formerly AnyConnect) dials, and the `openconnect` client on Linux and OpenWrt. It is a VPN inside TLS on TCP, and once a device is in, DTLS on UDP on the same port, which is faster. 443 is the default, where it looks like any website; any free port works. A server carries one, on this server or on a node running 3.0 or later.

- **Sign-in:** a username and password per device, as on OpenVPN and L2TP. The server checks each sign-in with the panel's agent. The agent knows each device's password and address, and ends a device's other session before it lets the new one in, so a second device on one sign-in takes the connection over. A device that was cut off cannot come back on its old session either.
- **DTLS:** on by default. Off sends everything over TLS on TCP.
- **Camouflage:** on by default. The server answers like an ordinary website unless the address carries its secret, so a scanner or a filter that probes it sees nothing to block. Customers dial the address with `/?secret` on it, and their page gives it whole. It needs ocserv 1.2 or later (Ubuntu 24.04, Debian 13). On an older one the tunnel runs without camouflage, and the panel says so in its log.
- **Certificate:** with **Use the panel's certificate**, a tunnel whose address the panel has a certificate for uses it, and devices trust it as it is. Otherwise the tunnel makes one of its own and renews it. In that case Cisco Secure Client has to be told to accept it (*Block Untrusted Servers* off), and the customer's page says how.

The installer installs ocserv, and the panel installs it when the first tunnel is made if it is missing. The panel's agent runs it from its own files and leaves it running across the panel's updates.

### AmneziaWG obfuscation

Choosing AmneziaWG opens the obfuscation values in the form, already filled in. The panel draws them for the server the tunnel is on: valid as AmneziaWG checks them, fitted to the tunnel's MTU, and unlike every other AmneziaWG tunnel on that server, with no shared identifier, no shared handshake padding and, while there are kinds left, a different packet before the handshake. They are kept small enough to cost a busy tunnel little: a few junk packets of at most 250 bytes, and data padding of at most 20. Change any of them before creating, or later with **Edit**.

There are two kinds of profile. **Modern (AmneziaWG 2)** has identifier ranges and a disguised packet before each handshake, which the current AmneziaWG and AmneziaVPN apps read. **Compatible (every app)** is what every AmneziaWG app has ever read, for devices that cannot be updated. A node running W-UI before 3.0 always gets a compatible one.

The values:

- **Jc, Jmin, Jmax:** the junk packets sent before each handshake.
- **S1 to S4:** the padding on each message type. S2 must not be S1 + 56, and S4 has to fit the link: at most 1440 minus the MTU.
- **H1 to H4:** the value each message type is marked with, above 4. Give a second value to make it a range (AmneziaWG 2.0), and each packet then carries a value of its own from it. The four must not overlap.
- **HeaderProtectionKey:** AmneziaWG 3 header protection, a key in base64. It needs S1 to S4 of at least 12.
- **I1 to I5:** packets sent before each handshake to look like another protocol. Pick a ready one (QUIC, a DNS query, STUN), or write your own from the tags `<b 0x…>`, `<r N>`, `<rc N>`, `<rd N>` and `<t>`, which both the kernel module and the phone apps read.

Every value is checked as AmneziaWG checks it, and a refusal names the field. **Draw new values** replaces the numbers and keeps I1 to I5. After any change, every customer on the tunnel needs their file again. Ranges and I1 to I5 need AmneziaWG 1.5 or later on the device, and W-UI 3.0 or later on a node.

A customer's page also offers each AmneziaWG file as an **AmneziaVPN** key, to copy or scan into the AmneziaVPN app. It is the same file.

The installer keeps the AmneziaWG kernel module working across kernel upgrades. Each new kernel gets its headers, so DKMS rebuilds the module as the kernel installs, and `wui-amnezia-kmod` builds it at boot if that did not happen. `amneziawg-go` is installed besides, so a kernel the module cannot be built for still carries the tunnels.

### IKEv2

An IKEv2 tunnel is the VPN built into Android 12 and newer, iPhone, Mac and Windows. It is on UDP 500 and 4500, and on a port of its own if given one (see below). A server carries one, on this server or on a node running 3.0 or later, and it shares strongSwan with an L2TP tunnel on the same server. Every session runs through the tunnel's own device, so speed limits apply to it as to every other tunnel.

Choose the ways a device may sign in; all four are on by default:

| Way | Devices | What the device needs |
|---|---|---|
| Username and password (MSCHAPv2) | Android, iPhone, Mac, Windows | Its sign-in, and the server's certificate installed once |
| Key (PSK) | Android, iPhone, Mac | Its username as the identity, and its password as the key |
| Certificate (RSA) | Android, iPhone, Mac | Its own `.p12` file |
| Certificate in EAP (EAP-TLS) | Android | Its own `.p12` file |

The panel makes the tunnel's certificate authority and the server's certificate, issues each device its own, and renews them, for a tunnel on a node as for one here. The customer's page offers the server's certificate and the device's `.p12`, which opens only with the device's password.

### Ports of their own: L2TP, PPTP and IKEv2

These three have standard ports: UDP 1701 inside IPsec for L2TP, TCP 1723 for PPTP, and UDP 500 and 4500 for IKEv2. The VPN built into Windows, iPhone, macOS and Android has nowhere to type another, so the standard port always stays open.

A tunnel can still be given a port of its own, for places where the standard one is blocked. The panel redirects it to the standard one, so both work. Only clients that take a port use it: the strongSwan app for IKEv2, Linux, and routers with a port setting. The form says so when the port is not the standard one, and the customer's page shows it as **Port (apps with a port setting)**. Another tunnel on the server cannot take that port.

### L2TP/IPsec: two devices behind one address

L2TP/IPsec cannot carry two devices that reach the server from the same public address at once, such as two phones on one home Wi-Fi or on one mobile carrier's shared address. Both run IPsec in transport mode on the same port, and the Linux kernel cannot tell them apart, so only one of them works. This holds for every Linux L2TP server, not only this panel. Where it matters, give those customers IKEv2, WireGuard or OpenConnect, which have no such limit.

### The server's firewall

With ufw or firewalld on, a tunnel's port is dropped there whatever the panel's own rules say. So the panel lets each tunnel's ports through: every change of the tunnels is written for `wui-firewall.service`, which opens what is new and closes what it opened before and no tunnel uses now. A rule that was there before, yours, is never closed. With neither firewall on, nothing is changed. PPTP's GRE and IPsec's ESP are let through as well.

### Changing an interface later

Everything except the protocol and the server can be changed after creation.

- **Endpoint, DNS, MTU and egress interface** apply in place and disconnect nobody. Customers' subscription links carry the new values; a configuration already imported into an app keeps the old ones until it is fetched again.
- **Name, port, subnet, mode and transport** take the tunnel down and bring it up again as new. Every customer on it needs their configuration again. The subscription link already carries the new one, so a customer who uses the link only has to refresh it.
- **A new subnet** also gives every device a new address, and is refused when it is too small for the devices already on it.

## Good to know

- **Several interfaces, one customer.** A customer on WireGuard and OpenVPN gets a configuration for each, and spends one allowance and one number of users across both.
- **A second interface as a fallback.** An AmneziaWG clone, OpenVPN over TCP 443, or OpenConnect on 443 keeps customers connected where plain WireGuard is filtered.
- **Interfaces on nodes** are run by the node's own panel; this panel sends it the interface and the customers. See [Nodes](/panel/nodes).
