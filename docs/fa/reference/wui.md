---
description: "یک باینری ایستا و بدون CGO در /usr/local/bin/wui که رابط کاربری داخلش است. بدون آرگومان خود پنل است؛ با یک زیرفرمان، ابزاری است که اسکریپت و نصب‌کننده از آن استفاده می‌کنند."
---

# باینری `wui`

یک باینری ایستا و بدون CGO در `/usr/local/bin/wui` که رابط کاربری داخل آن است. اگر بدون آرگومان اجرا شود، خود پنل است. با یک زیرفرمان، ابزاری است که اسکریپت و نصب‌کننده از آن استفاده می‌کنند.

```
wui                              run the panel
wui setting show                 print the effective configuration
wui setting show --json          the same, as JSON
wui setting set [flags]          change where the panel answers (applied at the next start)
wui setting reset                forget every panel setting; the admin account and the customers are kept
wui admin reset [flags]          reset the administrator account
wui token issue --name NAME      mint an API token, printed once
wui backup create [--dir DIR]    take a backup, the same archive the panel takes
wui backup list [--dir DIR]      the backups in that directory
wui backup restore FILE          stage a restore, applied at the next start
wui version                      print the version
wui keygen                       make a release-signing key pair
wui sign <binary>                sign a build, for a release
wui verify <binary> <sig>        check a build against the key this panel was built with
```

چهار زیرفرمان دیگر را systemd اجرا می‌کند، نه شما: `apply-update` (با `wui-update.service`، برای نصب به‌روزرسانی‌ای که پنل دانلود کرده)، `l2tp-agent` (با `wui-l2tp.service`، برای اجرای تونل‌های L2TP/IPsec، PPTP، IKEv2 و OpenConnect)، `pptpd` (که همان عامل راهش می‌اندازد)، و `firewall-sync` (با `wui-firewall.service`، برای باز کردن پورت تونل‌ها در ufw یا firewalld).

## `setting set`

```
--listen ADDR      the address to bind (0.0.0.0 for every address)
--port N           the port
--base-path PATH   the secret path the panel is served under
--cert FILE        certificate to serve TLS with
--key FILE         its private key
--no-tls           forget the certificate and serve plain HTTP
--sub-enable       serve customers' subscription links
--sub-disable      stop serving them
--sub-port N       a port of the subscription service's own (0: the panel's)
--sub-listen ADDR  the address it binds
--sub-cert FILE    certificate for that port (default: the panel's)
--sub-key FILE     its private key
```

موقع شروع، مقدارهای صفحه‌ی تنظیمات بر متغیرهای محیطی مقدم‌اند، پس تغییری که باید بماند باید همین‌جا ثبت شود. `setting show` نتیجه‌ی نهایی را در خط‌هایی که اسکریپت می‌خواند چاپ می‌کند: `listen`، `listenIP`، `port`، `basePath`، `scheme`، `cert`، `key`، `subEnabled`، `subPort`، `subPath`، `dbDriver` و `dbSource`، با رمز پوشیده.

## `backup`

`create` همان آرشیوی را می‌نویسد که زمان‌بند پنل می‌نویسد: نسخه‌ی SQLite اگر باشد، فایل‌های سرور، و یک دامپ JSON قابل‌حمل از همه‌ی جدول‌ها؛ پس فایل روی پنلی با هر کدام از دو موتور دیتابیس و روی W-UI جدیدتر بازگردانی می‌شود. اگر رمز بکاپ تنظیم شده باشد، آرشیو رمزنگاری می‌شود.

`restore` آرشیو را کپی و بررسی می‌کند، از داده‌های فعلی یک کپی کنارش نگه می‌دارد و بازگردانی را آماده می‌کند؛ راه‌اندازی بعدی آن را اعمال می‌کند. با `--move-addresses` به جای آدرس‌های همین سرور، آدرس‌های سرورِ داخل آرشیو گرفته می‌شوند.

هر دو به محیط پنل نیاز دارند، که منوی `w-ui` و فایل `/etc/wui/db.env` فراهمش می‌کنند. [بکاپ و بازگردانی](/fa/operations/backup-restore) را ببینید.

## `admin reset`

```
--username NAME    the administrator's name (default: keep the current one)
--password PASS    the new password (default: generate one and print it)
--password-stdin   read the password from standard input
--reset-two-factor forget the two-factor secret, so the account signs in with the password alone
--quiet            print nothing on success
```

## سیگنال‌ها

`SIGHUP` همه‌ی تونل‌ها را بدون متوقف کردن پنل، از روی پیکربندی ذخیره‌شده‌شان دوباره راه می‌اندازد؛ `systemctl reload wui` همین سیگنال را می‌فرستد. `SIGTERM` پنل را متوقف می‌کند و تونل‌ها را روشن می‌گذارد.
