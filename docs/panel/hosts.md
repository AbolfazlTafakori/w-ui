---
description: "The addresses customers' configurations point at. Without hosts, every configuration dials the interface's endpoint; with them, a customer gets one configuration per host — a CDN front, a second address, a domain and an IP."
---

# Hosts

A host is an address customers connect to. Without hosts, every configuration dials the interface's own **Endpoint**. With hosts, one customer fans out to several addresses — a CDN front, a second IP, a domain and an IP of the same server — and gets one configuration, one QR code and one subscription entry **per host**. When one address is blocked, the customer switches to another without a new file from you.

One entry on this page is one name over several addresses and several interfaces: it is stored as one host per address per interface, and shown and edited as one.

## The summary card

**Total**, **Enabled** and **Disabled**: how many host entries there are.

## The toolbar

**Add Host** opens the form (greyed out until an interface exists). With entries ticked: the count with its ×, **Enable**, **Disable** and **Delete** for all of them.

## The table

| Column | What it shows |
|--------|---------------|
| ☐ | Tick to select. |
| **Actions** | ↑ / ↓ move the entry up or down — the order is the order customers' configurations are listed in. ⚡ **Check now** tries each of the entry's addresses the way a customer's device would and shows the result. ✎ edits, 🗑 deletes after a confirmation. |
| **Enable** | A switch. A disabled host is not handed out; it is kept. |
| **Remark** | The name — it also names the configuration a customer gets for this host (`Roya-cdn.conf`) — with the description under it. |
| **Endpoint** | The addresses and port. **Inherits** when no address was given, so the interface's own is used. |
| **Interfaces** | The interfaces this host applies to. |
| **Reachable** | **Reachable** or **Unreachable**, as the last **Check now** found; hover an unreachable one for why. A new host shows Reachable until it has been checked. |
| **Tags** | Your own labels for the entry. |

### What Check now proves

It connects to each address on the host's port. For OpenVPN over TCP that shows the tunnel answering. A WireGuard address is UDP and does not answer a connection attempt, so for WireGuard it proves only that the name resolves and the address can be reached — the message says so.

## The form

**Basic:**

| Field | What it is | Limits |
|-------|------------|--------|
| **Remark** * | The host's name; the configuration a customer gets for it is named after it. | 1–256 characters. |
| **Description** | A note shown under the remark. | Up to 64 characters. |
| **Interfaces** * | The interfaces this host applies to. | At least one. |
| **Address** | One or more addresses — `cdn.example.com`, `203.0.113.7`, `cdn2.example.com:443`. Each becomes its own entry in the customer's list. Leave empty to use the interface's own address. | A host name or an IP, with an optional `:port`. |
| **Port** | The port customers dial. | 0–65535; **0** uses the interface's port. A port written in an address wins over this. |
| **Tags** | Labels only you see. | Letters, digits, `_` and `:`; no commas or spaces. |
| **Enable** | Off keeps the entry without handing it out. | — |

**Advanced:**

| Field | What it is |
|-------|------------|
| **Exclude from formats** | Formats this host is left out of: **conf** (the subscription's plain text), **base64** (its encoded form), **zip** (the archive of files), **page** (the subscription page in a browser). |
| **Shuffle host** | Hands the addresses out in a random order each time the subscription is read, so customers spread over them. |

## What a customer gets

- **WireGuard**: one configuration per enabled host address, in the table's order (shuffled when any host asks for it). Once a host exists, the interface's own endpoint is no longer handed out by itself — add a host with an empty address to keep it in the list. With no enabled host, the customer gets one configuration for the interface's endpoint.
- **OpenVPN**: one profile, whose `remote` lines are the interface's endpoint first and then every enabled host in order. The OpenVPN app tries them in turn until one answers. **Exclude from formats** and **Shuffle host** do not apply.
- A customer's subscription link picks up a new, changed or removed host at its next refresh.

## Precedence

A host's address wins; an empty address inherits the interface's endpoint. A port written in the address wins, then the host's **Port**, and **0** inherits the interface's port.
