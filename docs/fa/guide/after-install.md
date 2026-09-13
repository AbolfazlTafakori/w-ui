# بعد از نصب

نصاب مثل نصاب سنایی تمام می‌شود: هر چیزی که لازم داری روی یک صفحه. قبل از بستن ترمینال جایی امن کپی کن.

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

## هر خط از کجا می‌آید

| خط | چیست | بعداً کجا عوض می‌شود |
|----|------|----------------------|
| Username / Password | حساب مدیر | `w-ui` → ۷، یا تنظیمات → امنیت |
| Port | همان یک پورتی که پنل با آی‌پی و دامنه روی آن جواب می‌دهد | `w-ui` → ۱۰ |
| WebBasePath | مسیر مخفی؛ خارج از آن هیچ چیز جواب نمی‌دهد | `w-ui` → ۸ |
| Access URL | همین را در مرورگر بزن | — |
| API Token | توکن Bearer برای اتوماسیون، یک بار نشان داده می‌شود | نودها → توکن‌ها، یا `wui token issue` |

## `/etc/wui/install-result.env`

همان اطلاعات به شکل ماشین‌خوان، فقط برای root (mode 600)، تا اسکریپت cloud-init یا بنر ورود بردارد:

```bash
WUI_USERNAME=kQz8mR2pLw
WUI_PASSWORD=Hn4vT9xB2cWq7sYe
WUI_PANEL_PORT=41234
WUI_WEB_BASE_PATH=aBcDeFgHiJkLmNoPqR
WUI_ACCESS_URL=https://203.0.113.9:41234/aBcDeFgHiJkLmNoPqR/
WUI_API_TOKEN=wui_Qm3…
WUI_DB_TYPE=sqlite
```

وقتی مقادیر را جای امن‌تری گذاشتی پاکش کن؛ بعد از نصب چیزی آن را نمی‌خواند.

## نصاب دیگر چه کرد

- **fail2ban** نصب شد و یک jail گرفت که لاگ ورود پنل را می‌پاید (`w-ui` → ۲۲ برای مدیریت؛ `WUI_ENABLE_FAIL2BAN=false` برای رد کردن).
- **سرتیفیکیت** صادر و تمدیدش زمان‌بندی شد — هم cron خودِ acme.sh، هم یک تایمر systemd هر شش ساعت. پنل سرتیفیکیت تمدیدشده را خودش برمی‌دارد؛ چیزی ریستارت نمی‌شود.
- **دستور `w-ui`** نصب شد و آخرِ کار زیردستورهایش را چاپ می‌کند:

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

## رمز را گم کردی؟

```bash
w-ui            # → 7. Reset Username & Password
```

یا مستقیم:

```bash
wui admin reset --username admin
```

که یک رمز تولیدشدهٔ جدید را یک بار چاپ می‌کند.

## بعدی

[اولین تانل، اولین مشتری](/fa/guide/first-steps).
