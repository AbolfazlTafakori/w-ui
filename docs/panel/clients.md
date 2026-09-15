---
description: "Customers. One client is one plan, allowed a number of connections at once; each device file has its own key and address."
---

# Clients

Customers. One client is one plan, allowed a number of **connections at once**. Each device file (account) has its own key and address, and no more files are issued than connections allowed. The number beside the name is how many places the customer is connected from right now, counted from the public addresses their credentials are live from — a file used on two devices one after the other is one connection, both at the same time is two.

## The summary card

Clients, online, depleted, depleting, disabled, active — six figures with a coloured dot each; on a phone, two per row.

## The toolbar

**Add Clients** (one, or many from a prefix and a count), **more** (bulk enable / disable / delete / reset / extend for the selection, export), and with rows selected a **Delete**. Under it the filter bar: search (name, comment, sub id, key, password), **Filter** with a badge showing how many filters are on, sort, clear all, and "shown of total".

## The table

| Column | |
|--------|--|
| Actions | QR code, client information, reset traffic, edit, delete |
| Enabled | a switch; disabled when the plan is expired or exhausted |
| Online | with the last time they were seen |
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

## Statuses

`active`, `disabled`, `exhausted`, `expired`. Anything but `active` has its peers removed from the kernel; raising the quota or extending the expiry brings them back at once.

## Start on first use

A plan can be sold as "30 days from the first connection" instead of a fixed date: the clock starts when the first handshake arrives.

In the client form switch on **On hold** and give a **Plan length** in days. Until the customer connects the row shows an **On hold** tag and the expiry column shows the days; the moment their first connection arrives the countdown begins and the expiry date appears.

## OpenVPN username and password

OpenVPN logs in with a username and a password rather than a key. When an OpenVPN tunnel is among the servers chosen for a customer, two fields appear under the server picker: **OpenVPN username** and **OpenVPN password**. Type what the customer should use, or leave them empty and the panel generates a pair. On the edit form the username shows the current one; a new password only has to be typed when it should change.

The same credentials apply on every OpenVPN tunnel the customer is on. A second device on the same tunnel gets the username with a number after it (`roya-2`), because a tunnel knows its sessions by name. Usernames are unique per tunnel, 3 to 48 characters from letters, digits, `- _ . @`; passwords 6 to 64 characters with no spaces.
