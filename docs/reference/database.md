---
description: "Where the database lives, SQLite or PostgreSQL, what is in it, how it is backed up, and how to look inside it."
---

# Database

The database is the source of truth: the kernel state is rebuilt from it every two seconds. It is one of two engines:

- **SQLite**, the default: one file, `/var/lib/wui/wui.db`, with its `-wal` and `-shm` companions while the panel runs. Readable by the panel's own user alone.
- **PostgreSQL**: chosen at install with `--db postgres`, or later from `w-ui` → 25. The connection is in `/etc/wui/db.env`. CI installs and tests both.

Either way, a [backup](/operations/backup-restore) carries everything, and restores into a panel on either engine.

## Tables

| Table | Holds |
|-------|-------|
| `admins` | The owner, the panel administrators and the resellers: a bcrypt hash, the second factor, and a reseller's ceiling. |
| `admin_interfaces` | Which tunnels each reseller may sell. |
| `settings` | Every panel setting as `key` / `value` (`panel.*`, `sub.*`, `routing.*`, `engine.*`, `notify.*`). |
| `nodes` | This panel and the ones it manages. |
| `interfaces` | Tunnels: keys, subnet, port, mode, AmneziaWG parameters, OpenVPN's authority and server certificate, each protocol's own settings. |
| `clients` | Customers: quota, expiry, users (`device_limit`), renewal, status, subscription token, Telegram id. |
| `accounts` | One row per customer device: its key or sign-in, its address, its last handshake. |
| `account_endpoints` | Where each device was last seen from, for sharing detection. |
| `sharing_events` | The sharing history: each time a device was used from several places or held off, kept after the customer is deleted. |
| `traffic_samples` | Usage history. |
| `ip_leases` | Address allocation. |
| `groups`, `client_groups` | Groups, and who is in each. |
| `hosts`, `outbounds`, `outbound_subs`, `balancers`, `routing_rules` | What their pages show. |
| `api_tokens` | Hashes of issued tokens. |

The key sessions are signed with is not in the database; it is in `/var/lib/wui/keys/`.

## Looking inside

```bash
apt install sqlite3
sqlite3 /var/lib/wui/wui.db '.tables'
sqlite3 -header /var/lib/wui/wui.db 'select id,name,status,quota_bytes,used_bytes from clients;'
```

Read freely. Write only with the panel stopped, and only if you know why: the panel will enforce whatever you put there.

## Backups

Scheduled backups go to `/var/backups/wui/`. Settings → Backups sets how often and how many to keep, and the Telegram bot can receive each one. Restore through the panel or the menu, never by unpacking the archive by hand: Overview → **Backup & Restore** → **Choose a file**, or `w-ui backup` → 2. See [Backup and restore](/operations/backup-restore).

## Moving to another server

Install on the new server and restore a backup there. Interfaces come up with the same keys and ports, so customers' configurations keep working once DNS or the endpoint address points at the new machine. See [Move the panel to another server](/examples/move-server).
