---
description: "Find your generated credentials, reach the panel, change what should be changed, turn on two-factor authentication, and know what protects the sign-in form."
---

# First login

After the install, your first job is to sign in and **secure the panel** before you put a customer on it.

## Reach the panel

```text
https://<address-or-domain>:<port>/<path>/
```

The install prints all three — port, path, credentials — and writes them to a root-only file:

```bash [/etc/wui/install-result.env  (mode 600)]
WUI_USERNAME=…
WUI_PASSWORD=…
WUI_PANEL_PORT=…
WUI_WEB_BASE_PATH=…
WUI_ACCESS_URL=…
WUI_API_TOKEN=…
WUI_DB_TYPE=sqlite
```

If you missed them:

```bash
w-ui              # menu → 11 (View Current Settings)
w-ui settings     # the one-shot form
```

::: warning
There is no default `admin` / `admin` to change — both are generated — but if you passed `--username admin --password …` to the installer for convenience, change it now, before the first customer.
:::

## Change the port and path

A non-default port and a long random **path** are what keep the panel off scanners' lists. Both are random by default; to change them later:

- **`w-ui` → 8 — Reset Web Base Path** (randomises it)
- **`w-ui` → 10 — Change Port**
- or Settings → General

Both apply at the next restart; note the new address first.

## Change the administrator

- **`w-ui` → 7 — Reset Username & Password** (generated unless typed)
- or Settings → Security

Changing the password signs every session out.

## Two-factor authentication

Settings → Security → **Two-factor authentication**. Scan the code with any TOTP app (Google Authenticator, Aegis, 1Password…), confirm one code, and keep the recovery key somewhere safe. From then on the sign-in page asks for a six-digit code — **only after the password was right**, so the form never reveals which accounts have it.

## What protects the sign-in form

- **Throttling** — five free attempts per address and per account, then a wait that starts at 30 seconds and doubles, capped at 15 minutes. A quiet record is forgotten.
- **One error message** — "incorrect username or password" for a bad password and a bad code alike.
- **fail2ban** — installed by the installer, watching the panel's own log; repeat offenders are banned on the panel port. `w-ui` → 22.
- **Sessions** — twelve hours by default (Settings → General → Session length), invalidated when the password changes.
- **The path** — nothing outside it answers, the sign-in page included.

## Next

- [First tunnel, first customer](/guide/first-steps)
- [Security](/operations/security)
