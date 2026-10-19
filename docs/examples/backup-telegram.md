---
description: "Every scheduled backup delivered to your Telegram, so a dead server is an inconvenience and not a loss."
---

# Backups to Telegram

**1.** *Settings → Telegram Bot → General*: switch on **Enable Telegram Bot**, paste the bot token, put your chat id in **Admin Chat ID**. Save. The bot starts within fifteen seconds; press **Send Test Message** to be sure.

**2.** *Settings → Telegram Bot → Backup*: switch on **Automatic backup** and choose **When to send**. Daily, at a quiet hour, is right for most. Save.

**3.** To test now, press **💾 DB backup** in the bot's menu: the archive arrives in the chat.

The archive holds the database, the keys and every customer's credentials, so the chat should be yours alone. Set a backup password under Settings → Backups and the archives are encrypted. Restore from any of them: Overview → **Backup & Restore** → **Choose a file**, or on the server `w-ui backup` → 2. More in [Telegram bot](/panel/telegram#automatic-backup).
