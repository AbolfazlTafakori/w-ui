---
description: "nginx یا Caddy را جلوی پنل بگذار : کِی، و بلوک‌های دقیقی که کار می‌کنند، از جمله مسیر سابسکریپشن."
---

# Reverse proxy

پنل به آن نیاز ندارد: خودش HTTPS را روی پورتش سرو می‌کند، برای آی‌پی و دامنه یکسان. فقط وقتی پروکسی جلویش بگذار که از قبل برای سایت‌های دیگر یکی داری و پنل را کنارشان روی 443 می‌خواهی.

## نصب برای آن

موقع نصب **4 — Skip SSL** را انتخاب کن و به bind روی 127.0.0.1 **بله** بگو، یا:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --no-tls --local-only --port 2053 -y
```

بعد به پنل بگو کدام آدرس اجازه دارد هدر forwarded بگذارد، تا محدودسازی ورود و «آخرین بار از» آدرس مشتری را ببینند نه آدرس پروکسی را:

```bash
wui setting set --listen 127.0.0.1 --port 2053
# تنظیمات → Security → Trusted proxies: 127.0.0.1/32
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

`certbot --nginx -d panel.example.com` نیمهٔ TLS را پر می‌کند و تمدید می‌کند.

اگر خودِ پنل از قبل HTTPS سرو می‌کند (نصب پیش‌فرض)، به جای آن با HTTPS پروکسی کن:

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

Caddy خودش سرتیفیکیت می‌گیرد و تمدید می‌کند.

## مسیر سابسکریپشن

لینک سابسکریپشن مشتری‌ها آدرس عمومی پنل را دارد. پشت پروکسی، تنظیمات → سابسکریپشن → **Reverse proxy URI** را روی `https://panel.example.com` بگذار تا لینک‌ها به پروکسی اشاره کنند، یا به سرویس سابسکریپشن پورت جدا بده و آن را هم پروکسی کن.

## پورت خودِ پنل را بسته نگه دار

با پروکسی جلو، هیچ چیز بیرونی نباید مستقیم به پنل برسد: به 127.0.0.1 bind کن (بالا) و پورتش را در فایروال باز نکن. `w-ui` → 23.
