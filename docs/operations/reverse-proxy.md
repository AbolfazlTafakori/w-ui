---
description: "Put nginx or Caddy in front of the panel : when to, and the exact server blocks that work, including the subscription path."
---

# Reverse proxy

The panel does not need one: it serves HTTPS itself on its port, for the address and the domain alike. Put a proxy in front only when you already run one for other sites and want the panel on 443 beside them.

## Install for it

Choose **4 — Skip SSL** at install and answer **yes** to binding to 127.0.0.1, or:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --no-tls --local-only --port 2053 -y
```

Then tell the panel which address may set forwarded headers, so sign-in throttling and "last seen from" use the customer's address rather than the proxy's:

```bash
wui setting set --listen 127.0.0.1 --port 2053
# Settings → Security → Trusted proxies: 127.0.0.1/32
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

`certbot --nginx -d panel.example.com` fills in the TLS half and renews it.

If the panel itself already serves HTTPS (the default install), proxy to it as HTTPS instead:

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

Caddy issues and renews the certificate itself.

## The subscription path

Customers' subscription links carry the panel's public address. Behind a proxy, set Settings → Subscription → **Reverse proxy URI** to `https://panel.example.com` so the links point at the proxy, or give the subscription service its own port and proxy that too.

## Keep the panel's own port closed

With a proxy in front, nothing outside should reach the panel directly: bind to 127.0.0.1 (above) and do not open its port in the firewall. `w-ui` → 23.
