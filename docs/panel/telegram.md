---
description: "The classic panel bot, for its two audiences."
---

# Telegram bot

The classic panel bot, for its two audiences.

## Setting it up

1. Make a bot with @BotFather and copy its token.
2. Settings → Telegram: enable, paste the token, put your chat id in *Admin Chat ID* (ask @userinfobot, or send `/id` to your bot after saving).
3. Save. The bot starts polling within fifteen seconds; no restart.

## For the operator

Commands: `/start`, `/help`, `/status`, `/id`, `/usage <name>`, `/inbound <name>`, `/restart` (every tunnel), `/clearall` (reset all traffic, with a confirmation).

The keyboard: sorted traffic report · server usage · reset all traffic · DB backup (sent as a file) · sign-in failures · inbounds · running out · commands · online · all customers · add customer · subscription / config files / QR codes.

A customer's card has buttons for: refresh, device log, reset traffic, traffic limit, expiry, device limit, Telegram id, enable / disable, and their links.

## For customers

A customer whose **Telegram id** is on their plan (Clients → edit → Telegram id) can send `/usage` to see what is left, and ask for their subscription link, config files or QR codes. Anyone else gets a polite refusal.

## Automatic backup

Settings → Telegram → **Backup**: switch on **Automatic backup** and choose when — every day at a time, every week on a day at a time, every few hours, or a crontab line. At that time the bot sends the database backup to the admin chat as a file, with the panel's version, the server's name, when it was taken and its size under it. It is the same archive Settings → Backup makes, restored the same way (Overview → **Backup & Restore**).

- Times are in the panel's time zone (Settings → General → Time zone; the server's when unset).
- The schedule outlives a restart or an update: *every 6 hours* is every 6 hours however often the panel restarts. A backup that fell due while the panel was down is sent once, as soon as it is back.
- Switching it on, or choosing another time, starts the count then — nothing is sent at once.
- A backup that cannot be taken, or is larger than the 50 MB Telegram accepts from a bot, is not retried every minute: the chat is told why, and the next goes at the next time.
- Not more often than every 10 minutes: each one is the whole database.
- The archives are also kept on the server, with the others, by *How many to keep* in Settings → Backup.

## Notifications

Independently of the bot: customers depleting or expiring, sharing detected, a backup landing, a sign-in — each one switchable in Settings → Telegram.
