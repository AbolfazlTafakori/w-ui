---
description: "همه چیز متغیر محیطی است : فایل کانفیگی نیست که هماهنگ نگه داری. نصاب آن‌ها را در unit سیستم‌دی می‌نویسد؛ /etc/wui/wui.env بعد از آن خوانده می‌شود و روی آن می‌نشیند."
---

# پیکربندی

همه چیز متغیر محیطی است — فایل کانفیگی نیست که هماهنگ نگه داری. نصاب آن‌ها را در unit سیستم‌دی می‌نویسد؛ `/etc/wui/wui.env` بعد از آن خوانده می‌شود و روی آن می‌نشیند.

| متغیر | پیش‌فرض | معنی |
|-------|---------|------|
| `WUI_LISTEN` | `127.0.0.1:2096` | آدرس سرو |
| `WUI_BASE_PATH` | `/` | پیشوند مخفی URL |
| `WUI_TLS_CERT` / `WUI_TLS_KEY` | — | HTTPS با این جفت |
| `WUI_TRUSTED_PROXIES` | — | CIDRهایی که اجازهٔ `X-Forwarded-*` دارند |
| `WUI_DATA_DIR` | `./data` | دیتابیس و state |
| `WUI_DB_DRIVER` | `sqlite` | `sqlite` یا `postgres` |
| `WUI_DB_SOURCE` | `<data dir>/wui.db` | مسیر فایل یا DSN؛ نصاب هر دو متغیر دیتابیس را در `/etc/wui/db.env` نگه می‌دارد (فقط root، یونیت و اسکریپت می‌خوانند) |
| `WUI_BACKUP_DIR` | `<data dir>/backups` | بک‌آپ‌های زمان‌بندی‌شده؛ نصاب به `/var/backups/wui` می‌بردش |
| `WUI_COLLECT_INTERVAL` | `2s` | هر چند وقت مصرف خوانده شود (حداقل `1s`) |
| `WUI_DEFAULT_LOCALE` | `en` | `en` یا `fa` |
| `WUI_LOG_LEVEL` | `info` | `debug`، `info`، `warn`، `error` |
| `WUI_LOG_FORMAT` | `text` | `text` یا `json` |
| `WUI_DEBUG` | `false` | تشخیص پرحرف |

## اولویت

۱. آنچه صفحهٔ تنظیمات ذخیره کرده (`wui setting set`، یا تنظیمات → عمومی) — listen، پورت، مسیر، سرتیفیکیت، trusted proxies، منطقهٔ زمانی؛
۲. `/etc/wui/wui.env`؛
۳. خط‌های `Environment=` در unit؛
۴. پیش‌فرض‌های بالا.

پس پورتی که در پنل عوض شده در نصب‌های مجدد می‌ماند و env فقط چیزی است که نصب تازه با آن اجرا می‌شود.

## مسیرها

| | |
|---|---|
| `/usr/local/bin/wui` | پنل |
| `/usr/local/bin/w-ui` | منو |
| `/etc/systemd/system/wui.service` | unit |
| `/etc/wui/` | `wui.env`، `certs/`، `install-result.env` |
| `/var/lib/wui/` | `wui.db`، `openvpn/`، `geoip/`، `hops/`، `history.gob` |
| `/var/backups/wui/` | بک‌آپ‌های زمان‌بندی‌شده |
| `/var/log/wui/` | لاگ بن fail2ban |
| `/root/.acme.sh/` | acme.sh و state تمدیدش |
