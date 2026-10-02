---
description: "The Telegram bot: the operator manages customers and the server from the chat, customers ask for their usage and links, and the panel sends notices and backups."
---

# Telegram bot

The bot serves two audiences: **you**, who can look after customers and the server from the chat, and **your customers**, who can ask it for their usage and their links. It also sends the panel's notices and, if you want, its backups.

## Setting it up

1. Make a bot with @BotFather and copy its token.
2. *Settings → Telegram Bot*: switch on **Enable Telegram Bot**, paste the token into **Telegram Token**, and put your Telegram user ID in **Admin Chat ID** (ask @userinfobot, or send `/id` to your bot once it runs). Several admins: comma-separated.
3. **Save**. The bot starts within 15 seconds; no restart. **Send Test Message** proves it reaches you.

Where api.telegram.org is blocked, set **Telegram API Server** to a server that relays it, or send the panel's own traffic out through a working hop with *Settings → General → Panel Outbound*.

## For the operator

**Commands:**

| Command | What it does |
|---------|--------------|
| `/start`, `/help` | The menu, and what the bot can do. |
| `/status` | Says whether the bot is up. |
| `/id` | Your Telegram user ID. |
| `/usage <name>` | A customer's card. |
| `/inbound <name>` | An interface: protocol and port, how many customers are on it and online, its traffic up and down. |
| `/restart` | Reopens every tunnel. |
| `/clearall` | Resets every customer's traffic, after a confirmation. |

**The menu:** 📊 Sorted traffic report · 🖥 Server usage · ♻️ Reset all traffic (asks first) · 💾 DB backup (the archive, as a file) · 🚫 Sign-in failures · 📥 Interfaces · ⏳ Running out · 📖 Commands · 🟢 Online · 👥 All customers · ➕ Add customer · 🔗 Subscription / 📄 Config files / 🔲 QR codes.

**A customer's card** shows their plan and usage, with buttons: 🔄 Refresh · 📋 Device log · ♻️ Reset traffic · 📶 Traffic limit · 📅 Expiry · 📱 Users · 👤 Telegram id · 🔁 Enable / disable · and their links. Numbers are picked from a row of common values, or typed with ✏️ Custom. Changes go through the same checks as the panel's.

## For customers

A customer whose **Telegram ID** is set on their plan (*Clients → ✎ → Telegram ID*) can use the bot themselves: 📊 My usage shows what is used and left and when the plan ends; 🔗 Subscription, 📄 Config files and 🔲 QR codes send their link and files; `/usage <name>` does the same as My usage. Anyone else gets a polite refusal. They can learn their ID with `/id`.

## Notices

*Settings → Telegram Bot → Notifications* chooses which events reach the admin chats — customers running out, expiring or ended, a shared credential, an outbound or node going down or coming back, the panel starting or degrading, a backup taken, high CPU or memory, sign-ins — and how often the periodic report is sent. The full list is on [Settings](/panel/settings#notifications-1). The same notices can go by [email](/panel/settings#email).

## Automatic backup

*Settings → Telegram Bot → Backup*: switch on **Automatic backup** and choose **When to send** — every day at a time, every week on a day at a time, every few hours, or a crontab line. At that time the bot sends the backup archive to the admin chats as a file, with the panel's version, the server's name, when it was taken and its size. It is the same archive *Backup & Restore* makes, and is restored the same way (Overview → **Backup & Restore**).

- Times are in the panel's time zone (*Settings → General → Date and Time*; the server's when unset).
- The schedule survives a restart or an update: *every 6 hours* is every 6 hours however often the panel restarts. A backup that fell due while the panel was down is sent once, as soon as it is back.
- Switching it on, or choosing another time, starts the count then — nothing is sent at once.
- A backup that cannot be taken, or is larger than the 50 MB Telegram accepts from a bot, is not retried every minute: the chat is told why, and the next one goes at the next time.
- Not more often than every 10 minutes: each one is the whole database.
- The archives are also kept on the server with the others, up to *Settings → Backups → Keep*.
