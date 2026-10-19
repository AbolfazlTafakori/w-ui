---
description: "Everything is an environment variable, with no config file to keep in sync. The installer writes them into the systemd unit; /etc/wui/wui.env is read after it and overrides it."
---

# Configuration

Everything is an environment variable; there is no config file to keep in sync. The installer writes them into the systemd unit, and `/etc/wui/wui.env` is read after it and overrides it.

| Variable | Default | Meaning |
|----------|---------|---------|
| `WUI_LISTEN` | `127.0.0.1:2096` | The address to serve on. |
| `WUI_BASE_PATH` | `/` | The secret URL prefix. |
| `WUI_TLS_CERT` / `WUI_TLS_KEY` | | Serve HTTPS with this pair. |
| `WUI_TRUSTED_PROXIES` | | CIDRs allowed to set `X-Forwarded-*`. |
| `WUI_DATA_DIR` | `./data` | The database and state. |
| `WUI_DB_DRIVER` | `sqlite` | `sqlite` or `postgres`. |
| `WUI_DB_SOURCE` | `<data dir>/wui.db` | A file path or a DSN. The installer keeps both database variables in `/etc/wui/db.env`, readable by root only and read by the unit and the script. |
| `WUI_BACKUP_DIR` | `<data dir>/backups` | Where scheduled backups go. The installer points it at `/var/backups/wui`. |
| `WUI_COLLECT_INTERVAL` | `2s` | How often usage is read; at least `1s`. |
| `WUI_DEFAULT_LOCALE` | `en` | `en` or `fa`. |
| `WUI_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `WUI_LOG_FORMAT` | `text` | `text` or `json`. |
| `WUI_DEBUG` | `false` | Verbose diagnostics. |

## Precedence

1. What the settings page saved, through `wui setting set` or Settings → General: listen address, port, path, certificate, trusted proxies and time zone.
1. `/etc/wui/wui.env`.
1. The unit's `Environment=` lines.
1. The defaults above.

So a port changed in the panel sticks across reinstalls, and the environment is only what a fresh install runs on.

## Paths

| Path | What |
|---|---|
| `/usr/local/bin/wui` | The panel. |
| `/usr/local/bin/w-ui` | The menu. |
| `/etc/systemd/system/wui.service` | The unit. `wui-l2tp`, `wui-firewall` and `wui-update` are the root helpers beside it. |
| `/etc/wui/` | `wui.env`, `db.env`, `certs/`, `install-result.env`. |
| `/var/lib/wui/` | `wui.db`, `keys/`, `openvpn/`, `l2tp/`, `geoip/`, `hops/`, `history.gob`. |
| `/var/lib/wui/keys/` | The session-signing key and the backup password. Never archived, never restored over. |
| `/var/backups/wui/` | Scheduled backups. |
| `/var/log/wui/` | fail2ban's ban log. |
| `/root/.acme.sh/` | acme.sh and its renewal state. |
