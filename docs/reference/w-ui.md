---
description: "Installed alongside the panel. Run w-ui for the menu, or w-ui <subcommand> for one thing. The menu is the classic one, number for number; what is the core there is the tunnels here."
---

# The `w-ui` menu

Installed alongside the panel. Run `w-ui` for the menu, or `w-ui <subcommand>` for one thing. The menu is the classic one, number for number; what is the core there is the tunnels here.

```text
  W-UI  ·  WireGuard · AmneziaWG · OpenVPN   v1.1.0

  ╭──────────────────────────────────────────────────────╮
  │   0  Exit                                            │
  ├──────────────────────────────────────────────────────┤
  │ Install and update                                   │
  │   1  Install                                         │
  │   2  Update                                          │
  │   3  Update to the dev channel (latest commit)       │
  │   4  Update this menu                                │
  │   5  Install an older version                        │
  │   6  Uninstall                                       │
  ├──────────────────────────────────────────────────────┤
  │ Access                                               │
  │   7  Reset username and password                     │
  │   8  Reset the URL path                              │
  │   9  Reset settings                                  │
  │  10  Change the port                                 │
  │  11  Show current settings                           │
  ├──────────────────────────────────────────────────────┤
  │ Service                                              │
  │  12  Start                                           │
  │  13  Stop                                            │
  │  14  Restart                                         │
  │  15  Restart tunnels                                 │
  │  16  Status                                          │
  │  17  Logs                                            │
  │  18  Enable start on boot                            │
  │  19  Disable start on boot                           │
  ├──────────────────────────────────────────────────────┤
  │ Server                                               │
  │  20  SSL certificate                                 │
  │  21  Cloudflare SSL certificate                      │
  │  22  Connection limit                                │
  │  23  Firewall                                        │
  │  24  SSH port forwarding                             │
  │  25  PostgreSQL                                      │
  │  26  Enable BBR                                      │
  │  27  Update geo files                                │
  │  28  Speedtest by Ookla                              │
  ├──────────────────────────────────────────────────────┤
  │ Panel                  ● Running                     │
  │ Start on boot          Yes                           │
  │ WireGuard wg0          udp/443  ● Running            │
  │ OpenVPN OpenVpn        tcp/2053  ● Running           │
  ╰──────────────────────────────────────────────────────╯
```

Under the box: the panel state, whether it starts at boot, and one line per tunnel.

## What each one does

| # | Does | Asks |
|---|------|------|
| 1 | runs the installer | — |
| 2 | fetches the latest release, verifies it, replaces the binary and this script, restarts — nothing is asked; port, path, certificate, database and administrator stay. A panel with no certificate is offered one | confirm |
| 3 | the same, from the rolling `dev-latest` build of the latest commit | confirm |
| 4 | replaces this script with the one published for the installed version | confirm |
| 5 | installs a named earlier version | the version |
| 6 | removes the panel, its data and its unit — **after copying the data to `/root/wui-last-copy-…`** | confirm, default no |
| 7 | new username and password (generated unless typed), and whether to drop two-factor | both, 2FA, then whether to restart |
| 8 | a new random URL path | confirm |
| 9 | every panel setting back to the environment's; the admin and customers stay | confirm, default no |
| 10 | the port, applied at the next restart | the port |
| 11 | listen, port, path, certificate, database, **Access URL** — and an offer to get an IP certificate if there is none | — |
| 12–14 | start / stop / restart the service | — |
| 15 | every tunnel again from its configuration; the panel stays up | — |
| 16 | `systemctl status wui` | — |
| 17 | follow the debug log, or clear all journal logs | which |
| 18–19 | enable / disable at boot | — |
| 20 | get a certificate for a domain or for the IP, revoke, force-renew, list, set custom paths | see [Certificates](/guide/certificates) |
| 21 | a wildcard certificate through Cloudflare's DNS API | domain, token |
| 22 | fail2ban on the sign-in form: install, ban duration, unban all, ban log, ban / unban one, live log, status, restart, uninstall | which |
| 23 | ufw: install, list, open, delete, enable, disable, status | which |
| 24 | bind the panel to 127.0.0.1 and print the `ssh -L` command to reach it | which |
| 25 | PostgreSQL: install the server and the panel's database, move the data SQLite → PostgreSQL or back, status, start, stop, restart, autostart, log, and Backup & Restore | which |
| 26 | enable / disable BBR | which |
| 27 | refresh the country lists routing rules use, or fetch one | which |
| 28 | install and run Ookla's speedtest | — |

## Subcommands

```
w-ui start | stop | restart | restart-tunnels | status
w-ui settings                 # what the panel is actually running with
w-ui enable | disable
w-ui log | banlog
w-ui update | update-dev | legacy
w-ui backup | postgres
w-ui update-all-geofiles
w-ui backup
w-ui install | uninstall
w-ui setup-fail2ban
```

A question the script asks with nobody there to answer — piped input, a script — is answered **no**. A non-interactive run can never install, update or remove anything.
