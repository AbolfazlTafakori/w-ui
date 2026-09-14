---
description: "Five tabs, in the classic order. Every change is saved with the button at the top; the ones that concern where the panel listens take effect at the next restart, and the page says so."
---

# Settings

Five tabs, in the classic order. Every change is saved with the button at the top; the ones that concern where the panel listens take effect at the next restart, and the page says so.

## General

| Setting | |
|---------|--|
| Panel listen / port / URL path | where the panel answers — changing the port or path moves the panel at the next restart; note the new address before you restart |
| Panel domain, certificate, key | what the panel serves TLS with; the installer fills these |
| Session length | how long a sign-in lasts |
| Page size | rows per page in the tables |
| Time zone, language | for dates and the interface |
| Defaults for new customers | quota, days, device limit, reset cycle |
| Backups | schedule, how many to keep, whether to send each to Telegram |
| Trusted proxies | CIDRs allowed to set `X-Forwarded-*` |
| Collection interval, online window | how often usage is read, how long since a handshake counts as online |

## Security

Username and password, **two-factor authentication** (TOTP — scan with any authenticator, confirm a code, keep the recovery key), sign-in notifications to Telegram, and the security warnings the panel raises (plain HTTP, a default path, a weak session length).

## Telegram

Enable the bot, the token from @BotFather, the admin chat id(s), the bot's language, and which events notify: customers depleting, expiring, sharing detected, backups, sign-ins, a daily report. The interactive bot is covered on its own page: [Telegram bot](/panel/telegram).

## Email

SMTP host, port, encryption, sender, and the same notifications by mail for an operator who does not use Telegram.

## Subscription

Enable the service, its listen address, port and certificate (a listener of its own, or the panel's), the public domain and path, the reverse-proxy URI when it sits behind one, whether text subscriptions are base64-encoded, the update interval clients are told, the profile title, support and profile URLs, an announcement, and the **template** — six looks for the customer page, with a one-time preview link. See [Subscription page](/panel/subscription).
