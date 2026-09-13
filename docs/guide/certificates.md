# Certificates

The panel serves HTTPS itself, on its one port. There is no reverse proxy to configure, and the address and the domain reach it the same way.

## At install time

The installer's fourth question:

| Choice | Validity | Needs | Best for |
|--------|----------|-------|----------|
| **Domain** | 90 days | a name pointing at this server, port 80 reachable | a panel you share by name |
| **IP address** (default) | 6 days, renewed every 6 hours by a timer | port 80 reachable | any server, no domain at all |
| **Custom** | whatever the files say | the paths | certificates you already manage (certbot, a wildcard) |
| **Skip** | — | a proxy or SSH tunnel in front | advanced setups |

Both Let's Encrypt modes use acme.sh and the HTTP-01 challenge on a standalone listener. If something already holds port 80, the installer asks for another port for the listener; forward external 80 to it for the validation.

## Renewal is unattended

Two mechanisms, either of which is enough:

- acme.sh's own cron entry (installed by acme.sh);
- `wui-cert-renew.timer`, a systemd timer that runs `acme.sh --cron` every six hours — because many small images ship without a cron daemon, and because a six-day certificate renewed at day six cannot wait for a daily check.

```bash
systemctl list-timers wui-cert-renew.timer
```

When a certificate is renewed, its files are rewritten in place. **The panel notices by itself** — it re-reads the pair when the files change — so nothing restarts and no tunnel blinks.

## Later, from the menu

`w-ui` → **20. SSL Certificate Management**:

```
1. Get SSL (Domain)
2. Revoke & Remove
3. Force Renew
4. Show Existing Domains
5. Set Cert paths for the panel
6. Get SSL for IP Address (6-day cert, auto-renews)
```

and **21. Cloudflare SSL Certificate** for a wildcard over the DNS challenge with an API token.

Certificates live under `/etc/wui/certs/<name>/fullchain.pem` and `privkey.pem`, readable by the panel's own account.

## The subscription service on its own port

Settings → Subscription can put the subscription service on a listener of its own, with its own certificate. The link handed to customers then carries that port and scheme, so `https://your.host:2096/sub/TOKEN` works the way 3x-ui's does.

## Behind your own proxy

Choose **Skip** at install (or `--no-tls --local-only`), then terminate TLS in nginx or Caddy and proxy to `http://127.0.0.1:PORT/`. Add the proxy's address to Settings → Security → Trusted proxies so client addresses are read from `X-Forwarded-For`.
