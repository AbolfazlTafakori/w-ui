---
description: "Update in place with w-ui update, go back to an earlier version, uninstall with a last copy of the data kept, and where backups go."
---

# Updating and uninstalling

## Update

```bash
w-ui update
```

Or open `w-ui` and choose **2. Update**. It fetches the latest release, checks its checksum and signature, replaces the binary and the `w-ui` script, and restarts the panel.

**It asks nothing.** The port, the URL path, the certificate, the database and the administrator stay as the install set them. The one exception is a panel with no certificate at all, which is offered one. Settings, customers and keys are kept. WireGuard customers stay connected, because the tunnels are kernel objects and the panel restarts only an interface whose configuration actually changed.

`w-ui update-dev`, menu item 3, installs the rolling `dev-latest` build of the latest commit instead, to try a fix before it is released.

The same update without the menu, on any machine:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/update.sh)
```

Every release is signed. A panel built with the project's public key refuses an update whose signature does not match, and each release page also carries `SHA256SUMS`.

## Going back to an earlier version

`w-ui legacy`, menu item 5 (*Install an older version*), installs the version you name.

Going back from 3.0 to a release before it takes down first what 3.0 runs beside the panel: the L2TP agent, with any L2TP, IKEv2, PPTP or OpenConnect tunnel it carries, and the firewall helper. An older release has neither, and their units run the panel's binary with a command an older binary does not know. Customers on WireGuard and OpenVPN are unaffected.

## Uninstall

```bash
w-ui uninstall
```

Or open `w-ui` and choose **6. Uninstall**. Before anything is deleted, a copy of the database and every key is written to `/root/wui-last-copy-<date>.tar.gz` (mode 600), so an uninstall typed on the wrong server is a mistake you can undo, not a disaster.

WireGuard, OpenVPN, strongSwan, xl2tpd and nftables stay installed, since other things on the server may use them. Tunnels that were up stay up until the next reboot.

## Backups

The panel takes its own backups into `/var/backups/wui` and keeps a number of them. Set how often and how many from the overview: **Backup & Restore → All backups →**, then *Take a backup every* and *Keep*. Settings → Telegram Bot → **Backup** sends backups to Telegram on a schedule of its own.

From the shell, `w-ui backup` makes or restores one. An archive is a consistent snapshot: the database is never read as a file while it is being written. See [Backup and restore](/operations/backup-restore).
