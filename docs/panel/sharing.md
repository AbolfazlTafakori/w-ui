---
description: "Customers' files used from three or more places at once — the sign a configuration has been passed on. Reported for you to decide on, never acted on by itself."
---

# Sharing

This page lists files that are being used from **three or more public addresses within ten minutes** — the sign that a configuration has been copied to other people. It is a report: nothing here disconnects anyone. Look at the addresses and decide.

Why three: two is normal. A phone moving between Wi-Fi and mobile data changes address on its own, and a mobile carrier can put unrelated customers behind one address. Three different places inside ten minutes is rarely innocent.

This is separate from the live [device limit](/panel/clients#users-and-devices), which holds a file to one device at a time and acts on its own within seconds. This page catches what slips past it over a longer window, for you to follow up on.

## The summary card

| Figure | What it is |
|--------|------------|
| **Devices Flagged** | Files on the list. |
| **Customers Involved** | The customers those files belong to. |
| **Addresses Seen** | All the addresses the flagged files were used from. |

## The list

**Refresh** reads the report again; **Search** matches a customer, a file name or an address. Above the table, *Read this before acting* repeats why this is evidence, not proof.

| Column | What it shows |
|--------|---------------|
| **Menu** | 👁 **Open**: the customer's own page, where their files can be replaced or removed. |
| **Customer** | Whose file it is. Sortable. |
| **Device** | Which of their files. Sortable. |
| **Seen from** | Every public address it was used from in the window. Sortable by how many. |
| **Since** | When this run of sharing started. Sortable. |

When nothing is suspicious: *Nothing to review. No credential has been seen from three or more places inside ten minutes.*

## What to do about a flagged file

- Ask the customer. Someone travelling, or using two phones on two networks, can explain it.
- Replace that one file: on the customer's page, or with **Replace a user's keys** in the client form. Only that file stops working; the customer fetches the new one from their link.
- If the link itself was passed on, **New keys and link** on the customer's row replaces every file and the link at once.
- Lower the customer's **Users**, or switch them off.

The [Telegram bot](/panel/telegram) and [mail notices](/panel/settings) can send the same report, under the *sharing* notice.

## Behind a relay

Nothing here depends on how traffic reaches the server — direct, through a forwarder on another machine, a tunnel endpoint, a load balancer or a relay on the server itself. An address carrying five or more different customers at once is taken for a relay, and the port the relay gives each connection is used to tell its connections apart, so the report still sees separate devices. A household behind one router is two or three customers, not five, and is read as one address as before. Nothing to configure.

Addresses are kept for 30 days after they stop being used, so a pattern can be seen.
