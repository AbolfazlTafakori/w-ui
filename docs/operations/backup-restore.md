---
description: "What a backup holds, where the scheduled ones go, how to restore from the panel or the terminal, and why an archive from either database engine or an older version restores into any install."
---

# Backup and restore

A backup holds the one thing that cannot be regenerated: the interface private keys and every customer's credentials. Lose those, and every customer has to be issued new files. The binary, the packages and the certificate can all be replaced; this cannot. Treat the archive like a password file.

## What is in it

One `.tar.gz`, holding:

- **`wui.db`**: a consistent SQLite snapshot, taken with `VACUUM INTO` rather than as a raw copy that could be caught mid-write. It is only in backups from a SQLite panel. If the snapshot cannot be taken, the live file is **not** archived in its place, since it can lack changes still in the write-ahead log; the dump below carries the database instead. A backup with neither is not written, and the failure is reported.
- **`wui-export.json`**: every table as JSON, written through the schema. It is in every backup, whichever engine. This is the copy that crosses engines and versions: a column a newer panel added is left at its default, and a column it dropped is ignored.
- Everything else in `/var/lib/wui`, such as OpenVPN's PKI and server files and the generated profiles. Left out are the backups themselves, SQLite's write-ahead sidecars, a restore waiting to be applied, an update waiting for the update helper, and `keys/`.

**Nothing in a backup opens the panel it came from.** The key sessions are signed with, and the backup password, live in `/var/lib/wui/keys/`, which is never archived and never restored over. Before 3.0 the session key was in the database, so a backup could be used to sign in to its panel as its owner. 3.0 moves the key out and makes a new one, so every backup taken before carries a key that opens nothing. Everyone signs in once more after the update.

## Encryption

Set a **Backup password** under Settings → Backups, and every archive from then on is encrypted with it. It is named `….tar.gz.enc`, cannot be read without the password, and is refused whole if it was cut short or changed. The password is kept in `/var/lib/wui/keys/backup.password`: not in the database, and not in any backup. Archives taken before stay as they were.

- **Write the password down somewhere else.** An encrypted archive restores only where the same password is set. On a new server, set the password first, then upload the archive.
- Without a password, archives are plain `.tar.gz`. If the Telegram bot sends them, the panel's security warnings say so, and so does every archive's caption in the chat, until a password is set.
- The format is AES-256-GCM in 64 KiB chunks, each bound to its place and to whether it is the last. The key is scrypt (N=2¹⁵, r=8, p=1) of the password and a random salt per archive.

Two backups taken in the same second, such as a manual one beside a scheduled one, or a restore's safety copy beside either, get distinct names (`…-2.tar.gz`) instead of one overwriting the other.

## Taking one

| Where | How |
|---|---|
| The panel | Overview → **Backup & Restore** → **Download backup**; or Settings → Backups → **Back up now**, or download any listed archive. |
| On a schedule | Settings → Backups: **Take a backup every** and **Keep**. Written to `/var/backups/wui`. |
| The terminal | `w-ui backup` → 1, or `wui backup create`, with the panel's environment, which the menu supplies. |
| Telegram | Automatically, at the time set in Settings → Telegram Bot → Backup ([Telegram bot](/panel/telegram#automatic-backup)); or at once, with the bot's backup button. |
| Uninstall | A last copy is written to `/root/wui-last-copy-<date>.tar.gz` before anything is removed. |

## Restoring

A restore never unpacks over the live data. The archive is checked end to end, a copy of what is there now is taken first, and the files are staged beside the data directory. The **next start** applies them, since that is the only moment nothing has the database open.

- **From the panel:** Overview → **Backup & Restore** → **Choose a file**; or Settings → Backups → **Restore** on a listed archive, or **Upload an archive** from another server. The panel restarts itself, and the page reloads once the panel is back, not after a guessed number of seconds.
- **From the terminal:** `w-ui backup` → 2, and give the path. Or run `wui backup restore FILE`, then `systemctl restart wui`.

**How this panel is reached never changes.** Its port, URL path, domain, certificate files, trusted proxies and the subscription service's own listener are kept from this server, whatever the archive says. They used to come back with the archive. An archive from another server, or from this one before its port or path was changed, then put the panel on a port the firewall never opened, under a path nobody had, or, with a certificate path that does not exist here, stopped it from starting at all.

The customers, keys and **accounts** do come from the archive: after a restore, you sign in with the archive's administrator.

By default, this server's own addresses, which the tunnels tell customers to connect to, are kept over the archive's. The usual reason to restore on another machine is that the first one is gone, and its address with it. To clone one machine onto another and keep the archive's addresses, use `--move-addresses` on the command line, or turn off **Keep this server's addresses** in the panel.

## Across engines and versions

| Backup from | Restoring into | What happens |
|---|---|---|
| SQLite | SQLite | The snapshot file is put back: an exact copy. |
| SQLite | PostgreSQL | The JSON dump is loaded into PostgreSQL, with ids kept and sequences moved past them. |
| PostgreSQL | PostgreSQL | The JSON dump is loaded. |
| PostgreSQL | SQLite | The JSON dump is loaded into a fresh SQLite file. |
| An older W-UI | A newer one | The schema is migrated first, then the data lands in it. |
| A newer W-UI | An older one | Refused with a clear message when the dump's format is newer than the panel understands. Update the panel first. |

So the database chosen at install is not final. Install with SQLite, and when the customer count grows, take a backup, run the installer again with `--db postgres`, and restore the backup.

## Where things are

| Path | What |
|---|---|
| `/var/backups/wui/` | Scheduled backups and the panel's own, with retention. It is outside the data directory, so a restore cannot overwrite them and an uninstall leaves them. |
| `/var/lib/wui/.restore-pending/` | A staged restore, applied and removed at the next start. |
| `/var/lib/wui/.restore-import.json` | A dump waiting to be loaded, for a restore across engines. Removed once loaded; kept as `.failed` if it could not be loaded. |
| `/root/wui-last-copy-*.tar.gz` | The copy an uninstall leaves. |
