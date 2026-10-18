---
description: "The panel serves HTTPS itself, on its one port. There is no reverse proxy to configure, and the address and the domain reach it the same way."
---

# Certificates

The panel serves HTTPS itself, on its one port. There is no reverse proxy to configure, and the address and the domain reach it the same way.

## At install time

The installer's certificate question offers four choices:

| Choice | Validity | Needs | Suits |
|--------|----------|-------|-------|
| **Domain** | 90 days | A name pointing at this server, and port 80 reachable | A panel you share by name |
| **IP address** (default) | 6 days, renewed by a timer every 6 hours | Port 80 reachable | Any server, with no domain at all |
| **Custom** | Whatever the files say | The paths to the files | Certificates you already manage, from certbot or a wildcard |
| **Skip** | | A proxy or SSH tunnel in front of the panel | Advanced setups |

Both Let's Encrypt modes use acme.sh with the HTTP-01 challenge on a listener of its own. If something already holds port 80, the manual install asks for another port for that listener, and you forward external port 80 to it for the validation. The automatic install, and any install with no terminal to ask on, skips the certificate instead and leaves port 80 to whatever holds it.

## Renewal needs nothing from you

Two mechanisms renew the certificate, and either is enough on its own:

- acme.sh's own cron entry, which acme.sh installs;
- `wui-cert-renew.timer`, a systemd timer that runs `acme.sh --cron` every six hours. It exists because many small images ship without a cron daemon, and because a six-day certificate cannot wait for a daily check.

```bash
systemctl list-timers wui-cert-renew.timer
```

A renewal rewrites the certificate's files in place. **The panel notices by itself:** it reads the pair again when the files change, so nothing restarts and no tunnel drops.

## Later, from the menu

`w-ui` → **20. SSL certificate**:

```
1. Get SSL (Domain)
2. Revoke & Remove
3. Force Renew
4. Show Existing Domains
5. Set Cert paths for the panel
6. Get SSL for IP Address (6-day cert, auto-renews)
```

`w-ui` → **21. Cloudflare SSL certificate** gets a wildcard certificate over the DNS challenge, with a Cloudflare API token.

Certificates live in `/etc/wui/certs/<name>/` as `fullchain.pem` and `privkey.pem`, readable by the panel's own account.

## The subscription service on its own port

Settings → Subscription can put the subscription service on a listener of its own, with its own certificate. The link customers receive then carries that port and scheme, so `https://your.host:2096/subscribe/TOKEN` works the way customers expect.

## Behind your own proxy

Choose **Skip** at install time, or pass `--no-tls --local-only`. Terminate TLS in nginx or Caddy and proxy to `http://127.0.0.1:PORT/`. Then add the proxy's address under Settings → General → **Trusted proxy CIDRs**, so the panel reads customers' addresses from `X-Forwarded-For`.
