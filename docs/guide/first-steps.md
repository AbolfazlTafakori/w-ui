---
description: "A fresh panel is empty on purpose: no interfaces, no customers, one administrator. Three steps get a customer connected."
---

# First tunnel, first customer

A fresh panel is empty on purpose: no interfaces, no customers, one administrator. Three steps get a customer connected.

## 1. Create an interface

Go to **Interfaces → Add Interface**. An interface is one tunnel: a protocol on a port, handing out addresses from a range, at the public address customers connect to.

| Field | Note |
|-------|------|
| Server | This server, or one of your nodes. On a 3x-ui or Pasarguard node, the form makes that panel's own inbounds instead. |
| Protocol | WireGuard, OpenVPN, OpenConnect, IKEv2, L2TP/IPsec or PPTP; the protocols installed on the server are offered |
| Interface | The tunnel's name, which is also the network device's name (`wg0`) |
| Endpoint | The hostname or IP address written into customers' files |
| Port | UDP for WireGuard. OpenVPN can use TCP instead, and on 443 it gets through where little else does |
| Subnet | `10.66.0.0/16` gives about 65,000 addresses; the first becomes the gateway |
| MTU | `1420` suits most networks |
| DNS | Given to customers, unless the panel's own resolver is on, which it is by default |
| Egress interface | Your public network card, usually `eth0` |
| Mode | For WireGuard: Standard, or AmneziaWG, an obfuscated WireGuard for networks that block the plain one |

The keys are generated for you, and the private key never appears in anything you can copy out of the panel. For OpenVPN, a certificate authority, a server certificate and a `tls-crypt` key are generated and stored with the interface, so a backup of the database is a complete backup. Every field, and each protocol's own settings, are on [Interfaces](/panel/interfaces).

## 2. Create a customer

Go to **Clients → Add Clients**. One client is one customer and their plan.

| Field | Note |
|-------|------|
| Name | What you will search for; their files are named after it |
| Data allowance | Empty or `0` is unlimited. The kernel enforces it |
| Users | How many people the plan is for: one file each, each on one device at a time |
| Valid for | How long from now. Or turn on **Start when they first connect** with a number of days, counted from the first connection |
| Speed limit | Download speed in Mbit/s; `0` is no limit |
| Renews | Never, daily, weekly or monthly: the traffic comes back each period |
| Telegram ID | Lets the customer ask the bot about their own plan |
| Servers this customer can use | The interfaces they are on, one or several |

**Presets** fill in a typical plan in one click. **more → Add Bulk** makes up to 200 customers at once from a name prefix and a count. Every field is on [Clients](/panel/clients).

## 3. Give the customer their configuration

On the customer's row:

- **QR Code:** one per user, per interface, and per host. A phone scans it with the app for that tunnel, such as WireGuard or AmneziaWG.
- **Client Information:** every file and link, to copy or download.
- **Subscription link:** one address the customer keeps. The page behind it lists every file with its QR code, in the customer's language and in the template you chose. Apps that read subscriptions fetch it again on their own, so a change you make later reaches them without a new file.

Which app on which device: [Client apps](/guide/client-apps).

## What the statuses mean

| Status | Meaning |
|--------|---------|
| **Active** | Working |
| **Disabled** | Switched off by you |
| **Out of data** | Used the whole data allowance |
| **Expired** | Past the end date |
| **On hold** | Waiting for the first connection to start the clock |

A customer who is not active or on hold has their files taken out of the kernel, so their traffic stops. More data, more time, a reset or a renewal brings them straight back, with no restart and no new file.

## How OpenVPN customers differ

WireGuard recognises a customer's file by its key. OpenVPN customers get a username and password instead, and there is **no certificate per customer**: making a customer adds a sign-in, not a certificate to issue and later revoke. Three things follow:

- **One sign-in serves one person.** A second connection with it ends the first.
- **Addresses stay fixed,** so the kernel keeps counting for the right person across reconnects.
- **Cutting someone off is immediate.** The sign-in is removed *and* the live session is ended.

Adding or removing a customer never restarts the server, and restarting or updating the panel disconnects nobody.
