---
description: "The same bot 3x-ui runs, for the same two audiences."
---

# Telegram bot

The same bot 3x-ui runs, for the same two audiences.

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

## Notifications

Independently of the bot: customers depleting or expiring, sharing detected, a backup landing, a sign-in — each one switchable in Settings → Telegram.
