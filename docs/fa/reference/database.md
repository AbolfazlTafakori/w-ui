---
description: "دیتابیس کجاست، چه چیزی داخلش است، چطور بک‌آپ می‌شود و چطور داخلش را ببینی."
---

# دیتابیس

یک فایل SQLite: `/var/lib/wui/wui.db` (با همراهان `-wal` و `-shm` وقتی پنل روشن است). منبع حقیقت است — وضعیت کرنل هر دو ثانیه از آن ساخته می‌شود — پس بک‌آپ این فایل بک‌آپ همه چیز است.

## جدول‌ها

| جدول | نگه می‌دارد |
|------|-------------|
| `admins` | مدیر، هش bcrypt، راز 2FA |
| `settings` | هر تنظیم پنل به شکل `key` / `value` (`panel.*`، `sub.*`، `routing.*`، `engine.*`، `notify.*`) |
| `nodes` | این پنل و آن‌هایی که می‌پاید |
| `interfaces` | تانل‌ها: کلیدها، زیرشبکه، پورت، حالت، پارامترهای AmneziaWG، CA و سرتیفیکیت سرور OpenVPN |
| `clients` | مشتری‌ها: سهمیه، انقضا، سقف دستگاه، وضعیت، توکن سابسکریپشن، شناسهٔ تلگرام |
| `accounts` | یک ردیف به ازای هر دستگاه مشتری: کلید یا credential، آدرس، آخرین handshake |
| `account_endpoints` | هر دستگاه آخرین بار از کجا دیده شده (تشخیص اشتراک‌گذاری) |
| `traffic_samples` | تاریخچهٔ مصرف |
| `ip_leases` | تخصیص آدرس |
| `groups`، `hosts`، `outbounds`، `outbound_subs`، `balancers`، `routing_rules` | آنچه صفحه‌هایشان نشان می‌دهند |
| `api_tokens` | هش توکن‌های صادرشده |

## نگاه به داخل

```bash
apt install sqlite3
sqlite3 /var/lib/wui/wui.db '.tables'
sqlite3 -header /var/lib/wui/wui.db 'select id,name,status,quota_bytes,used_bytes from clients;'
```

آزادانه بخوان. فقط با پنل متوقف بنویس، و فقط اگر می‌دانی چرا — پنل هر چه بگذاری را با کمال میل اعمال می‌کند.

## بک‌آپ

بک‌آپ‌های زمان‌بندی‌شده در `/var/backups/wui/` به شکل `wui-backup-<date>.tar.gz` می‌روند: دیتابیس که خودِ SQLite کپی کرده (`VACUUM INTO`)، دایرکتوری OpenVPN، و کلیدها. تنظیمات → General زمان‌بندی و تعداد نگه‌داری را تعیین می‌کند؛ تلگرام می‌تواند هر کدام را بگیرد.

برگرداندن: نمای کلی → Backup → Restore، یا `w-ui` → 25، یا دستی:

```bash
systemctl stop wui
tar xzf wui-backup-….tar.gz -C /
chown -R wui:wui /var/lib/wui
systemctl start wui
```

## انتقال به سرور دیگر

روی سرور جدید نصب کن، پنل را آن‌جا متوقف کن، بک‌آپ را برگردان، شروع کن. اینترفیس‌ها با همان کلیدها و پورت‌ها بالا می‌آیند، پس کانفیگ مشتری‌ها وقتی DNS یا آدرس endpoint به ماشین جدید اشاره کند کار می‌کند.

## PostgreSQL

`WUI_DB_DRIVER=postgres` و `WUI_DB_SOURCE=<DSN>` پذیرفته می‌شوند؛ SQLite چیزی است که نصاب راه می‌اندازد و در CI تست می‌شود.
