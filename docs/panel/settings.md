---
description: "The panel's own settings: where it listens and how, the account and two-factor, the Telegram bot, mail, the subscription service, and, reached from the overview, backups, the engine's state, the log and the system report."
---

# Settings

The sidebar has five settings tabs, **General**, **Authentication**, **Telegram Bot**, **Email** and **Subscription**, each with inner tabs. Four more sections are reached from elsewhere:

- **Backups**, from Overview → Backup & Restore → *All backups →*;
- **System**, from Overview → *System*;
- **Engine** and **Logs**, by their address: `/settings/engine` and `/settings/logs`.

## Saving and restarting

The top of every tab has **Save**, enabled once something has changed, and **Restart Panel**, enabled when nothing is unsaved; it asks first. Changes are kept once you press Save, and most take effect at once. These take effect at the **next restart** of the panel:

- where the panel listens: Listen IP, Listen Domain, Listen Port and URI Path;
- the panel's certificate and key;
- the trusted proxies;
- the time zone;
- where the subscription service listens, and its certificate.

Before changing the port or the path, write down the new address. After the restart, the panel answers only there.

**Security warnings** above the tabs name anything that leaves the panel exposed: plain HTTP, a short or default path, two-factor off, the username still `admin`, or the installer's password never changed. The account warnings are about the owner's account, each said once; operators without a second factor are named together in one line. *The panel is reachable from any address* means it listens on every interface (`0.0.0.0`), as the installer sets it up. That matters only if the panel sits behind a proxy or tunnel and should be bound to `127.0.0.1`.

## General

### General

| Setting | What it does | Limits |
|---------|--------------|--------|
| **Listen IP** | The address the panel listens on. Empty listens on all of them. | At restart. |
| **Listen Domain** | The domain the panel answers to. Empty answers on every domain and IP. | At restart. |
| **Listen Port** | The panel's port. Empty keeps the one it runs on. | 1 to 65535, and unused. At restart. |
| **URI Path** | The secret path in the panel's address, such as `/AbCd…/`. | Starts and ends with `/`. At restart. |
| **Session Duration** | How long a sign-in lasts, in minutes. | |
| **Trusted proxy CIDRs** | The addresses allowed to tell the panel the real client address, host and scheme, through the `X-Forwarded-*` headers. Only for a reverse proxy in front of the panel. | Comma-separated. At restart. |
| **Panel Outbound** | The [outbound](/panel/outbounds) the panel's own traffic leaves through: its checks, the Telegram bot, and talking to nodes. | Must exist. |
| **Pagination Size** | Rows per page in the tables. | `0` shows everything. |
| **Language** | The language new sessions start in. | |

### Notifications

| Setting | What it does |
|---------|--------------|
| **Expiration Date Notification** | How many days before a customer's end date they count as expiring soon, for the notices. |
| **Traffic Cap Notification** | How many GB before the end of a customer's allowance they count as running out, for the notices. |

### Certificates

**Public Key Path** and **Private Key Path** are the certificate and key files the panel serves HTTPS with, as full paths starting with `/`. The installer fills them in. Empty serves plain HTTP. Applies at restart.

### External Traffic

**External traffic report** posts every customer's new traffic, at each collection, to the **Report address** (http or https) as JSON, `[{"email","up","down"}]`, so a billing system is told without having to ask the panel.

### Date and Time

**Time Zone** is the zone scheduled jobs run in: backups, reports, and renewals counted by the day. Applies at restart. **Calendar Type** is Gregorian or Jalali, for the dates on the pages.

### Customer defaults

What the client form starts with for a new customer: **Default traffic allowance** (GB, `0` for unlimited), **Default duration** (days, `0` for no end date), **Default users**, **Default speed limit** (Mbit/s, `0` for unlimited) and **Default traffic reset** (never, daily, weekly or monthly).

## Authentication

### Admin credentials

To change your username or password, fill in **Current Username**, **Current Password**, **New Username** and **New Password**, then press **Confirm**. You are signed out, and sign in again with the new ones.

### Two-factor authentication

**Enable 2FA** opens a window. Scan the QR code with any authenticator app, or copy the key beside it, then type the code the app shows. From then on, signing in asks for the code as well. Turning it off asks for a code, or your password.

**Panel allowed to manage this one** is for a panel that is a [node](/panel/nodes). Paste the managing panel's authority here, and that panel must then present a client certificate as well as its token, so a token read out of a log or a backup is no longer enough. Empty accepts the token alone.

### API Token

A token lets a program, such as a bot, a script or a managing panel, use the [API](/reference/api) without a password. **New token** asks for a **Name** and shows the token **once**. Copy it then: it is stored only as a hash. The list shows each token's name, when it was made and when it was last used. **Delete** revokes it at once.

## Telegram Bot

### General

| Setting | What it does |
|---------|--------------|
| **Enable Telegram Bot** | Turns the bot on. |
| **Telegram Token** | The bot's token from @BotFather. A saved token shows as configured; leave it as it is to keep it. |
| **Admin Chat ID** | The Telegram user IDs of the administrators, comma-separated. Get yours from @userinfobot, or send `/id` to the bot. |
| **Telegram Bot Language** | The language the bot speaks. |
| **Telegram API Server** | Another Telegram API server, for where api.telegram.org is blocked. Empty uses the default. |
| **Send Test Message** | Sends one message to every admin chat, to prove the settings work. |

### Notifications

Under **Telegram Event Notifications**, tick what the bot tells the admins about:

| Event | When |
|-------|------|
| **Out of traffic**, **Expired** | A customer is cut off. |
| **Expiring soon** | A customer is within the *Expiration Date Notification* days of their end date. |
| **Shared credential** | A file is on the [Sharing](/panel/sharing) report. |
| Outbound **Down** / **Up** | An outbound stops answering, after the number of failed checks given beside it, or comes back. |
| **Started or degraded** | The panel started, or part of it stopped working. |
| **Backup taken** | A scheduled backup was made. |
| Node **Down** / **Up** | A [node](/panel/nodes) stops or starts answering. |
| **CPU high (%)**, **Memory high (%)** | The server goes over the percentage beside it. |
| **Login attempt** | Someone signed in, or failed to. |

**Notification Time** is how often the periodic report is sent: hourly, daily, weekly, monthly, every *N* minutes or hours, or a crontab expression.

### Backup

**Automatic backup** sends the backup archive to the admin chats by itself, at **When to send**, in the panel's time zone. A backup that falls due while the panel is off is sent as soon as the panel is back. It is the same archive *Backup & Restore* makes, and it restores the same way. See [Telegram bot](/panel/telegram).

## Email

The same notices by mail, for an operator who does not use Telegram.

| Setting | What it does |
|---------|--------------|
| **Enable Email Notifications** | Turns mail on. |
| **SMTP Host**, **SMTP Port** | The mail server, such as `smtp.gmail.com` and `587`. |
| **SMTP Username**, **SMTP Password** | The mail account. |
| **SMTP From Address**, **SMTP Sender Name** | Who the mail is from. An empty address uses the username. |
| **Recipients** | Where it goes, comma-separated. |
| **Encryption** | None, STARTTLS or TLS. |
| **Send Test Email** | Sends one, to prove the settings work. |
| **Email Event Notifications** | Which events are mailed; the same list as Telegram's. |

## Subscription

### General

| Setting | What it does |
|---------|--------------|
| **Subscription Service** | Turns customers' subscription links on or off. When it is off, no link works and the panel shows none. |
| **Listen IP**, **Listen Domain**, **Listen Port** | Where the subscription service listens. An empty Listen Port keeps it on the panel's own port and address. At restart. |
| **URI Path** | The path links start with, such as `/subscribe/`. |
| **Reverse Proxy URI** | The full base of the link, such as `https://sub.example.com/path/`, when customers reach the service through a reverse proxy, or on another domain or port than the ones above. It is used for the link and the QR code the panel shows. |

### Information

**Encode** returns the plain-text subscription as Base64, which some apps expect. **Update Intervals** tells customers' apps how often, in hours, to refresh the subscription.

### Profile

**Subscription Title**, **Support URL**, **Profile URL** and **Announce** are shown in the customer's app. Each can carry the customer's own details: `{{EMAIL}}` (their name), `{{ID}}`, `{{SHORT_ID}}`, `{{SUB_ID}}` and `{{TELEGRAM_ID}}`.

### Certificates

**Certificate file** and **Private key file** for the subscription service, when it listens on a port of its own. Empty uses the panel's certificate. At restart.

### Template

The look of the page a customer sees when they open their link in a browser. Every customer's page follows the template chosen. **Preview** opens a one-time preview link. See [Subscription page](/panel/subscription).

## Backups

| Setting | What it does |
|---------|--------------|
| **Take a backup every** | Hours between scheduled backups; `0` turns them off. The schedule is read from the newest archive on disk, so a restart does not postpone it. |
| **Keep** | How many archives to keep. Older ones are removed after each backup. |
| **Backup password** | Every archive taken from then on is encrypted with it, the ones sent to Telegram included. At least 10 characters. **Stop encrypting** removes it. |

Below the settings is every archive on the server, marked *encrypted* where it is, with **Download**, **Restore** and **Delete**; **Back up now**; and **Upload an archive**, to restore one from your computer. An archive holds the database, every interface key and every customer credential, so treat one like a password file. See [Backup and restore](/operations/backup-restore).

::: warning Write the backup password down somewhere else
The password is kept on this server and never inside a backup. An encrypted archive is restored only on a panel that has the same backup password set, so a lost password means an archive that cannot be restored.
:::

## System

What this panel is: **Version**, with what it was built from, the platform and the uptime; **Storage**, the database driver and how many interfaces, customers and devices it holds; **Listening on**, where the panel answers now; and **Database**, where it is kept.

## Engine

Whether enforcement is running here. **Data limits** is *active* when the kernel stops a customer at the byte they run out. **Speed limits** is *active* when each limited customer has a queue of their own. The **Reconciler** is the loop that rebuilds the kernel's state from the database; it shows how many times it has run, when it last ran, and how much traffic it has counted. The engine's own settings are on the [Engine](/panel/engine) page.

## Logs

The panel's last few hundred lines, from memory, filtered by level. It is the same as the **Logs** window on the [Overview](/panel/overview#the-log), which can also read the system journal.
