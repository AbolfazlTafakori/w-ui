---
description: "گذاشتن nginx یا Caddy جلوی پنل: چه وقت لازم است، و بلوک‌های دقیقی که کار می‌کنند، از جمله مسیر سابسکریپشن."
---

# پراکسی معکوس

پنل به پراکسی معکوس نیازی ندارد. خودش روی پورتش HTTPS ارائه می‌دهد، چه برای آدرس IP و چه برای دامنه. فقط وقتی پراکسی جلوی پنل بگذارید که از قبل برای سایت‌های دیگر یکی دارید و می‌خواهید پنل هم کنار آنها روی 443 باشد.

## نصب برای این حالت

موقع نصب، گزینه‌ی **4. Skip SSL** را انتخاب کنید و به بستن پنل روی 127.0.0.1 جواب **yes** بدهید. یا بدون سؤال نصب کنید:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --no-tls --local-only --port 2053 -y
```

بعد به پنل بگویید کدام آدرس اجازه دارد هدرهای forwarded را بگذارد، تا محدودسازی ورود و «آخرین بار از» آدرس مشتری را ببینند، نه آدرس پراکسی را:

```bash
wui setting set --listen 127.0.0.1 --port 2053
# تنظیمات ← عمومی ← CIDRهای پراکسی مورد اعتماد: 127.0.0.1/32
```

## nginx

```nginx [/etc/nginx/sites-available/panel]
server {
    server_name panel.example.com;

    location / {
        proxy_pass         http://127.0.0.1:2053;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 300s;
        proxy_buffering    off;
    }
    client_max_body_size 16m;

    listen 443 ssl;
    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;
}
```

دستور `certbot --nginx -d panel.example.com` بخش TLS را پر می‌کند و تمدیدش را هم انجام می‌دهد.

`proxy_buffering off` و زمان خواندن طولانی مهم‌اند: صفحه‌ی سابسکریپشن مشتری و خود پنل یک stream باز نگه می‌دارند و پراکسی‌ای که بافر می‌کند آن را نگه می‌دارد.

اگر خود پنل از قبل HTTPS ارائه می‌دهد، که نصب پیش‌فرض همین‌طور است، به جای آن با HTTPS به پنل وصل شوید:

```nginx
proxy_pass https://127.0.0.1:2053;
proxy_ssl_server_name on;
```

## Caddy

```caddyfile [Caddyfile]
panel.example.com {
    reverse_proxy 127.0.0.1:2053
}
```

برنامه‌ی Caddy خودش گواهی می‌گیرد و تمدیدش می‌کند.

## مسیر سابسکریپشن

لینک اشتراک مشتری‌ها آدرس عمومی پنل را دارد. پشت پراکسی، در تنظیمات ← سابسکریپشن، **پروکسی معکوس URI مسیر** را روی `https://panel.example.com/subscribe/` بگذارید تا لینک‌ها به پراکسی اشاره کنند؛ یا به سرویس سابسکریپشن پورت جداگانه بدهید و آن را هم پشت پراکسی ببرید.

## پورت خود پنل را بسته نگه دارید

وقتی پراکسی جلوی پنل است، هیچ چیز بیرونی نباید مستقیم به پنل برسد. پنل را، همان‌طور که بالا آمد، به 127.0.0.1 ببندید و پورتش را در فایروال باز نکنید (`w-ui` ← 23).
