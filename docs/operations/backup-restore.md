---
description: "What a backup holds, where the scheduled ones go, how to restore from the panel or the terminal, and why an archive from either database engine or an older version restores into any install."
---

# Backup and restore

A backup holds the one thing that cannot be regenerated: the interface private keys and every customer's credentials. Lose those, and every customer has to be issued new files. The binary, the packages and the certificate can all be replaced; this cannot. Treat the archive like a password file.

## What is in it

One `.tar.gz`, holding:

- **`wui.db`**: a consistent SQLite snapshot, taken with `VACUUM INTO` rather than as a raw copy that could be caught mid-write. It is only in backups from a SQLite panel. If the snapshot cannot be taken, the live file is **not** archived in its place, since it can lack changes still in the write-ahead log; the dump below carries the database instead. A backup with neither is not written, and the failure is reported.
- **`wui-dump/`**: the whole database as a stream, in parts of at most 8 MiB (`00001.jsonl`, `00002.jsonl`, …). It is in every backup, whichever engine, and on PostgreSQL it is the database. This is the copy that crosses engines and versions: a column a newer panel added is left at its default, and a column it dropped is ignored.
  - **One moment.** Every table is read inside one read-only transaction (REPEATABLE READ on PostgreSQL), so a backup taken while customers are being added or removed never holds a file without its customer. It takes no lock that holds back the panel.
  - **Small and steady on memory.** Rows are written as they are read, a part in memory at a time: measured at 50,000 customers with a file on each of seven protocols (2.4 million rows), the backup held under 5 MiB, against more than a gigabyte for the JSON copy older versions wrote.
  - **Exact.** Every number is written as stored, the largest byte counters included, and every time to the microsecond, which is what both engines keep.
  - **Checked.** Every table ends with its number of rows and the SHA-256 of its rows, and the dump ends with a line saying it is complete. Checking an archive, and every restore before it touches anything, reads it to the last row; a cut, damaged or altered dump is refused.
  - Older backups carry **`wui-export.json`** instead, every table in one JSON document. They still restore.
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
- **From the terminal:** `w-ui backup` → 2, and give the path. Or run `wui backup restore FILE`, then `systemctl restart wui`. Run as root, as the script is, what it writes is given to the panel's own user, so the panel can read the staged restore and download the archive.

**What is checked before, and what happens if it fails:**

- **The archive being restored is never deleted** to keep within the number of backups kept, even when the safety copy taken first would otherwise push it out.
- **A database file in the archive is opened and checked first.** A damaged one, or one from a panel with database changes this one does not know, is refused then, with nothing staged; it is never found by a panel that will not start.
- **A database dump from a newer panel is refused** before anything is loaded. Update this panel first.
- **The load is one transaction**, read back and checked against the backup before it is kept. If it is refused at the start, the files restored with it are put back as they were, and the panel runs on its own data and its own files.
- **The panel's latest writes are kept** whenever its database file is not itself replaced.
- **Uploads** take archives up to 8 GB; a larger file is said to be too large, with the limit.
- **A backup with a switched-off tunnel or node, or a customer allowed no device,** restores exactly. Before 3.3.0 such a backup was refused at the restore.

**How this panel is reached never changes.** Its port, URL path, domain, certificate files, trusted proxies and the subscription service's own listener are kept from this server, whatever the archive says. They used to come back with the archive. An archive from another server, or from this one before its port or path was changed, then put the panel on a port the firewall never opened, under a path nobody had, or, with a certificate path that does not exist here, stopped it from starting at all.

The customers, keys and **accounts** do come from the archive: after a restore, you sign in with the archive's administrator.

By default, this server's own addresses, which the tunnels tell customers to connect to, are kept over the archive's. The usual reason to restore on another machine is that the first one is gone, and its address with it. To clone one machine onto another and keep the archive's addresses, use `--move-addresses` on the command line, or turn off **Keep this server's addresses** in the panel.

## Across engines and versions

| Backup from | Restoring into | What happens |
|---|---|---|
| SQLite | SQLite | The snapshot file is put back: an exact copy. |
| SQLite | PostgreSQL | The dump is loaded into PostgreSQL, with ids kept and sequences moved past them. |
| PostgreSQL | PostgreSQL | The dump is loaded. |
| PostgreSQL | SQLite | The dump is loaded into a fresh SQLite file. |
| An older W-UI | A newer one | The schema is migrated first, then the data lands in it. |
| A newer W-UI | An older one | Refused with a clear message when the dump's format is newer than the panel understands. Update the panel first. |

So the database chosen at install is not final. Install with SQLite, and when the customer count grows, take a backup, run the installer again with `--db postgres`, and restore the backup.

A dump is loaded in **one transaction**. As it is read, every table's count and checksum is checked; then what was written is read back from the database, still inside the transaction, and held to the backup: every table's rows, and every value by its checksum. Only then is it committed. Anything short of that (a damaged file, a value the engine would change without a word) rolls everything back, the panel starts on the data it had, and the dump is kept beside it as `.restore-dump.jsonl.failed`. Measured at 2.4 million rows: under 10 MiB of memory, about a minute and a half into PostgreSQL and under four minutes into SQLite.

To check an archive without restoring it: `wui backup check FILE`. It reads the archive through and prints how many tables and rows its database copy holds, every one matching its checksum.

### Moving between SQLite and PostgreSQL

`w-ui` → PostgreSQL → **1** installs PostgreSQL and makes the panel's database and role; **2** moves the panel's data from SQLite to PostgreSQL, **10** back. A move:

1. takes a backup on the engine the panel is on, and checks it to the last row. A backup that does not check out moves nothing;
2. stages the restore while still on that engine, so this server's own addresses (how the panel and the subscription service are reached) are read from the real data;
3. switches the connection, starts the panel, and waits for it to report the load, however long a large panel takes;
4. if the load fails, or the panel stops, puts the panel back on the engine and the data it had, by itself.

The old engine's data is left where it was, in case of a change of mind. Tunnels stay up throughout: moved on the test server both ways with every WireGuard peer and OpenVPN session kept, and every customer's subscription link answering byte for byte as before.

## Where things are

| Path | What |
|---|---|
| `/var/backups/wui/` | Scheduled backups and the panel's own, with retention. It is outside the data directory, so a restore cannot overwrite them and an uninstall leaves them. |
| `/var/lib/wui/.restore-pending/` | A staged restore, applied and removed at the next start. |
| `/var/lib/wui/.restore-import.json` | A dump waiting to be loaded, for a restore across engines. Removed once loaded; kept as `.failed` if it could not be loaded. |
| `/root/wui-last-copy-*.tar.gz` | The copy an uninstall leaves. |
