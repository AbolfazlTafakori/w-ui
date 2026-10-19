---
description: "نصاب کارش را با صفحه‌ای تمام می‌کند که هر چه لازم دارید رویش هست. پیش از بستن ترمینال، آن را جای امنی ذخیره کنید."
---

# بعد از نصب

نصاب کارش را با صفحه‌ای تمام می‌کند که هر چه لازم دارید رویش هست. پیش از بستن ترمینال، آن را جای امنی ذخیره کنید.

```
  ═══════════════════════════════════════════
       Panel Installation Complete!
  ═══════════════════════════════════════════
  Username:    kQz8mR2pLw
  Password:    Hn4vT9xB2cWq7sYe
  Port:        41234
  WebBasePath: aBcDeFgHiJkLmNoPqR
  Database:    SQLite (/var/lib/wui/wui.db)
  Access URL:  https://203.0.113.9:41234/aBcDeFgHiJkLmNoPqR/
  API Token:   wui_Qm3…
  ═══════════════════════════════════════════
  ⚠ IMPORTANT: Save these credentials securely!
  ⚠ SSL Certificate: Enabled and configured
  Install result written to /etc/wui/install-result.env (mode 600).
```

## هر خط چیست

| خط | چیست | بعداً کجا عوض می‌شود |
|----|------|----------------------|
| Username / Password | حساب مدیر پنل | `w-ui` ← 7، یا تنظیمات ← احرازهویت |
| Port | همان یک پورتی که پنل با آدرس IP و با دامنه روی آن جواب می‌دهد | `w-ui` ← 10 |
| WebBasePath | مسیر مخفی؛ هیچ آدرسی بیرون از آن جواب نمی‌دهد | `w-ui` ← 8 |
| Access URL | آدرسی که در مرورگر باز می‌کنید | |
| API Token | توکن Bearer برای اتوماسیون، که فقط یک بار نشان داده می‌شود | تنظیمات ← احرازهویت ← توکن API، یا `wui token issue` |

## فایل `/etc/wui/install-result.env`

همین مقادیر به شکلی که اسکریپت می‌تواند بخواند، و فقط برای root قابل خواندن (mode 600). اسکریپت cloud-init یا بنر ورود به سرور می‌تواند آن‌ها را از این‌جا بردارد:

```bash
WUI_USERNAME=kQz8mR2pLw
WUI_PASSWORD=Hn4vT9xB2cWq7sYe
WUI_PANEL_PORT=41234
WUI_WEB_BASE_PATH=aBcDeFgHiJkLmNoPqR
WUI_ACCESS_URL=https://203.0.113.9:41234/aBcDeFgHiJkLmNoPqR/
WUI_API_TOKEN=wui_Qm3…
WUI_DB_TYPE=sqlite
```

وقتی مقادیر را جای امن‌تری گذاشتید، فایل را پاک کنید. بعد از نصب، هیچ برنامه‌ای آن را نمی‌خواند.

به‌روزرسانی این فایل را دست نمی‌زند و توکن API تازه‌ای نمی‌سازد؛ فرقی نمی‌کند نصاب را دوباره اجرا کنید، `w-ui update` بزنید یا دکمهٔ **Update** خود پنل را. فقط نصب این فایل را می‌نویسد و فقط نصب توکن `installer` داخل آن را صادر می‌کند.

## کارهای دیگر نصاب

- **برنامهٔ fail2ban** نصب می‌شود، با یک jail که لاگ ورود پنل را زیر نظر دارد. مدیریتش از `w-ui` ← 22 است؛ برای نصب نکردنش هنگام نصب `WUI_ENABLE_FAIL2BAN=false` را بدهید.
- **سرتیفیکیت** صادر و تمدیدش زمان‌بندی می‌شود؛ هم با cron خودِ acme.sh و هم با تایمر systemd که هر شش ساعت اجرا می‌شود. پنل سرتیفیکیت تمدیدشده را خودش به کار می‌برد و چیزی ری‌استارت نمی‌شود.
- **دستور `w-ui`** نصب می‌شود. نصاب در پایان، زیردستورهای اصلی آن را چاپ می‌کند:

```
┌───────────────────────────────────────────────────────┐
│  w-ui control menu usages (subcommands):              │
│                                                       │
│  w-ui             - Admin Management Script           │
│  w-ui start       - Start                             │
│  w-ui stop        - Stop                              │
│  w-ui restart     - Restart                           │
│  w-ui status      - Current Status                    │
│  w-ui settings    - Current Settings                  │
│  w-ui enable      - Enable Autostart on OS Startup    │
│  w-ui disable     - Disable Autostart on OS Startup   │
│  w-ui log         - Check logs                        │
│  w-ui banlog      - Check Fail2ban ban logs           │
│  w-ui update      - Update                            │
│  w-ui legacy      - Legacy version                    │
│  w-ui install     - Install                           │
│  w-ui uninstall   - Uninstall                         │
└───────────────────────────────────────────────────────┘
```

فهرست کامل، از جمله `restart-tunnels` و `backup` و `postgres`، در [راهنمای دستور w-ui](/fa/reference/w-ui) آمده است.

## رمز را فراموش کرده‌اید؟

منو را باز کنید و گزینهٔ 7، *Reset username and password*، را انتخاب کنید:

```bash
w-ui
```

یا مستقیم ریستش کنید. این دستور یک رمز تازه می‌سازد و فقط یک بار نشانش می‌دهد:

```bash
wui admin reset --username admin
```

## قدم بعد

[اولین تونل، اولین مشتری](/fa/guide/first-steps).
