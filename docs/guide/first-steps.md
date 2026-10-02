---
description: "A fresh panel is empty on purpose: no interfaces, no customers, one administrator. Three steps get a customer connected."
---

# First tunnel, first customer

A fresh panel is empty on purpose: no interfaces, no customers, one administrator. Three steps get a customer connected.

## 1. Create an interface

**Interfaces → Add Interface.** An interface is one tunnel: a protocol on a port, handing out addresses from a range, at the public address customers dial.

| Field | Note |
|-------|------|
| Protocol | WireGuard or OpenVPN |
| Interface | its name, which is also the network device's name (`wg0`) |
| Endpoint | the hostname or IP that goes into customers' configurations |
| Port | UDP for WireGuard; OpenVPN can use TCP — on 443 it passes where little else does |
| Subnet | `10.66.0.0/16` gives about 65,000 addresses; the first becomes the gateway |
| MTU | `1420` suits most networks |
| DNS | handed to customers, unless the panel's own resolver is on (it is by default) |
| Egress interface | your public network card, usually `eth0` |
| Mode | Standard, or AmneziaWG — obfuscated WireGuard for networks that block the plain one |

The keys are generated for you, and the private key never appears in anything you can copy out of the panel. For OpenVPN, a certificate authority, server certificate and `tls-crypt` key are generated and stored with the interface, so a database backup is a complete backup. Every field is explained on [Interfaces](/panel/interfaces).

## 2. Create a customer

**Clients → Add Clients.** One client is one customer and their plan.

| Field | Note |
|-------|------|
| Name | what you will search for; their files are named after it |
| Data allowance | empty or `0` is unlimited; enforced in the kernel |
| Users | how many people the plan is for: one file each, each on one device at a time |
| Valid for | how long from now; or **On hold** with a number of days, counted from the first connection |
| Speed limit | download, in Mbit/s; `0` is uncapped |
| Renews | never, daily, weekly or monthly: the traffic comes back each period |
| Telegram ID | lets the customer ask the bot about their own plan |
| Servers this customer can use | the interfaces they are on — one or several |

**Presets** fill a typical plan in one click. **more → Add Bulk** makes up to 200 at once, with a name prefix and a count. Every field is explained on [Clients](/panel/clients).

## 3. Hand out the configuration

On the customer's row:

- **QR code** — one per user, per interface (and per host); phones scan it with the WireGuard or AmneziaWG app.
- **Client Information** — every file and link, to copy or download.
- **Subscription link** — one address the customer keeps. The page behind it lists every file with its QR code, in their language and your chosen template; apps that speak subscriptions fetch it again by themselves, so a change you make later reaches them without a new file.

Which app on which device: [Client apps](/guide/client-apps).

## What the statuses mean

| Status | Meaning |
|--------|---------|
| `active` | working |
| `disabled` | switched off by you |
| `exhausted` | used the data allowance |
| `expired` | past the end date |

Anything other than `active` has its files taken out of the kernel, so the customer stops passing traffic. More traffic, more time, a reset or a renewal brings them straight back — no restart, no new file.

## How OpenVPN customers differ

WireGuard identifies a customer's file by its key. OpenVPN customers get a username and password, and there are **no per-customer certificates**: creating one adds a login, not a certificate to issue and revoke. Three things follow:

- **one login serves one person** — a second sign-in with it ends the first;
- **addresses are pinned**, so the kernel's counting keeps following the right person across reconnects;
- **cutting someone off is immediate** — the login is removed *and* the live session is ended.

Adding or removing a customer never restarts the server, and restarting or updating the panel disconnects nobody.
