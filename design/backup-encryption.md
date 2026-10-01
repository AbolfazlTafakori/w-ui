# Design: encrypted backups

Status: proposed. Not implemented.

## The problem

A backup is the panel: `wui.db` and the portable dump hold the JWT secret
that signs sessions, the Telegram bot token, the mail password, every
interface's private key, the OpenVPN authority, and every customer's keys
and logins. Archives sit in `/var/backups/wui` (mode 0600, in a 0750
directory the panel's user owns), are downloaded through the panel, and -- with the automatic
backup -- are sent to a Telegram chat, all unencrypted. Anyone who can read
the chat can impersonate the server to every customer and sign in as the
owner.

## Design

### The format

An encrypted backup is the same `.tar.gz` archive, encrypted with
[age](https://age-encryption.org) under a passphrase, and named
`wui-backup-<time>.tar.gz.age`:

- **age**, not a format of our own: a small, reviewed Go library
  (`filippo.io/age`), streaming (a backup is never held in memory whole),
  authenticated, and with a command-line tool -- an operator can decrypt an
  archive on any machine with `age -d` and no panel at all.
- **A passphrase** (age's scrypt recipient), not a key file: it is what an
  operator can keep in a password manager and type on a new server.
- Inside is byte for byte today's archive, so everything after decryption --
  checks, staging, the cross-engine import, the kept panel address -- is
  today's code.

### Settings

- **Settings → Backup → Encryption passphrase**: write-only, shown as set or
  not, like the bot token. Stored as a setting (`backup.passphrase`) and kept
  across a restore onto the same server like the panel's address (it is not
  inside the archive it encrypts: the archive holds the database as it was
  *before* the passphrase is applied to it -- see below).
- **Encrypt backups kept on this server** (off by default; on once a
  passphrase is set, for new installs).
- **Encrypt the Telegram backup** (on whenever a passphrase is set; the
  Telegram page says plainly when it is not).

The passphrase is checked for length (at least 12 characters) and confirmed
by typing it twice. The page says, in so many words, that a lost passphrase
is a lost backup.

### What the archive holds of the passphrase

The settings table inside the archive carries `backup.passphrase`. It is
removed from the copy that goes into the archive (the snapshot is taken, the
key deleted from the copy, then archived), so an archive never holds the
passphrase that opens it, and a restore keeps the receiving panel's own --
in the list beside the panel address (`backup.panelAccessSettings`).

### Restore, and every old archive

- An archive is recognised by its first bytes: gzip (`1f 8b`) is an
  unencrypted archive from any release, restored exactly as today; the age
  header (`age-encryption.org/v1`) asks for the passphrase.
- **The panel**: uploading or restoring an encrypted archive opens a
  passphrase field; a wrong passphrase says so and changes nothing.
- **The terminal**: `wui backup restore FILE --passphrase-stdin`, and the
  `w-ui` menu asks for it.
- **Every archive from v1.0.0 on keeps restoring unchanged**: the upgrade
  fixtures' backups are the test.

### Telegram

With a passphrase set, the automatic backup and the bot's backup button send
the `.age` file; the caption says it is encrypted and how to restore it.
Without one, they send the archive as today, and the Telegram page shows a
warning beside the switch.

## Tests

- Round trip: back up with a passphrase, restore with it; the same data, the
  same customers' configurations (the upgrade test's comparison).
- A wrong passphrase, a truncated `.age` file, and an `.age` file that is not
  ours: each refused with its own message, nothing staged.
- Every earlier release's unencrypted backup (the fixtures) still restores.
- The archive holds no `backup.passphrase`; a restore keeps the receiving
  panel's.
- The `.age` file decrypts with the age library's own decrypter (what `age -d`
  runs), outside the panel's code.
- The Telegram backup is the `.age` file when a passphrase is set, and the
  archive otherwise.
- Mutation checks on each of the above.

## Docs

`docs/operations/backup-restore.md` (the format, the passphrase, decrypting
by hand with `age -d`), the Telegram page, `docs/reference/errors.md` for the
new messages, the CHANGELOG -- English and Persian.
