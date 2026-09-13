# Updating and uninstalling

## Update

```bash
w-ui update
```

or `w-ui` → **2. Update**. It fetches the latest release, verifies it, replaces the binary and restarts the panel. Settings, customers and keys are kept. WireGuard customers stay connected: the tunnels are kernel objects, and the panel only restarts an interface whose configuration actually changed.

`w-ui update-dev` (menu 3) builds the latest commit on `main` from source instead — for trying a fix before it is released.

`w-ui legacy` (menu 5) installs a named earlier version.

Every release is signed. A panel built with the project's public key refuses an update whose signature does not match; the release page also carries `SHA256SUMS`.

## Uninstall

```bash
w-ui uninstall
```

or `w-ui` → **6. Uninstall**. Before anything is deleted, a copy of the database and every key is written to `/root/wui-last-copy-<date>.tar.gz` (mode 600) — a mistyped uninstall on the wrong server is a mistake, not a disaster.

WireGuard, OpenVPN and nftables are left installed; other things on the server may be using them. Tunnels that were up stay up until the next reboot.

## Backups

The panel takes its own backups on a schedule (Settings → General) into `/var/backups/wui`, keeps a number of them, and can send each one to Telegram. `w-ui` → **25. Backup & Restore** makes or restores one from the shell. An archive is a consistent snapshot — the database is copied by SQLite itself, never read as a file that is being written.
