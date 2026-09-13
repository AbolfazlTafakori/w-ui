---
description: "یک دستور W-UI را روی اوبونتو، دبیان، AlmaLinux، Rocky یا Fedora نصب می‌کند؛ سؤال‌هایی که می‌پرسد، هر پرچم، نصب بدون سؤال، و اجرا پشت پروکسی."
---

# نصب

یک دستور، با root، روی یک سرور تازه:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

## چه چیزی لازم دارد

| | |
|---|---|
| سیستم‌عامل | Ubuntu 22.04 / 24.04، Debian 12 / 13، AlmaLinux 9، Rocky 9، Fedora 41 — هر کدام با هر ریلیز به‌صورت تمیز در CI نصب می‌شود |
| معماری | x86-64 یا arm64 |
| کرنل | کرنلی با `nft_quota` — اوبونتو، دبیان و بیشتر کرنل‌های VPS دارند؛ نصاب بررسی می‌کند و می‌گوید |
| پورت‌ها | پورت پنل (TCP)، یک پورت UDP برای هر اینترفیس وایرگارد، TCP 443 اگر OpenVPN می‌خواهی، و پورت 80 در دسترس برای سرتیفیکیت Let's Encrypt |

نصاب `nftables`، `wireguard-tools`، AmneziaWG، OpenVPN، `easy-rsa` و `qrencode` را نصب می‌کند، فورواردینگ IPv4/IPv6 را روشن می‌کند، کاربر بی‌امتیاز `wui` می‌سازد، و یک unit سخت‌شدهٔ systemd ثبت می‌کند.

## سؤال‌ها

اول می‌پرسد، بعد بی‌مراقب کار می‌کند:

۱. **پورت پنل** — رندوم و آزاد، مگر خودت بگویی. پورتی که چیز دیگری روی آن گوش می‌دهد را قبول نمی‌کند.
۲. **مسیر URL** — یک پیشوند ۱۸ حرفی رندوم. هر چیزی خارج از آن، حتی صفحهٔ ورود، 404 می‌دهد.
۳. **مدیر** — نام و رمز هر دو تولید می‌شوند، مگر خودت بدهی.
۴. **چطور به پنل برسیم:**

   ```
   1) Let's Encrypt for Domain (90-day validity, auto-renews)
   2) Let's Encrypt for IP Address (6-day validity, auto-renews)   ← پیش‌فرض
   3) Custom SSL Certificate (path to existing files)
   4) Skip SSL (advanced — behind reverse proxy / SSH tunnel only)
   ```

   هر کدام را انتخاب کنی، پنل روی همان یک پورت جواب می‌دهد — با آی‌پی و با دامنه یکسان. [سرتیفیکیت](/fa/guide/certificates).

۵. اینکه OpenVPN و AmneziaWG هم کنار WireGuard نصب شود یا نه.

اگر روی همه Enter بزنی، هیچ چیزِ نتیجه قابل حدس نیست: پورت رندوم، مسیر رندوم، مدیر رندوم، رمز تولیدشده، سرتیفیکیت برای خودِ آی‌پی سرور.

## نصب بدون سؤال

هر جواب یک پرچم هم هست و `--yes` سؤال‌ها را رد می‌کند:

| پرچم | اثر |
|------|-----|
| `--port N` | روی پورت N گوش بده |
| `--path SEG` / `--no-path` | پیشوند URL، یا هیچ |
| `--username NAME` / `--password PASS` | مدیر |
| `--domain NAME` | Let's Encrypt برای این دامنه |
| `--ip-cert [ADDR]` | سرتیفیکیت ۶ روزهٔ Let's Encrypt برای آی‌پی سرور (خودکار پیدا می‌شود مگر بدهی) |
| `--tls-cert PATH` / `--tls-key PATH` | سرتیفیکیتی که خودت داری |
| `--no-tls` | HTTP ساده — فقط پشت پروکسی یا تونل SSH |
| `--local-only` | فقط روی 127.0.0.1 |
| `--no-amnezia` / `--no-openvpn` | آن پکیج‌ها را نصب نکن |
| `--local PATH` | باینری‌ای که خودت ساخته‌ای |
| `--from-source` | آخرین کامیت را از سورس بساز (Go را اگر نباشد نصب می‌کند) |
| `-y`, `--yes` | هیچ نپرس |

متغیرهای محیطی هم کار می‌کنند: `WUI_DOMAIN`، `WUI_SERVER_IP`، `WUI_SSL_MODE=ip|domain|none`، `WUI_ADMIN_USER`، `WUI_ADMIN_PASSWORD`، `WUI_ENABLE_FAIL2BAN=false`.

```bash
# برای cloud-init
WUI_SSL_MODE=ip bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --port 2053 -y
```

## پشت nginx یا پروکسی دیگر

گزینهٔ ۴ را انتخاب کن و به «bind به 127.0.0.1» **بله** بگو. بعد پروکسی را به `http://127.0.0.1:PORT/` بده و TLS را همان‌جا تمام کن. آدرس پروکسی را در تنظیمات → امنیت → Trusted proxies بگذار تا آدرس مشتری‌ها از `X-Forwarded-For` خوانده شود.

## اجرای دوبارهٔ نصاب

اجرای دوباره روی سروری که پنل دارد یعنی **ارتقا**: پورت، مسیر، مدیر و سرتیفیکیت را نگه می‌دارد، باینری را عوض می‌کند و سرویس را ریستارت می‌کند. مشتری‌های وایرگارد قطع نمی‌شوند — تانل‌ها در کرنل زندگی می‌کنند، نه در پروسهٔ پنل.
