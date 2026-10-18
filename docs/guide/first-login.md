---
description: "Find your generated credentials, reach the panel, change what should be changed, turn on two-factor authentication, and know what protects the sign-in form."
---

# First login

After the install, the first job is to sign in and **secure the panel**, before you put a customer on it.

## Reach the panel

```text
https://<address-or-domain>:<port>/<path>/
```

The installer prints the port, the path and the credentials, and writes them to a file only root can read:

```bash [/etc/wui/install-result.env  (mode 600)]
WUI_USERNAME=…
WUI_PASSWORD=…
WUI_PANEL_PORT=…
WUI_WEB_BASE_PATH=…
WUI_ACCESS_URL=…
WUI_API_TOKEN=…
WUI_DB_TYPE=sqlite
```

If you missed them, either of these shows them again:

```bash
w-ui              # menu → 11 (Show current settings)
w-ui settings     # the same, without the menu
```

::: warning
There is no default `admin` / `admin` to change, because both are generated. If you passed `--username admin --password …` to the installer for convenience, change them now, before the first customer.
:::

## Change the port and the path

A port that is not the usual one and a long random **path** keep the panel off scanners' lists. Both are random from the start. To change them later:

- **`w-ui` → 8, Reset the URL path**, makes a new random path.
- **`w-ui` → 10, Change the port.**
- Or in the panel, *Settings → General → General*: **Listen Port** and **URI Path**.

Both take effect at the next restart, so write down the new address first.

## Change the administrator

- **`w-ui` → 7, Reset username and password**, generates both unless you type them.
- Or in the panel, *Settings → Authentication → Admin credentials*.

Changing the password signs out every session.

## Two-factor authentication

In *Settings → Authentication → Two-factor authentication*, press **Enable 2FA**. Scan the code with any TOTP app, such as Google Authenticator, Aegis or 1Password, and confirm one code. From then on the sign-in page asks for a six-digit code, **only after the password was right**, so the form never reveals which accounts have two-factor on.

Turning it off asks for a code or for your password, so a lost phone does not lock you out while you still know the password. If both are lost, `w-ui` → 7 resets the account from the server, two-factor included.

## What protects the sign-in form

- **Throttling.** Five free attempts per address and per account, then a wait that starts at 30 seconds and doubles each time, up to 15 minutes. A record that stays quiet is forgotten.
- **One error message.** A wrong password and a wrong code both get "incorrect username or password".
- **fail2ban.** The installer sets it up to watch the panel's own log, and repeat offenders are banned on the panel's port. Manage it from `w-ui` → 22.
- **Sessions.** Twelve hours by default (*Settings → General → Session Duration*), and all of them end when the password changes.
- **The path.** Nothing outside it answers, the sign-in page included.

## Next

- [First tunnel, first customer](/guide/first-steps)
- [Security](/operations/security)
