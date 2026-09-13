---
description: "What 3x-ui calls inbounds: the tunnels this server offers. One interface is one subnet on one port with one protocol."
---

# Interfaces

What 3x-ui calls **inbounds**: the tunnels this server offers. One interface is one subnet on one port with one protocol.

## The summary card

Total sent / received, total usage, and how many interfaces exist.

## The toolbar

**Add Inbound**, **General Actions** (import from a file, export every customer's links, reset all traffic), and a search that matches the name, port or protocol. With rows selected: the count, and **Delete**.

## The table

| Column | |
|--------|--|
| ID | sortable |
| Menu | edit, and the row menu: information, QR of every customer, reset traffic, clone, restart, delete |
| Enabled | a switch; off removes every peer from the kernel without deleting anything |
| Remark | the interface's name |
| Node | which server carries it, when nodes are attached |
| Port | the listen port |
| Protocol | `wireguard` / `openvpn`, the transport, and **AmneziaWG** when obfuscated |
| Clients | total, active, disabled, depleted, online |
| Traffic | used / limit |
| Speed | live rate |
| Duration | expiry, when set |

On a phone the table becomes a list of cards; tap the ⓘ for the figures.

## The form

**Basic:** name, protocol, mode (standard or Amnezia), subnet, listen port, endpoint host, DNS handed to clients, MTU, NAT interface.

**AmneziaWG** adds the obfuscation parameters (Jc, Jmin, Jmax, S1–S4, H1–H4); generated for you, editable for a client that needs specific ones.

**OpenVPN** adds the transport (UDP or TCP), cipher, and whether to push a redirect-gateway.

Editing an interface that is up applies the change in place. Only a change to the tunnel itself (port, keys, subnet) restarts it; a name or DNS change does not disconnect anyone.
