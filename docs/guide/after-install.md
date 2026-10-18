---
description: "The installer ends with everything you need on one screen. Copy it somewhere safe before you close the terminal."
---

# After the install

The installer ends with everything you need on one screen. Copy it somewhere safe before you close the terminal.

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

## What each line is

| Line | What it is | Where to change it later |
|------|-----------|--------------------------|
| Username / Password | The administrator account | `w-ui` → 7, or Settings → Authentication |
| Port | The one port the panel answers on, by address and by domain | `w-ui` → 10 |
| WebBasePath | The secret path; nothing outside it answers | `w-ui` → 8 |
| Access URL | The address to open in a browser | |
| API Token | A bearer token for automation, shown once | Settings → Authentication → API Token, or `wui token issue` |

## `/etc/wui/install-result.env`

The same values in a form a script can read, readable by root only (mode 600). A cloud-init script or a login banner can pick them up from here:

```bash
WUI_USERNAME=kQz8mR2pLw
WUI_PASSWORD=Hn4vT9xB2cWq7sYe
WUI_PANEL_PORT=41234
WUI_WEB_BASE_PATH=aBcDeFgHiJkLmNoPqR
WUI_ACCESS_URL=https://203.0.113.9:41234/aBcDeFgHiJkLmNoPqR/
WUI_API_TOKEN=wui_Qm3…
WUI_DB_TYPE=sqlite
```

Delete the file once the values are somewhere safer. Nothing reads it after the install.

An update leaves the file as it is and issues no new API token, whether you update by running the installer again, with `w-ui update`, or with the panel's own **Update** button. Only the install writes this file, and only the install issues the `installer` token in it.

## What else the installer did

- **fail2ban** is installed, with a jail that watches the panel's sign-in log. Manage it from `w-ui` → 22; set `WUI_ENABLE_FAIL2BAN=false` at install time to skip it.
- **A certificate** is issued and its renewal scheduled, by acme.sh's cron entry and by a systemd timer that runs every six hours. The panel picks up a renewed certificate on its own, and nothing restarts.
- **The `w-ui` command** is installed. The installer ends by printing its main subcommands:

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

The full list, with `restart-tunnels`, `backup`, `postgres` and the rest, is in the [w-ui command reference](/reference/w-ui).

## Lost the password?

Open the menu and choose 7, *Reset username and password*:

```bash
w-ui
```

Or reset it directly. The command prints a new generated password once:

```bash
wui admin reset --username admin
```

## Next

[First tunnel, first customer](/guide/first-steps).
