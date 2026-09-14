---
description: "Everything is an environment variable : there is no config file to keep in sync. The installer writes them into the systemd unit; /etc/wui/wui.env is read after it and overrides it."
---

# Configuration

Everything is an environment variable — there is no config file to keep in sync. The installer writes them into the systemd unit; `/etc/wui/wui.env` is read after it and overrides it.

| Variable | Default | Meaning |
|----------|---------|---------|
| `WUI_LISTEN` | `127.0.0.1:2096` | address to serve on |
| `WUI_BASE_PATH` | `/` | the secret URL prefix |
| `WUI_TLS_CERT` / `WUI_TLS_KEY` | — | serve HTTPS with this pair |
| `WUI_TRUSTED_PROXIES` | — | CIDRs allowed to set `X-Forwarded-*` |
| `WUI_DATA_DIR` | `./data` | database and state |
| `WUI_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `WUI_DB_SOURCE` | `<data dir>/wui.db` | file path or DSN; the installer keeps both database variables in `/etc/wui/db.env` (root only, read by the unit and the script) |
| `WUI_BACKUP_DIR` | `<data dir>/backups` | where scheduled backups go; the installer points it at `/var/backups/wui` |
| `WUI_COLLECT_INTERVAL` | `2s` | how often usage is read (minimum `1s`) |
| `WUI_DEFAULT_LOCALE` | `en` | `en` or `fa` |
| `WUI_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WUI_LOG_FORMAT` | `text` | `text` or `json` |
| `WUI_DEBUG` | `false` | verbose diagnostics |

## Precedence

1. What the settings page saved (`wui setting set`, or Settings → General) — listen, port, path, certificate, trusted proxies, time zone;
2. `/etc/wui/wui.env`;
3. the unit's `Environment=` lines;
4. the defaults above.

So a port changed in the panel sticks across reinstalls, and the environment is only what a fresh install runs on.

## Paths

| | |
|---|---|
| `/usr/local/bin/wui` | the panel |
| `/usr/local/bin/w-ui` | the menu |
| `/etc/systemd/system/wui.service` | the unit |
| `/etc/wui/` | `wui.env`, `certs/`, `install-result.env` |
| `/var/lib/wui/` | `wui.db`, `openvpn/`, `geoip/`, `hops/`, `history.gob` |
| `/var/backups/wui/` | scheduled backups |
| `/var/log/wui/` | fail2ban's ban log |
| `/root/.acme.sh/` | acme.sh and its renewal state |
