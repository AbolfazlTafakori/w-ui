---
description: "The Telegram bot, and the same bot on Bale: the operator manages customers and the server from the chat, customers ask for their usage and links, and the panel sends notices and backups."
---

# Telegram bot

The bot serves two audiences: **you**, who can look after customers and the server from the chat, and **your customers**, who can ask it for their usage and their links. It also sends the panel's notices and, if you want, its backups.

## Setting it up

1. Make a bot with @BotFather and copy its token.
1. Go to *Settings → Telegram Bot*. Switch on **Enable Telegram Bot**, paste the token into **Telegram Token**, and put your Telegram user ID in **Admin Chat ID**. To find your ID, ask @userinfobot, or send `/id` to your bot once it runs. For several admins, separate the IDs with commas.
1. Press **Save**. The bot starts within 15 seconds, with no restart. **Send Test Message** proves it reaches you.

Where api.telegram.org is blocked, set **Telegram API Server** to a server that relays it, or send the panel's own traffic out through a working hop with *Settings → General → Panel Outbound*.

## For the operator

**Commands:**

| Command | What it does |
|---------|--------------|
| `/start`, `/help` | The menu, and what the bot can do. |
| `/status` | Says whether the bot is up. |
| `/id` | Your Telegram user ID. |
| `/usage <name>` | A customer's card. |
| `/inbound <name>` | An interface: its protocol and port, how many customers are on it and online, and its traffic up and down. |
| `/restart` | Reopens every tunnel. |
| `/clearall` | Resets every customer's traffic, after a confirmation. |

**The menu:** 📊 Sorted traffic report · 🖥 Server usage · ♻️ Reset all traffic (asks first) · 💾 DB backup (the archive, as a file) · 🚫 Sign-in failures · 📥 Interfaces · ⏳ Running out · 📖 Commands · 🟢 Online · 👥 All customers · ➕ Add customer · 🔗 Subscription / 📄 Config files / 🔲 QR codes.

**A customer's card** shows their plan and usage, with buttons: 🔄 Refresh · 📋 Device log · ♻️ Reset traffic · 📶 Traffic limit · 📅 Expiry · 📱 Users · 👤 Telegram id · 🔁 Enable / disable · and their links. Numbers are picked from a row of common values, or typed with ✏️ Custom. Changes go through the same checks as the panel's.

## For customers

A customer whose **Telegram ID** is set on their plan (*Clients → ✎ → Telegram ID*) can use the bot themselves. 📊 My usage shows what is used and left, and when the plan ends. 🔗 Subscription, 📄 Config files and 🔲 QR codes send their link and files. `/usage <name>` does the same as My usage. Anyone else gets a polite refusal. A customer can learn their ID with `/id`.

## Notices

*Settings → Telegram Bot → Notifications* chooses which events reach the admin chats, and how often the periodic report is sent. The events cover customers running out, expiring or ended; a shared credential; an outbound or node going down or coming back; the panel starting or degrading; a backup taken; high CPU or memory; and sign-ins. The full list is on [Settings](/panel/settings#notifications-1). The same notices can go by [email](/panel/settings#email).

## Automatic backup

Go to *Settings → Telegram Bot → Backup*, switch on **Automatic backup** and choose **When to send**: every day at a time, every week on a day at a time, every few hours, or a crontab line. At that time the bot sends the backup archive to the admin chats as a file, with the panel's version, the server's name, when it was taken and its size. It is the same archive *Backup & Restore* makes, and it is restored the same way, from Overview → **Backup & Restore**. If a backup password is set, the archive is encrypted with it.

- Times are in the panel's time zone, set under *Settings → General → Date and Time*; the server's zone is used when it is unset.
- The schedule survives a restart or an update: *every 6 hours* is every 6 hours however often the panel restarts. A backup that fell due while the panel was down is sent once, as soon as the panel is back.
- Switching it on, or choosing another time, starts the count from then. Nothing is sent at once.
- A backup that cannot be taken, or is larger than the 50 MB Telegram accepts from a bot, is not retried every minute. The chat is told why, and the next one goes at the next time.
- Backups are not sent more often than every 10 minutes, since each one is the whole database.
- The archives are also kept on the server with the others, up to the number set in *Settings → Backups → Keep*.

## The Bale bot

Bale is the Iranian messenger whose bot API is Telegram's, with small differences. The panel runs the same bot there, beside the Telegram one: the same menu, commands and customer card for administrators, the same answers for customers, the same notifications, periodic report and automatic backup. Use either, or both.

1. In Bale, open @BotFather, make a bot and copy its token.
1. Go to *Settings → Bale Bot*. Switch on **Enable Bale Bot**, paste the token into **Bale Token**, and put your Bale user ID in **Admin Chat ID**. To learn your ID, send `/id` to your bot once it runs. For several admins, separate the IDs with commas.
1. Press **Save**, then **Send Test Message**. The bot starts within 15 seconds, with no restart.

What is its own:

- **Its settings.** Its token, admin chats, language, which events it reports, the report's time and the automatic backup are set on its own tab, apart from Telegram's. The thresholds behind the CPU, memory and outbound events are the panel's, and shared.
- **Its customers.** A customer uses the Bale bot through the **Bale ID** on their plan (*Clients → ✎ → Bale ID*), apart from the Telegram ID. The bot's 👤 button sets the Bale ID.
- **Its messages.** Bale has no HTML formatting, so the bot writes Bale's own Markdown; the content is the same.
- **Its automatic backup** goes on its own schedule. The 50 MB limit is the same as Telegram's.

Bale is reached from inside Iran with no relay, which makes it the bot to rely on where Telegram is filtered.
