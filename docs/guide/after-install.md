---
description: "The installer ends the way 3x-ui's does, with everything you need on one screen. Copy it somewhere safe before you close the terminal."
---

# After the install

The installer ends the way 3x-ui's does, with everything you need on one screen. Copy it somewhere safe before you close the terminal.

```
  ═══════════════════════════════════════════
       Panel Installation Complete!
  ═══════════════════════════════════════════
  Username:    kQz8mR2pLw
  Password:    Hn4vT9xB2cWq7sYe
  Port:        41234
  WebBasePath: aBcDeFgHiJkLmNoPqR
  Database:    SQLite (/var/lib/wui/wui.db)
  Access URL:  https://203.0.113.9:41234/aBcDeFgHiJkLmNoPqR/
  API Token:   wui_Qm3…
  ═══════════════════════════════════════════
  ⚠ IMPORTANT: Save these credentials securely!
  ⚠ SSL Certificate: Enabled and configured
  Install result written to /etc/wui/install-result.env (mode 600).
```

## Where each line comes from

| Line | What it is | Where to change it later |
|------|-----------|--------------------------|
| Username / Password | the administrator account | `w-ui` → 7, or Settings → Security |
| Port | the one port the panel answers on, by address and by domain | `w-ui` → 10 |
| WebBasePath | the secret path; nothing outside it answers | `w-ui` → 8 |
| Access URL | paste this into a browser | — |
| API Token | a bearer token minted for automation, shown once | Nodes → tokens, or `wui token issue` |

## `/etc/wui/install-result.env`

The same facts, machine-readable, root-only (mode 600), for a cloud-init script or a login banner to pick up:

```bash
WUI_USERNAME=kQz8mR2pLw
WUI_PASSWORD=Hn4vT9xB2cWq7sYe
WUI_PANEL_PORT=41234
WUI_WEB_BASE_PATH=aBcDeFgHiJkLmNoPqR
WUI_ACCESS_URL=https://203.0.113.9:41234/aBcDeFgHiJkLmNoPqR/
WUI_API_TOKEN=wui_Qm3…
WUI_DB_TYPE=sqlite
```

Delete it once you have the values somewhere safer; nothing reads it after the install.

## What else the installer did

- **fail2ban** was installed and given a jail that watches the panel's sign-in log (`w-ui` → 22 to manage it; `WUI_ENABLE_FAIL2BAN=false` to skip).
- **A certificate** was issued and its renewal scheduled — acme.sh's cron entry plus a systemd timer every six hours. The panel picks a renewed certificate up by itself; nothing restarts.
- **The `w-ui` command** was installed. It prints its subcommands at the end:

```
┌───────────────────────────────────────────────────────┐
│  w-ui control menu usages (subcommands):              │
│                                                       │
│  w-ui             - Admin Management Script           │
│  w-ui start       - Start                             │
│  w-ui stop        - Stop                              │
│  w-ui restart     - Restart                           │
│  w-ui status      - Current Status                    │
│  w-ui settings    - Current Settings                  │
│  w-ui enable      - Enable Autostart on OS Startup    │
│  w-ui disable     - Disable Autostart on OS Startup   │
│  w-ui log         - Check logs                        │
│  w-ui banlog      - Check Fail2ban ban logs           │
│  w-ui update      - Update                            │
│  w-ui legacy      - Legacy version                    │
│  w-ui install     - Install                           │
│  w-ui uninstall   - Uninstall                         │
└───────────────────────────────────────────────────────┘
```

## Lost the password?

```bash
w-ui            # → 7. Reset Username & Password
```

or directly:

```bash
wui admin reset --username admin
```

which prints a new generated password once.

## Next

[First tunnel, first customer](/guide/first-steps).
