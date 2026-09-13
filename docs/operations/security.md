---
description: "What the panel does to protect itself and what you should do on top: firewall, two-factor authentication, backups off the box."
---

# Security

## What the panel does for you

- **HTTPS by default.** The installer gets a certificate for your domain or for the server's own address and renews it unattended.
- **Nothing to find.** A random port and a random URL prefix; every path outside it, the sign-in page included, answers 404.
- **Nothing to guess.** A generated administrator name and password; no `admin` / `admin`.
- **Sign-in throttling** per address and per account, and a fail2ban jail (`w-ui` → 22) that bans an address after repeated failures — on the panel's port only, so a customer's tunnel and your SSH are untouched.
- **Two-factor authentication** (TOTP), asked for only after the password was right, so it never reveals which accounts have one.
- **A strict Content-Security-Policy** with a per-request nonce.
- **An unprivileged service account.** The panel runs as `wui` with `CAP_NET_ADMIN` and `CAP_NET_BIND_SERVICE` only, `ProtectSystem=strict`, and one writable directory.
- **Secrets stay put.** Passwords are bcrypt hashes; API tokens are stored as hashes and shown once; interface and device private keys never appear in anything the panel renders for display — only in the customer's own file.
- **Signed releases.** A panel built with the project's public key refuses an update whose signature does not match.

## What you should do

- Keep the firewall to the panel port, the tunnel ports, port 80 for renewals, and SSH.
- Turn on two-factor authentication: the password is the only thing between an attacker and every customer's configuration.
- Send backups somewhere else — Telegram, or copy `/var/backups/wui` off the box.
- Read `install-result.env` once and delete it.

## Reporting a vulnerability

See [SECURITY.md](https://github.com/AbolfazlTafakori/w-ui/blob/main/SECURITY.md). Please do not open a public issue for a security problem.
