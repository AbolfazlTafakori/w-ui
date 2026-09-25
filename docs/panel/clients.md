---
description: "Customers. One client is one plan for a number of users; each user has a file of their own that works on one device at a time."
---

# Clients

Customers. One client is one plan for a number of **users** — one by default. Each user has a file of their own (its own key and address, on every server the customer is allowed), and each file works on one device at a time. The number beside the name is how many are connected right now against how many users the plan has.

## The summary card

Clients, online, depleted, depleting, disabled, active — six figures with a coloured dot each; on a phone, two per row.

## The toolbar

**Add Clients**, **more** → **Add Bulk** (the classic bulk dialog: a quantity, a name method — random, prefix + number from a start, or prefix + random + postfix — with a live example, then the plan they all share), **more** (bulk enable / disable / delete / reset / extend for the selection, export), and with rows selected a **Delete**. Under it the filter bar: search (name, comment, sub id, key, password), **Filter** with a badge showing how many filters are on, sort, clear all, and "shown of total".

## The table

| Column | |
|--------|--|
| Actions | QR code, client information, **new keys**, reset traffic, edit, delete |
| Enabled | a switch; disabled when the plan is expired or exhausted |
| Online | live: a device with traffic moving in the last 75 seconds, on any server, counted every two seconds — a device that connects shows within a few seconds |
| Client | name, connections in use / allowed at once (red when over), comment |
| Group | click to filter |
| Attached inbounds | which interfaces the plan is on |
| Traffic | used, a bar, the limit — red past 100 %, orange near it |
| Speed | live rate |
| Remaining | data left |
| Duration | time left, or ∞ |

Pagination with a size changer past ten rows. On a phone: cards with the status dot, the traffic bar, and one menu.

## QR code

One QR per device, per host — a customer on two hosts gets two codes per device. Downloadable as PNG.

## Client information

Everything about one plan on one screen: status, quota, expiry, devices, every link (config file, subscription, per host), the Telegram id, the sub id, when they were last seen and from where.

## New keys

The key on a customer's row issues them fresh credentials: a new WireGuard key pair and preshared key, and a new OpenVPN password, for every file they hold. Whatever was copied, forwarded or sold on stops working within seconds, and the customer takes the new files from their link.

Kept: their address, their device and user names, their OpenVPN usernames, their plan and their usage — only the secrets change, so routing rules, the IP log and the traffic figures still point at the same customer.

Rotating the **subscription link** is a different thing and does not do this: it changes where the files are fetched from, not what is inside them, so a file that leaked keeps working until its keys are rotated. From the API: `POST /api/clients/{id}/rotate-keys`, with `accountIds` to rotate named files only.

## Statuses

`active`, `disabled`, `exhausted`, `expired`. Anything but `active` has its peers removed from the kernel; raising the quota or extending the expiry brings them back at once.

## Device files

A device's file is named after the customer: `Hossein.conf` or `Hossein.ovpn` for a customer with one device, `Hossein-laptop.conf` when they hold several, in whatever script the name is written. A WireGuard app takes the tunnel's name from the file and allows fifteen characters, so a `.conf` is cut to fit.

## Ended plans

A customer who has used their allowance or passed their date is switched off by the panel itself, within a tick: the row shows **Out of data** or **Expired**, the Enabled switch is off, and their devices are removed from every server. Raising the allowance or extending the date brings them back at once.

## Start on first use

A plan can be sold as "30 days from the first connection" instead of a fixed date: the clock starts when the first handshake arrives.

In the client form switch on **On hold** and give a **Plan length** in days. Until the customer connects the row shows an **On hold** tag and the expiry column shows the days; the moment their first connection arrives the countdown begins and the expiry date appears.

## Users

A plan is for a number of **users**, one by default. Each user gets a file of their own — `Roya.conf` for a plan of one, `Roya-user-1.conf`, `Roya-user-2.conf`… for more — on every server the customer is allowed, and each file works on **one device at a time**. So a two-user plan is two single-user plans sharing one allowance and one expiry: simple to sell, simple to check.

Raising the number on an existing customer issues the missing files at once (and the subscription page shows them the same instant); lowering it removes the newest files, so the one the customer has had longest keeps working. Empty means no limit: one file, any number of devices.

**Enforcement** is live and spans every server. Every two seconds the panel looks at which files have traffic moving and where it comes from; a file used from two places at once — its address keeps flipping back and forth — is over its one device, and more files live than the plan has users is over the plan; in either case the newest is **held off for two minutes** (a WireGuard peer removed and back on its own when the hold ends, an OpenVPN session ended). A phone walking from wifi onto mobile data changes address once and is not held. Behind a relay, where every device arrives from one address, the port the relay gives each flow tells them apart. The log says `connection limit reached; device held off` with the customer, the file and until when.

## The client dialog

**Add client** and the pencil on a row open the same dialog, in three tabs:

- **Basics** — name (the ↻ draws a random one), data allowance with its unit, users, how long the plan is valid, On hold, speed limit, traffic reset, Telegram ID, note, group (a drop-down of the groups that exist, or type a new one), the servers the customer may use (Select all / Clear all), and the **Enabled** switch. The question mark beside a label explains it on hover.

  **Empty or 0 means unlimited** for the allowance and the validity, and for users it means one file with no device limit — on creation and on an edit alike: clearing the box on an existing customer removes the limit. With **On hold** switched on, the validity box becomes **Expire days**: how many days the plan runs once the customer first connects.
- **Credentials** — the OpenVPN username and password when an OpenVPN server is selected (each with ↻ to generate), the devices to issue on creation, and the **Subscription ID**: the secret in the customer's link. Type one to keep a link a customer already has (8–64 characters: letters, digits, `-`, `_`, unique), or ↻ for a new one; changing it stops the old link.
- **Links** — for an existing customer, the subscription link with copy and open; the files themselves are on the customer's page.

## OpenVPN username and password

OpenVPN logs in with a username and a password rather than a key, and **each user of a plan logs in as themselves**: the Credentials tab shows one login row per user (User 1, User 2, …), each with a username and a password and a ↻ to generate either. Type what each person should use, or leave a field empty and the panel generates it. On an existing customer the rows start with the current usernames; a blank password keeps theirs. The same login applies on every OpenVPN tunnel the customer is on, is carried inside that user's file so it connects on import, and is shown beside the user's line on the subscription page. Usernames are unique per tunnel and two users cannot be given the same one; 3 to 48 characters from letters, digits, `- _ . @`; passwords 6 to 64 characters with no spaces.

The API takes the same as `openvpnUsers: [{username, password}, …]` in the plan's order; the older `openvpnUsername` / `openvpnPassword` pair still sets user 1.
