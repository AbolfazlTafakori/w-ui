---
description: "The addresses customers' configurations point at. Without hosts, every configuration dials the interface's endpoint; with them, a customer gets one configuration per host, such as a CDN front, a second address, or a domain and an IP."
---

# Hosts

A host is an address customers connect to. Without hosts, every configuration dials the interface's own **Endpoint**. With hosts, one customer fans out to several addresses, such as a CDN front, a second IP, or a domain and an IP of the same server, and gets one configuration, one QR code and one subscription entry **per host**. When one address is blocked, the customer switches to another without a new file from you.

Hosts apply to WireGuard and AmneziaWG interfaces, and to OpenVPN as extra `remote` lines; see [What a customer gets](#what-a-customer-gets). OpenConnect, IKEv2, L2TP/IPsec and PPTP always use the interface's own endpoint.

One entry on this page is one name over several addresses and several interfaces. It is stored as one host per address per interface, and shown and edited as one.

## The summary card

**Total**, **Enabled** and **Disabled**: how many host entries there are.

## The toolbar

**Add Host** opens the form; it is greyed out until an interface exists. With entries ticked, the toolbar shows their count with a ×, and **Enable**, **Disable** and **Delete** for all of them.

## The table

| Column | What it shows |
|--------|---------------|
| ☐ | Tick to select. |
| **Actions** | ↑ and ↓ move the entry up or down; the order is the order customers' configurations are listed in. ⚡ **Check now** tries each of the entry's addresses the way a customer's device would, and shows the result. ✎ edits; 🗑 deletes after a confirmation. |
| **Enable** | A switch. A disabled host is kept, but not handed out. |
| **Remark** | The name, with the description under it. It also names the configuration a customer gets for this host, such as `Roya-cdn.conf`. |
| **Endpoint** | The addresses and the port. **Inherits** when no address was given, so the interface's own is used. |
| **Interfaces** | The interfaces this host applies to. |
| **Reachable** | **Reachable** or **Unreachable**, as the last check found. Hover over an unreachable one to see why. A new host shows Reachable until it has been checked. |
| **Tags** | Your own labels for the entry. |

### What Check now proves

It connects to each address on the host's port. For OpenVPN over TCP, that shows the tunnel answering. A WireGuard address is UDP and does not answer a connection attempt, so for WireGuard it proves only that the name resolves and the address can be reached. The message says so.

## The form

**Basic:**

| Field | What it is | Limits |
|-------|------------|--------|
| **Remark** * | The host's name. The configuration a customer gets for it is named after it. | 1 to 256 characters. |
| **Description** | A note shown under the remark. | Up to 64 characters. |
| **Interfaces** * | The interfaces this host applies to. | At least one. |
| **Address** | One or more addresses, such as `cdn.example.com`, `203.0.113.7` or `cdn2.example.com:443`. Each becomes its own entry in the customer's list. Leave it empty to use the interface's own address. | A host name or an IP, with an optional `:port`. |
| **Port** | The port customers dial. | 0 to 65535. `0` uses the interface's port. A port written in an address wins over this. |
| **Tags** | Labels only you see. | Letters, digits, `_` and `:`; no commas or spaces. |
| **Enable** | Off keeps the entry without handing it out. | |

**Advanced:**

| Field | What it is |
|-------|------------|
| **Exclude from formats** | The formats this host is left out of: **conf**, the subscription's plain text; **base64**, its encoded form; **zip**, the archive of files; **page**, the subscription page in a browser. |
| **Shuffle host** | Hands the addresses out in a random order each time the subscription is read, so customers spread over them. |

## What a customer gets

- **WireGuard and AmneziaWG:** one configuration per enabled host address, in the table's order, or shuffled when any of the hosts asks for it. Once a host exists, the interface's own endpoint is no longer handed out by itself; to keep it in the list, add a host with an empty address. With no enabled host, the customer gets one configuration for the interface's endpoint.
- **OpenVPN:** one profile, whose `remote` lines are the interface's endpoint first and then every enabled host in order. The OpenVPN app tries them in turn until one answers. **Exclude from formats** and **Shuffle host** do not apply.
- **OpenConnect, IKEv2, L2TP/IPsec and PPTP:** the interface's endpoint only.

A customer's subscription link picks up a new, changed or removed host at its next refresh.

## Precedence

A host's address wins, and an empty address inherits the interface's endpoint. For the port, a port written in the address wins, then the host's **Port**, and `0` inherits the interface's port.
