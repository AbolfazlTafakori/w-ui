---
description: "Customers. One client is one plan — an allowance, a time, a number of users — on one or more interfaces; each user has a file of their own that works on one device at a time."
---

# Clients

A client is one customer and the plan they bought: how much traffic, for how long, for how many users, on which interfaces. Each user of a plan gets a file of their own — its own key and address on every interface the customer is on — and each file works on one device at a time.

The page refreshes itself every 3 seconds, and pauses while a form, a QR window or a confirmation is open.

## For a reseller: Your account

A [reseller](/panel/operators) sees a strip above everything else with what they bought and what is left of it:

| Item | What it shows |
|------|---------------|
| **Users** | Users sold against the limit — each customer counts for the users their plan is for. Orange when the limit is reached. |
| **Traffic left** | What is left of the reseller's own allowance, which their customers spend between them. Orange from 80 % used, red when spent. |
| **Time left** | Days until the reseller's term ends. Orange in the last week, red when it has ended. |

When the reseller is switched off, their term has ended or their allowance is spent, a line under the strip says so and why: their customers are paused until it is fixed, and nothing can be added or changed meanwhile.

## The summary card

Six figures, each with a coloured dot (two per row on a phone):

| Figure | Counts |
|--------|--------|
| **Clients** | Every customer. |
| **Online** | Customers with traffic moving right now (see [Online](#online)). |
| **Ended** | Out of traffic plus past their date. |
| **Depleting** | Active customers who have used 80 % or more of their allowance — the moment to sell a renewal. |
| **Disabled** | Customers switched off by hand. |
| **Active** | Customers whose plan is running. |

When no interface exists yet, a yellow notice says so, with a link to create one; **Add Clients** stays greyed out until there is one. A reseller who has not been given any interface is told to ask the owner for one.

## The toolbar

| Control | What it does |
|---------|--------------|
| **Add Clients** | Opens the [client form](#the-client-form). Replaced by the selection count once customers are ticked; its × clears the selection. |
| **more** | The [more menu](#the-more-menu). Its items depend on whether customers are selected. |
| **Delete** | Only with customers selected: deletes them after a confirmation; five or more ask you to type how many. |

### The filter bar

| Control | What it does |
|---------|--------------|
| **Search** | Matches the name and the note, as you type. |
| **Filter** | Opens the [filter panel](#the-filter-panel). A badge shows how many kinds of filter are on, and each one shows as a chip under the bar; a chip's × removes just that filter. |
| **Sort** | Oldest first (the default: a customer keeps their place as others are added), Newest first, Recently updated, Recently online, Name A→Z, Name Z→A, Most traffic, Highest remaining, Expiring soonest. |
| **Clear all** | Shown while anything narrows the list: clears the search, every filter and the group filter at once. |
| *shown of total* | How many customers the filters match, of all of them. |

Clicking a group chip in the table filters the list to that group.

### Selecting across pages

Ticking the box in the table's header selects the customers on this page. When there are more, a line offers **Select all N customers** — or, with a filter on, **Select all N customers that match** — which selects every one of them across every page. Changing the search or a filter afterwards clears such a selection, so an action never reaches customers you can no longer see.

## The filter panel

**Filter** opens a side panel. Every part narrows the list together with the others; inside one part, several choices mean *any of them*.

| Part | Narrows to |
|------|-----------|
| **Status** | Active, Depleting, Out of data, Expired, Disabled, Online — tick any number. |
| **Protocol** | WireGuard and/or OpenVPN. |
| **Interfaces** | Customers on any of the chosen interfaces. |
| **Groups** | Customers in any of the chosen groups. |
| **Expires** | An end date from … to …; either end may be left open. |
| **Traffic** | Used traffic from … to … GB. |
| **Renews** | All, renewing, or not renewing. |
| **Note** | All, has a note, or has none. |

**Clear all** in the panel clears it; **Done** closes it.

## The table

| Column | What it shows |
|--------|---------------|
| ☐ | Tick to select. |
| **Actions** | QR Code · Client Information · **New keys and link** · Reset Traffic · Edit · Delete — each described below. |
| **Enabled** | A switch to turn the customer on or off. It cannot be switched while the plan has ended (out of data or expired: the panel switched it off, and more traffic or time brings it back) or while the reseller who sold it is paused. |
| **Online** | The customer's state: **Online** (green, with a dot), **Offline**, **Disabled**, **On hold** (a plan waiting for its first connection), **Depleting** (80 % used or more), **Out of data**, **Expired**, or **Paused** (their reseller is switched off or ran out; hover for why, and for what happens when the reseller is back). Hover any other state for when they were last online. |
| **Client** | The name — click it to open the [customer's page](#the-customer-page) — then connections in use now / users allowed (red when over), and the note. |
| **Groups** | Shown when any group exists. The first group as a chip; *+N* for the rest, named on hover. Click a chip to filter by it. |
| **Attached interfaces** | The first interface the customer is on (gold for WireGuard, orange for OpenVPN), *+N* for the rest, named on hover. |
| **Traffic** | Used, a bar, and the allowance (∞ for none). Hover for up ↑ and down ↓. The bar is green, orange from 80 %, red when the allowance is used, grey while switched off, and a purple wash for an unlimited plan. |
| **Speed** | What the customer is moving right now: ↑ up / ↓ down per second, averaged over the last few seconds, from the kernel's own counters. "—" while they move nothing. A customer on a node shows that node's last report, about every 20 seconds. |
| **Remaining** | Traffic left: green, orange from 80 % used, red when none is left, ∞ for an unlimited plan. |
| **Duration** | Time left: green, orange in the last 3 days, red once passed, ∞ for no end date; hover for the exact date. A plan waiting for its first connection shows its length, such as **30d**, in blue. |

Below the table: the total, the pages, and — past ten customers — the page size (10, 25, 50, 100 or 200 per page; it starts at *Settings → Pagination Size*).

### On a phone

The table becomes a list of cards. Each card has the tick box, a coloured status dot, the name (a link to the customer's page), **Ended** or **Depleting** when it applies, ⓘ for Client Information, the switch and ⋮ — QR Code, New keys and link, Reset Traffic, Edit, Delete. Below: the note, the traffic bar and, while they move something, the speed.

### Online

A customer is online while one of their files has had traffic moving within the last 75 seconds, on any server. The panel looks every 2 seconds, so a device that connects shows within a few seconds. A WireGuard app with keepalive on sends something every 25 seconds, so an idle but connected phone stays online; one that has not sent anything for 75 seconds is offline.

## Row actions

| Action | What it does |
|--------|--------------|
| **QR Code** | A window with one tab per interface, and the subscription link first. Inside an interface, pick the user (as buttons while there are few, as a list past four) to see their QR code; for OpenVPN, their username and password too. Each code can be copied or saved as a picture. |
| **Client Information** | Everything about the customer in one window: status, switch, traffic, remaining, duration, groups, note, Telegram ID, subscription ID, users, attached interfaces, last online, when created and updated, the subscription link, and every user's WireGuard and OpenVPN file to copy, download or show as QR. **IP Log** lists each file with its address inside the tunnel. |
| **New keys and link** | Issues the customer fresh credentials for every file — a new WireGuard key pair and preshared key, a new OpenVPN password — and a new subscription link, after a confirmation. Whatever was copied, forwarded or sold on stops working within seconds; the customer needs the new link. Kept: the addresses, the file and user names, the OpenVPN usernames, the plan and the usage. |
| **Reset Traffic** | Sets the customer's used traffic back to zero. One cut off for running out is back at once. |
| **Edit** | Opens the client form for this customer. |
| **Delete** | Deletes the customer after a confirmation. Every file they hold stops working at once, their addresses go back to the pool, and their usage is gone. There is no undo. |

## The more menu

**With nothing selected:**

| Item | What it does |
|------|--------------|
| **Add Bulk** | Creates many customers at once — see [Add Bulk](#add-bulk). |
| **Export clients** | Downloads every customer as a JSON file (`wui-clients-DATE.json`): names, notes, groups, allowances, usage, end dates, users, speed limits, renewal, state. No keys: it records who was sold what. |
| **Import clients** | Paste an export — the whole file, or just its list of customers — to create them again on the first interface. A name that already exists is skipped. Every imported customer gets new keys, so the files from the old server do not work here; hand out the new links. Usage is not carried over. |
| **Reset all client traffic** | Sets every customer's used traffic back to zero, after a confirmation. |
| **Add or remove time** / **Add or remove traffic** | Greyed out: they work on a selection. Tick customers first. |
| **Delete depleted** | Deletes every customer who is out of data or past their date, after a confirmation naming how many. |
| **Delete unattached clients** | Deletes every customer who is on no interface at all. |

**With customers selected:**

| Item | What it does |
|------|--------------|
| **Add servers to the users** | Adds the interfaces you choose to every selected customer. Each gets a file per user on each new interface; their link picks them up. Anyone it cannot be done for — no addresses left on that interface, say — is named with the reason. |
| **Remove servers from the users** | Removes the interfaces you choose from every selected customer. A customer who would be left with no interface is refused and named. |
| **Add to group** | Puts every selected customer into a group; pick one or type a new name. |
| **Ungroup** | Takes every selected customer out of all groups. |
| **Enable** / **Disable** | Switches every selected customer on or off. A plan that has ended is cut off again within a tick: it needs more traffic or time, not the switch. |
| **Add or remove time** | See [Adding or removing time and traffic](#adding-or-removing-time-and-traffic). |
| **Add or remove traffic** | The same, for traffic. |
| **Set traffic limit and reset cycle** | Sets one allowance (with its unit) and/or one renewal cycle on every selected customer. A field left blank is not changed. |
| **Reset Traffic** | Sets the used traffic of every selected customer back to zero, after a confirmation. Anyone out of data is back on. |
| **New keys and link** | New keys and a new link for every selected customer, one after another; five or more ask you to type how many. A customer that fails is named and the rest are still done. |
| **Sub links** | Every selected customer's subscription link, one per line, to copy. |

## Adding or removing time and traffic

**more → Add or remove time** and **more → Add or remove traffic** change every selected customer at once. Nothing happens with nothing selected.

The window has **Add / Take back** and three fields: **months, days, hours** for time (a month is 30 days, a day 24 hours), **TB, GB, MB** for traffic (in 1024s, as the panel shows sizes). As you type, the panel works out what will happen to exactly this selection and shows it before anything is applied: how many change, how many come back on, how many stop, and how many are left alone and why. Taking back from five or more asks you to type how many.

| Customer | Time | Traffic |
|---|---|---|
| no end date / no traffic limit | left as they are | left as they are |
| running | their own end date moves | their allowance moves; what they used stays |
| ended by date | their own end date moves: ended 5 days ago and given 2, still ended; ended a day ago and given 2, one more day | allowance moves, still ended by date |
| out of traffic | date moves, still out of traffic | back on when the new allowance is above what they used |
| switched off | gets it, stays off | gets it, stays off |
| starts on first connection, not connected yet | left alone, unless **Also customers who have not connected yet** is ticked: then their plan gets longer, to the hour, and still starts on first connection | allowance moves |
| taken back | can end a plan | stops at what the customer has used — never below it, and never to no limit |

A reseller's selection reaches only their own customers.

## The client form

**Add Clients** and ✎ open the same form, in three tabs. A value the server refuses is shown under its field, and the form switches to the tab it is on. The **?** beside a label explains it on hover.

### Basics

On a new customer, **Presets** fill the plan in one click: 30 GB · 30 days · 1 user, 50 GB · 30 days · 2 users, 100 GB · 30 days · 3 users, 200 GB · 60 days · 3 users.

| Field | What it is | Limits |
|-------|------------|--------|
| **Name** * | The customer's name; ↻ draws a random one. Their files are named after it. | Up to 64 characters in the form, no line breaks. Names do not have to be unique, but importing goes by name. |
| **Data allowance** | Traffic for the whole plan, across every interface, in MB, GB or TB. | Empty or 0 is unlimited — on an edit too: clearing it removes the limit. |
| **Users** | How many people the plan is for. Each gets a file of their own, and each file works on one device at a time. Raising it issues the missing files at once; lowering it removes the newest, so the oldest keeps working. | 0–50. Empty or 0 is one file with no device limit. A [reseller](/panel/operators) with a user limit must give 1 or more. |
| **Valid for** | How long the plan runs from now, in hours, days or months. | Empty or 0 never expires — on an edit too. Saving a value restarts the countdown from now. |
| **On hold** | The plan waits until the customer first connects, then runs for its length. The field above becomes **Expire days**: how long it runs once started. | A plan on hold needs a length. |
| **Speed limit** | A cap on the customer's download speed, in Mbit/s. Upload is not limited. | 0 is uncapped. |
| **Renews** | Never, Daily, Weekly or Monthly. A plan that renews gets its traffic back each period — see [Renewing plans](#renewing-plans). | — |
| **Telegram ID** | The customer's numeric Telegram user ID. With the [bot](/panel/telegram) on, they can ask it for their links and usage. | 0 is none. |
| **Note** | Anything to remember about them; it is searchable. | Up to 256 characters in the form. |
| **Groups** | Labels to sort customers by — a reseller, a region, a plan. Pick existing ones or type a new name. | A customer can be in several. |
| **Servers this customer can use** * | The interfaces the customer is on; **Select all** / **Clear all**. Below it, how many addresses are left on the chosen interfaces. | At least one. A reseller sees only the interfaces they were given. |
| **Enabled** | Off creates or keeps the customer switched off. | — |

### Credentials

| Field | What it is | Limits |
|-------|------------|--------|
| **Replace a user's keys** | Editing only. One line per user, with **WireGuard** and (when the customer is on an OpenVPN interface) **OpenVPN**. Issues that one user new credentials for that one kind of file, at once; their other files, the other users and the link are untouched. What the old file held stops working within seconds. | — |
| **OpenVPN login** | Only when the customer is on an OpenVPN interface. One row per user (User 1, User 2, …), each with a username and a password and ↻ to generate either. Empty fields are generated. On an edit the rows start with the current usernames, and a blank password keeps the current one. The same login works on every OpenVPN interface the customer is on, is carried inside that user's file, and is shown on the subscription page. | Username 3–48 characters of letters, digits and `- _ . @`, unique per interface, and two users cannot share one. Password 6–64 characters, no spaces. |
| **Subscription ID** | The secret part of the customer's subscription link. Type one to keep a link a customer already has, or ↻ for a new one. Changing it stops the old link. | 8–64 characters: letters, digits, `-`, `_`; unique. |
| **Devices** | Editing only: the files the customer holds. Extra files are added and removed on the customer's page. | — |

### Links

For an existing customer: the subscription link, with copy and open. A new customer's link appears once it is created. When subscriptions are switched off in *Settings*, it says so.

## The customer page

Click a customer's name to open their own page. It shows their state, traffic, allowance, users, renewal and end date, with **Enable/Disable**, **Reset Traffic** and **Delete**, and then their **Devices**: every file, with its address and when it was last seen.

- **Add** issues an extra file with the name you give it (*Phone, laptop…*), on every interface the customer is on. A customer can hold at most 64 files.
- Each file can be downloaded, shown as a QR code for the WireGuard app, or shown as text. The text carries the device's private key, so it stays hidden until you ask.
- Removing a file stops it working at once; its address goes back to the pool, and the customer keeps their other files.
- **Download all** saves every file in one zip.

## Add Bulk

**more → Add Bulk** creates up to 200 customers in one go, each with their own keys, files and subscription link, all on the same plan.

| Field | What it is |
|-------|------------|
| **Quantity** | 1–200. |
| **Name method** | **Random**: a random handle each. **Prefix + number**: your prefix with a running number from **Start from** (`shop1 … shop50`). **Prefix + random + postfix**: your prefix, a random handle and your postfix. An example of the first and last name is shown as you type. The prefix and postfix together are at most 48 characters. |
| The plan | Data allowance, Users, Valid for / On hold, Speed limit, Renews, Groups, the interfaces and Enabled — as in the client form, the same for every customer created. |

If one cannot be created, the ones made before it are kept and the message says how many were.

## Statuses

| Status | Meaning |
|--------|---------|
| **active** | The plan is running. |
| **disabled** | Switched off by hand. |
| **exhausted** (*Out of data*) | The allowance is used. The panel switches them off within a tick. More traffic, a reset or a renewal brings them back at once. |
| **expired** | The end date has passed. The panel switches them off within a tick. More time brings them back at once. |

Anything but active has its files taken out of every server; nothing is deleted.

## Renewing plans

A plan with **Renews** set to Daily, Weekly or Monthly gets its traffic back at the start of every period: what the customer used goes to zero, and one cut off for running out is back on at once. The allowance, the end date and the switch are not touched — a plan that renews monthly and ends in three months renews twice and then ends. A switched-off customer's usage is renewed too, and they stay off.

Periods are counted from the last renewal or the last time their traffic was reset; before the first, from when the plan started (its first connection, for a plan on hold) or was created. A renewal keeps the plan's own day — one sold on the 10th renews on the 10th, even if the panel was off that day — and is applied within a minute of it.

## Start on first use

A plan can be sold as "30 days from the first connection" instead of a fixed date. Switch on **On hold** and give **Expire days**. Until the customer connects, the state reads **On hold** and the duration shows the days in blue; at the first connection the countdown begins and the end date appears.

## Users and devices

A plan is for a number of **users**, one by default. Each user gets a file of their own — `Roya.conf` for a plan of one, `Roya-user-1.conf`, `Roya-user-2.conf`… for more — on every interface the customer is on, and each file works on **one device at a time**. A two-user plan is two one-user plans that share an allowance and an end date.

The limit is enforced live, across every server. Every 2 seconds the panel looks at which files have traffic moving and where it comes from. A file used from two places at once — its address keeps flipping back and forth — is over its one device; more files live than the plan has users is over the plan. Either way, the newest is **held off for two minutes**: a WireGuard device is removed and comes back by itself when the hold ends, an OpenVPN session is ended. A phone moving from Wi-Fi to mobile data changes address once and is not held. Behind a relay, where every device arrives from one address, the port the relay gives each connection tells them apart. The log says `connection limit reached; device held off`, with the customer, the file and until when.

## Device files

A file is named after the customer: `Hossein.conf` or `Hossein.ovpn` for a customer with one, `Hossein-laptop.conf` when they hold several, in whatever script the name is written. A WireGuard app takes the tunnel's name from the file and allows 15 characters, so a `.conf` name is cut to fit.

## OpenVPN logins

OpenVPN signs in with a username and a password rather than a key, and each user of a plan signs in as themselves. The API takes the logins as `openvpnUsers: [{username, password}, …]` in the plan's order; the older `openvpnUsername` / `openvpnPassword` pair still sets user 1.

## New keys, and the link on its own

**New keys and link** on a row replaces everything: every file and the subscription link. To replace one user's file on its own, use **Replace a user's keys** in the client form's Credentials tab. To change only where the files are fetched from, change the **Subscription ID**: that stops the old link, but a file that leaked keeps working until its keys are replaced.

From the API: `POST /api/clients/{id}/rotate-keys`, with `accountIds` to replace named files only. An OpenVPN password that changes also ends the session it was signed in with, so it takes effect now rather than at the next reconnect.
