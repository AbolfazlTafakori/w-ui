---
description: "Customers' files used from three or more places at once, the sign that a configuration has been passed on. Reported for you to decide on, never acted on by itself."
---

# Sharing

This page lists files that are being used from **three or more public addresses within ten minutes**, the sign that a configuration has been copied to other people. It is a report, and nothing here disconnects anyone. Look at the addresses and decide.

It has two tabs: **Now**, what is true at this moment, and **History**, every time it has happened.

Why three? Two is normal. A phone moving between Wi-Fi and mobile data changes address on its own, and a mobile carrier can put unrelated customers behind one address. Three different places inside ten minutes is rarely innocent.

This is separate from the live [limit on users](/panel/clients#users-and-devices), which holds a file to one device at a time and acts on its own within seconds. This page catches what slips past that limit over a longer window, for you to follow up on.

## The summary card

| Figure | What it is |
|--------|------------|
| **Devices Flagged** | The files on the list. |
| **Customers Involved** | The customers those files belong to. |
| **Addresses Seen** | All the addresses the flagged files were used from. |

## Now

**Refresh** reads the report again. **Search** matches a customer, a file name or an address. Above the table, *Read this before acting* repeats why this is evidence, not proof.

| Column | What it shows |
|--------|---------------|
| **Menu** | 👁 **Open**: the customer's own page, where their files can be replaced or removed. |
| **Customer** | Whose file it is. Sortable. |
| **Device** | Which of their files. Sortable. |
| **Seen from** | Every public address it was used from in the window. Sortable by how many. |
| **Since** | When this run of sharing started. Sortable. |

When nothing is suspicious, the page says *Nothing to review. No credential has been seen from three or more places inside ten minutes.*

## What to do about a flagged file

- Ask the customer. Someone who is travelling, or using two phones on two networks, can explain it.
- Replace that one file, on the customer's page or with **Replace a user's keys** in the client form. Only that file stops working, and the customer fetches the new one from their link.
- If the link itself was passed on, **New keys and link** on the customer's row replaces every file and the link at once.
- Lower the customer's **Users**, or switch them off.

The [Telegram bot](/panel/telegram) and the [mail notices](/panel/settings) send a message under the *Shared credential* event when a file first shows on the list: once, not every minute it stays there.

## History

A file leaves **Now** ten minutes after its extra places go quiet, and **History** keeps it. Every file that has been on the list, and every device [held off](/panel/clients#users-and-devices) for going over its plan's users, stays here after it leaves the list and after the customer is deleted, so you can see who keeps doing it.

| Column | What it shows |
|--------|---------------|
| **What** | *Several places*, when it was on the list, or *Held off*, when it went over its plan and was cut off for two minutes. *from the log* marks what was read back from the server's log; see below. |
| **Customer** | Whose file it was, marked *deleted* when the customer has been deleted since. The name stays as it was. |
| **Device** | Which of their files. |
| **Seen from** | Every address it was used from while it lasted. |
| **Detail** | For a hold: the plan's users, and the server that held it. |
| **When** | From when to when. One occurrence that goes on is one row; the same file again more than half an hour later is a new row. |

The summary card shows the number of records, the customers in them, and how many were holds. Records are kept for 400 days.

### The time before history was kept

Releases before this one kept no history, but they logged every device they held off, and every device that connected and where from. The first time this release starts, it reads that log back: holds as they were logged, and files that connected from three or more places inside ten minutes, which is this page's own rule. A file whose places took turns without it ever disconnecting logged only one connection, so this finds less than the page would have; but what it finds, it really saw. Those rows are marked *from the log*.

Reading the log needs the panel's user to be allowed to read the system journal, which by default it is not. If it was not allowed, **History** says so and gives the command that allows it. Run the command, then press **Read the log again**. Reading it again never adds a record twice.

## Behind a relay

Nothing here depends on how traffic reaches the server: directly, through a forwarder on another machine, a tunnel endpoint, a load balancer, or a relay on the server itself. An address carrying five or more different customers at once is taken for a relay, and the port the relay gives each connection is used to tell its connections apart, so the report still sees separate devices. A household behind one router is two or three customers, not five, and is read as one address as before. There is nothing to configure.

Addresses are kept for 30 days after they stop being used, so that a pattern can be seen. The history is kept for 400 days.
