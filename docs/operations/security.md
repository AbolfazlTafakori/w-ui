---
description: "What the panel does to protect itself, and what you should do on top: firewall, two-factor authentication, backups off the box."
---

# Security

## What the panel does for you

- **HTTPS by default.** The installer gets a certificate for your domain or for the server's own address, and renews it unattended.
- **Nothing to find.** A random port and a random URL prefix. Every path outside it, the sign-in page included, answers 404.
- **Nothing to guess.** A generated administrator name and password; there is no `admin` / `admin`.
- **Sign-in throttling** per address and per account, and a fail2ban jail (`w-ui` → 22. Connection limit) that bans an address after repeated failures. It watches the panel's port only, so a customer's tunnel and your SSH are untouched.
- **Two-factor authentication** (TOTP), asked for only after the password was right, so it never reveals which accounts have it.
- **A strict Content-Security-Policy**, with a nonce per request.
- **Sign-ins kept off the customer's page.** L2TP, PPTP, IKEv2 and OpenConnect sign-ins are shown masked, and each is copied on a click, fetched by the page alone, counted and logged. The link's raw text, download and archive carry the mask too. See [Subscription page](/panel/subscription#sign-ins-are-masked) for what that stops, and what no page can.
- **An unprivileged service account.** The panel runs as `wui` with only `CAP_NET_ADMIN` and `CAP_NET_BIND_SERVICE`, under `ProtectSystem=strict`, with one writable directory.
- **Secrets stay put.** Passwords are bcrypt hashes. API tokens are stored as hashes and shown once. Interface and device private keys never appear in anything the panel renders for display, only in the customer's own file.
- **A word before the certificate runs out.** The security warnings at the top of Settings say when the certificate the panel or the subscription service is served with has less than two weeks left, and say it louder once it has passed. Renewal is still the job of the installer and acme.sh; this makes sure an expiry is not something you hear about from a customer.
- **Signed releases.** A panel built with the project's public key refuses an update whose signature does not match.

## Where the secrets are

Every customer's keys, every administrator's password hash and second factor, and the credentials of the panels used as nodes are in the database. Here is what protects them:

- **On the server**, the database is `/var/lib/wui/wui.db`, readable by the panel's own user alone: `0600`, in a `0750` directory owned by that user. Since 3.0 the panel makes the file private when it starts, as earlier releases left it readable by everyone.
- **In a backup**, the archive is encrypted with the backup password when one is set ([Backup and restore](/operations/backup-restore#encryption)). The key sessions are signed with is never in a backup, so a backup cannot be used to sign in to its panel.
- **Not column by column.** Encrypting the second factor and the nodes' credentials inside the database, with a key kept out of the backup, would make them unreadable after a restore on another server, and the owner could not sign in to their own panel. The file's permissions and the backup's encryption protect the same secrets without that failure.

## What you should do

- Limit the firewall to the panel port, the tunnel ports, port 80 for renewals, and SSH. See [Ports and firewall](/operations/ports-firewall).
- Turn on two-factor authentication. The password is the only thing between an attacker and every customer's configuration.
- Set a backup password, and send backups somewhere else: Telegram, or copy `/var/backups/wui` off the box.
- Read `install-result.env` once, then delete it.

## Reporting a vulnerability

See [SECURITY.md](https://github.com/AbolfazlTafakori/w-ui/blob/main/SECURITY.md). Please do not open a public issue for a security problem.
