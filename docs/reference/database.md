---
description: "Where the database lives, what is in it, how it is backed up, and how to look inside it."
---

# Database

One SQLite file: `/var/lib/wui/wui.db` (with its `-wal` and `-shm` companions while the panel runs). It is the source of truth — the kernel state is rebuilt from it every two seconds — so a backup of this file is a backup of everything.

## Tables

| Table | Holds |
|-------|-------|
| `admins` | the administrator, a bcrypt hash, the 2FA secret |
| `settings` | every panel setting as `key` / `value` (`panel.*`, `sub.*`, `routing.*`, `engine.*`, `notify.*`) |
| `nodes` | this panel and the ones it watches |
| `interfaces` | tunnels: keys, subnet, port, mode, AmneziaWG parameters, OpenVPN CA and server certificate |
| `clients` | customers: quota, expiry, device limit, status, subscription token, Telegram id |
| `accounts` | one row per customer device: key or credential, address, last handshake |
| `account_endpoints` | where each device was last seen from (sharing detection) |
| `traffic_samples` | usage history |
| `ip_leases` | address allocation |
| `groups`, `hosts`, `outbounds`, `outbound_subs`, `balancers`, `routing_rules` | what their pages show |
| `api_tokens` | hashes of issued tokens |

## Looking inside

```bash
apt install sqlite3
sqlite3 /var/lib/wui/wui.db '.tables'
sqlite3 -header /var/lib/wui/wui.db 'select id,name,status,quota_bytes,used_bytes from clients;'
```

Read freely. Write only with the panel stopped, and only if you know why — the panel will happily enforce whatever you put there.

## Backups

Scheduled backups go to `/var/backups/wui/` as `wui-backup-<date>.tar.gz`: the database copied by SQLite itself (`VACUUM INTO`), the OpenVPN directory, and the keys. Settings → General sets the schedule and how many to keep; Telegram can receive each one.

Restore: Overview → Backup → Restore, or `w-ui` → 25, or by hand:

```bash
systemctl stop wui
tar xzf wui-backup-….tar.gz -C /
chown -R wui:wui /var/lib/wui
systemctl start wui
```

## Moving to another server

Install on the new server, stop the panel there, restore a backup, start. Interfaces come up with the same keys and ports, so customers' configs keep working once DNS or the endpoint address points at the new machine.

## PostgreSQL

`WUI_DB_DRIVER=postgres` and `WUI_DB_SOURCE=<DSN>` are accepted; SQLite is what the installer sets up and what is tested in CI.
