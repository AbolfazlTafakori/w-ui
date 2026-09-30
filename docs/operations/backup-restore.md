---
description: "What a backup holds, where the scheduled ones go, how to restore from the panel or the terminal, and why an archive from either database engine or an older version restores into any install."
---

# Backup and restore

A backup is the one thing that cannot be regenerated: the interface private keys and every customer's credentials. Lose those and every customer has to be reissued. The binary, the packages, the certificate — all replaceable. This is not. Treat the archive like a password file.

## What is in it

One `.tar.gz`, holding:

- **`wui.db`** — a consistent SQLite snapshot (`VACUUM INTO`, not a raw copy that can be caught mid-write). Only in backups from a SQLite panel. If the snapshot cannot be taken, the live file is **not** archived in its place — it can lack changes still in the write-ahead log — and the dump below carries the database. A backup with neither is not written, and the failure is reported.
- **`wui-export.json`** — every table as JSON, written through the schema. In every backup, whichever engine. This is the copy that crosses engines and versions: a column a newer panel added is left at its default, a column it dropped is ignored.
- Everything else in `/var/lib/wui` — OpenVPN's PKI and server files, generated profiles — except the backups themselves, SQLite's write-ahead sidecars, a restore waiting to be applied and an update waiting for the update helper.

Two backups taken in the same second — a manual one beside a scheduled one, or a restore's safety copy beside either — get distinct names (`…-2.tar.gz`) instead of one overwriting the other.

## Taking one

| Where | How |
|---|---|
| The panel | Settings → Backup → **Back up now**, or download any listed archive |
| On a schedule | Settings → Backup: interval and how many to keep; written to `/var/backups/wui` |
| The terminal | `w-ui backup` → 1, or `wui backup create` (with the panel's environment; the menu supplies it) |
| Telegram | automatically, at the time set in Settings → Telegram → Backup ([Telegram bot](/panel/telegram#automatic-backup)); or at once, with the bot's backup button |
| Uninstall | a last copy is written to `/root/wui-last-copy-<date>.tar.gz` before anything is removed |

## Restoring

A restore never unpacks over the live data. The archive is checked end to end, a copy of what is there now is taken first, the files are staged beside the data directory, and the **next start** applies them — the only moment nothing has the database open.

- **From the panel:** Overview → **Backup & Restore** → *Choose a file*, or Settings → Backup → **Restore** on a listed archive. The panel restarts itself, and the page reloads once it is back — not after a guessed number of seconds.

**How this panel is reached never changes.** Its port, URL path, domain, certificate files, trusted proxies and the subscription service's own listener are kept from this server, whatever the archive says. They used to come back with the archive: an archive from another server — or from this one before its port or path was changed — put the panel on a port the firewall never opened, under a path nobody had, or, with a certificate path that does not exist here, stopped it from starting at all. The customers, keys and **accounts** do come from the archive: after a restore you sign in with the archive's administrator.
- **From the terminal:** `w-ui backup` → 2, give the path. Or `wui backup restore FILE`, then `systemctl restart wui`.

By default this server's own addresses — what the tunnels tell customers to connect to — are kept over the archive's. The usual reason to restore on another machine is that the first one is gone, and its address with it. Cloning one machine onto another and wanting the archive's addresses is `--move-addresses` on the command line, or the checkbox in the panel.

## Across engines and versions

| Backup from | Restoring into | What happens |
|---|---|---|
| SQLite | SQLite | the snapshot file is put back — an exact copy |
| SQLite | PostgreSQL | the JSON dump is loaded into PostgreSQL, ids kept, sequences moved past them |
| PostgreSQL | PostgreSQL | the JSON dump is loaded |
| PostgreSQL | SQLite | the JSON dump is loaded into a fresh SQLite file |
| an older W-UI | a newer one | the schema is migrated first, then the data lands in it |
| a newer W-UI | an older one | refused with a clear message when the dump's format is newer than the panel understands; update the panel first |

So the database choice at install is not final: install with SQLite, and when the customer count grows, take a backup, re-run the installer with `--db postgres` and restore it.

## Where things are

| Path | What |
|---|---|
| `/var/backups/wui/` | scheduled backups and the panel's own, with retention; outside the data directory so a restore cannot clobber them and an uninstall leaves them |
| `/var/lib/wui/.restore-pending/` | a staged restore, applied and removed at the next start |
| `/var/lib/wui/.restore-import.json` | a dump waiting to be loaded, for a cross-engine restore; removed once loaded, kept as `.failed` if it could not be |
| `/root/wui-last-copy-*.tar.gz` | the copy an uninstall leaves |
