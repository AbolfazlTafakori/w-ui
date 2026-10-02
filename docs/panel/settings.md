---
description: "The panel's own settings: where it listens and how, the account and two-factor, the Telegram bot, mail, the subscription service, and — reached from the overview — backups, the engine's state, the log and the system report."
---

# Settings

Five tabs in the sidebar — **General**, **Authentication**, **Telegram Bot**, **Email**, **Subscription** — each with inner tabs. Four more sections are reached from elsewhere: **Backups** (Overview → Backup & Restore → *All backups →*), **System** (Overview → *System*), and **Engine** and **Logs** (by their address, `/settings/engine` and `/settings/logs`).

## Saving and restarting

At the top of every tab: **Save** (enabled once something has changed) and **Restart Panel** (enabled when nothing is unsaved; asks first). Changes are kept once you press Save. Most take effect at once. These take effect at the **next restart** of the panel:

- where the panel listens — Listen IP, Listen Domain, Listen Port, URI Path;
- the panel's certificate and key;
- the trusted proxies;
- the time zone;
- where the subscription service listens, and its certificate.

Before changing the port or the path, write down the new address: after the restart the panel answers only there.

**Security warnings** above the tabs name anything that leaves the panel exposed — plain HTTP, a short or default path, two-factor off, the username still `admin`, the installer's password never changed. The account warnings are about the owner's account, each said once; operators without a second factor are named together in one line. *The panel is reachable from any address* means it listens on every interface (`0.0.0.0`), as the installer sets it up; it matters only if the panel sits behind a proxy or tunnel and should be bound to `127.0.0.1`.

## General

### General

| Setting | What it does | Limits |
|---------|--------------|--------|
| **Listen IP** | The address the panel listens on. Empty listens on all of them. | At restart. |
| **Listen Domain** | The domain the panel answers to. Empty answers on every domain and IP. | At restart. |
| **Listen Port** | The panel's port. Empty keeps the one it runs on. | 1–65535, unused. At restart. |
| **URI Path** | The secret path in the panel's address — `/AbCd…/`. | Starts and ends with `/`. At restart. |
| **Session Duration** | How long a sign-in lasts, in minutes. | — |
| **Trusted proxy CIDRs** | Addresses allowed to tell the panel the real client address, host and scheme (the `X-Forwarded-*` headers). Only for a reverse proxy in front of the panel. | Comma-separated. At restart. |
| **Panel Outbound** | The [outbound](/panel/outbounds) the panel's own traffic leaves through — its checks, the Telegram bot, talking to nodes. | Must exist. |
| **Pagination Size** | Rows per page in the tables. | 0 shows everything. |
| **Language** | The language new sessions start in. | — |

### Notifications

| Setting | What it does |
|---------|--------------|
| **Expiration Date Notification** | How many days before a customer's end date they count as expiring soon, for the notices. |
| **Traffic Cap Notification** | How many GB before the end of a customer's allowance they count as running out, for the notices. |

### Certificates

**Public Key Path** and **Private Key Path**: the certificate and key files the panel serves HTTPS with (full paths, starting with `/`). The installer fills them. Empty serves plain HTTP. At restart.

### External Traffic

**External traffic report** posts every customer's new traffic, at each collection, to the **Report address** (http or https) as JSON — `[{"email","up","down"}]` — so a billing system is told without asking the panel.

### Date and Time

**Time Zone** — scheduled jobs (backups, reports, renewals counted by the day) run in it; at restart. **Calendar Type** — Gregorian or Jalali, for dates on the pages.

### Customer defaults

What the client form starts with for a new customer: **Default traffic allowance** (GB, 0 unlimited), **Default duration** (days, 0 no end date), **Default users**, **Default speed limit** (Mbit/s, 0 unlimited) and **Default traffic reset** (never, daily, weekly, monthly).

## Authentication

### Admin credentials

To change your username or password: **Current Username**, **Current Password**, **New Username**, **New Password**, then **Confirm**. You are signed out and sign in again with the new ones.

### Two-factor authentication

**Enable 2FA** opens a window: scan the QR code with any authenticator app (or copy the key beside it), then type the code it shows. From then on signing in asks for the code as well. Turning it off asks for a code, or your password.

**Panel allowed to manage this one** is for a panel that is a [node](/panel/nodes): paste the managing panel's authority here and it must present a client certificate as well as its token, so a token read out of a log or a backup is no longer enough. Empty accepts the token alone.

### API Token

Tokens let a program — a bot, a script, a managing panel — use the [API](/reference/api) without a password. **New token** asks for a **Name** and shows the token **once**: copy it then, it is stored only as a hash. The list shows each token's name, when it was made and last used; **Delete** revokes it at once.

## Telegram Bot

### General

| Setting | What it does |
|---------|--------------|
| **Enable Telegram Bot** | Turns the bot on. |
| **Telegram Token** | The bot's token from @BotFather. A saved token shows as configured; leave it to keep it. |
| **Admin Chat ID** | The Telegram user IDs that are administrators, comma-separated. Get yours from @userinfobot, or send `/id` to the bot. |
| **Telegram Bot Language** | The language the bot speaks. |
| **Telegram API Server** | Another Telegram API server, for where api.telegram.org is blocked. Empty uses the default. |
| **Send Test Message** | Sends one message to every admin chat, to prove the settings work. |

### Notifications

**Telegram Event Notifications** — tick what the bot tells the admins about:

| Event | When |
|-------|------|
| **Out of traffic**, **Expired** | A customer is cut off. |
| **Expiring soon** | A customer is within the *Expiration Date Notification* days of their end date. |
| **Shared credential** | A file is on the [Sharing](/panel/sharing) report. |
| Outbound **Down** / **Up** | An outbound stops answering — after the number of failed checks given beside it — or comes back. |
| **Started or degraded** | The panel started, or part of it stopped working. |
| **Backup taken** | A scheduled backup was made. |
| Node **Down** / **Up** | A [node](/panel/nodes) stops or starts answering. |
| **CPU high (%)**, **Memory high (%)** | The server goes over the percentage beside it. |
| **Login attempt** | Someone signed in, or failed to. |

**Notification Time** — how often the periodic report is sent: hourly, daily, weekly, monthly, every *N* minutes or hours, or a crontab expression.

### Backup

**Automatic backup** sends the backup archive to the admin chats by itself at **When to send** (in the panel's time zone). A backup that falls due while the panel is off is sent as soon as it is back. It is the same archive *Backup & Restore* makes, and restores the same way. See [Telegram bot](/panel/telegram).

## Email

The same notices by mail, for an operator who does not use Telegram.

| Setting | What it does |
|---------|--------------|
| **Enable Email Notifications** | Turns mail on. |
| **SMTP Host**, **SMTP Port** | The mail server, such as `smtp.gmail.com` and `587`. |
| **SMTP Username**, **SMTP Password** | The mail account. |
| **SMTP From Address**, **SMTP Sender Name** | Who the mail is from; an empty address uses the username. |
| **Recipients** | Where it goes, comma-separated. |
| **Encryption** | None, STARTTLS or TLS. |
| **Send Test Email** | Sends one, to prove the settings work. |
| **Email Event Notifications** | Which events are mailed — the same list as Telegram's. |

## Subscription

### General

| Setting | What it does |
|---------|--------------|
| **Subscription Service** | Turns customers' subscription links on or off. Off: no link works, and the panel shows none. |
| **Listen IP**, **Listen Domain**, **Listen Port** | Where the subscription service listens. Empty Listen Port keeps it on the panel's own port and address. At restart. |
| **URI Path** | The path links start with, such as `/subscribe/`. |
| **Reverse Proxy URI** | The full base of the link (`https://sub.example.com/path/`) when customers reach the service through a reverse proxy or another domain or port than the ones above. Used for the link and QR code the panel shows. |

### Information

**Encode** returns the plain-text subscription as Base64, which some apps expect. **Update Intervals** tells customers' apps how often to refresh the subscription, in hours.

### Profile

**Subscription Title**, **Support URL**, **Profile URL** and **Announce** are shown in the customer's app. Each can carry the customer's own details: `{{EMAIL}}` (their name), `{{ID}}`, `{{SHORT_ID}}`, `{{SUB_ID}}`, `{{TELEGRAM_ID}}`.

### Certificates

**Certificate file** and **Private key file** for the subscription service when it listens on a port of its own. Empty uses the panel's certificate. At restart.

### Template

The look of the page a customer sees when they open their link in a browser; every customer's page follows the one chosen. **Preview** opens a one-time preview link. See [Subscription page](/panel/subscription).

## Backups

| Setting | What it does |
|---------|--------------|
| **Take a backup every** | Hours between scheduled backups; 0 turns them off. The schedule is read from the newest archive on disk, so a restart does not postpone it. |
| **Keep** | How many archives to keep; older ones are removed after each backup. |

Below: every archive on the server, with **Download**, **Restore** and **Delete**; **Back up now**; and **Upload backup** to restore one from your computer. An archive holds the database, every interface key and every customer credential — treat one like a password file. See [Backup and restore](/operations/backup-restore).

## System

What this panel is: **Version** (and what it was built from, the platform and the uptime), **Storage** (the database driver, and how many interfaces, customers and devices it holds), **Listening on** (where the panel answers now) and **Database** (where it is kept).

## Engine

Whether enforcement is running here: **Data limits** (*active* when the kernel stops a customer at the byte they run out), **Speed limits** (*active* when each limited customer has a queue of their own), and the **Reconciler** — the loop that rebuilds the kernel's state from the database — with how many times it has run, when last, and how much traffic it has counted. The engine's own settings are on the [Engine](/panel/engine) page.

## Logs

The panel's last few hundred lines, from memory, filtered by level — the same as the **Logs** window on the [Overview](/panel/overview#the-log), which can also read the system journal.
