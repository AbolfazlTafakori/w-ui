---
description: "One command installs W-UI on Ubuntu, Debian, AlmaLinux, Rocky or Fedora; the questions it asks, every flag, unattended installs, and running behind a proxy."
---

# Install

Run one command, as root, on a fresh server:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

## What it needs

| | |
|---|---|
| OS | Ubuntu 22.04, 24.04 or 26.04; Debian 12 or 13; AlmaLinux 9; Rocky 9; Fedora 41. Each is installed clean in CI for every release. |
| Architecture | x86-64 or arm64 |
| Kernel | One with `nft_quota`. Stock Ubuntu, Debian and most VPS kernels have it; the installer checks and tells you. |
| Ports | The panel port (TCP), one UDP port per WireGuard interface, TCP 443 if you use OpenVPN, and port 80 reachable for a Let's Encrypt certificate |

The installer brings everything the protocols need: `nftables` and `wireguard-tools`, AmneziaWG, OpenVPN with `easy-rsa`, strongSwan and `xl2tpd` for L2TP/IPsec and IKEv2, `pppd` for PPTP, `ocserv` for OpenConnect, xray and sing-box for the proxy outbounds, and `qrencode`. Each protocol stays off until a tunnel uses it. The installer also turns on IPv4 and IPv6 forwarding, creates an unprivileged `wui` user, and registers a hardened systemd unit.

## The questions

The first question is how to install:

```
1) Manual     — you choose the port, the URL path, the administrator, the database,
                the certificate and what is installed
2) Automatic  — three questions (database, domain, certificate); everything else is
                chosen and set up for you, and shown at the end   ← default
```

`--auto` chooses the automatic install without asking, and `--manual` the manual one.

### Automatic

It asks three questions, then installs everything on its own:

1. **The database:** SQLite or PostgreSQL, the same choice as in the manual install below.
2. **A domain,** if you have one whose DNS points at this server. It gets a Let's Encrypt certificate. The installer checks the record at once and tells you if it does not point here yet. Leave it blank if you have no domain.
3. **Without a domain, a certificate for this server's IP address?** Yes by default: Let's Encrypt, valid for six days, renewed automatically.

Everything else is chosen the way pressing Enter through the manual questions would choose it: a random free port, a random URL path, a generated administrator and password, a free subscription port, and every protocol. At the end the installer prints the panel's address, the username, the password, the ports and the path.

Nothing after the three questions asks anything. If a certificate cannot be issued, the installer skips it rather than asking. Port 80 served by another project on the server, for example, is left alone. The panel then comes up on plain HTTP until you get a certificate from the menu (`w-ui` → 20).

### Manual

It asks for every choice, in this order, then runs unattended:

1. **The panel port.** A random free one unless you name it. A port something else already listens on is refused.
2. **The URL path.** A random 18-character prefix. Everything outside it answers 404, the sign-in page included.
3. **The administrator.** The name and the password are both generated unless you set them.
4. **The database:**

   ```
   1) SQLite      — one file, nothing to install; fine up to a few thousand customers   ← default
   2) PostgreSQL  — for a large number of customers; installed and configured here
   ```

   A backup from either restores into the other, so you are not locked into this choice. See [Backup and restore](/operations/backup-restore).

5. **How the panel is reached:**

   ```
   1) Let's Encrypt for Domain (90-day validity, auto-renews)
   2) Let's Encrypt for IP Address (6-day validity, auto-renews)   ← default
   3) Custom SSL Certificate (path to existing files)
   4) Skip SSL (advanced — behind reverse proxy / SSH tunnel only)
   ```

   Whichever you choose, the panel answers on one port, by the address and by the domain alike. See [Certificates](/guide/certificates).

6. **The protocols.** One question, *Install every protocol?*, yes by default. If you answer no, each is asked on its own: OpenVPN, AmneziaWG, L2TP/IPsec, IKEv2, PPTP, OpenConnect, and xray with sing-box. A protocol left out is not lost: the panel installs it when you make the first tunnel of that kind.

The **subscription port** is not asked. The installer picks a free random one beside the panel's port, turns the subscription service on and gives it the panel's certificate, so a customer's link works from the first minute. `--sub-port N` names one instead.

If you press Enter through every question, nothing about the result can be guessed: a random port, a random path, a random administrator, a generated password, and a certificate for the server's own address.

## Unattended installs

Every answer is also a flag, and `--yes` skips the questions:

| Flag | Effect |
|------|--------|
| `--port N` | Listen on port N |
| `--sub-port N` | The subscription service's port (default: random) |
| `--db sqlite\|postgres` | Where the data lives (default: `sqlite`) |
| `--path SEG` / `--no-path` | The URL prefix, or none |
| `--username NAME` / `--password PASS` | The administrator |
| `--domain NAME` | Let's Encrypt for this domain |
| `--ip-cert [ADDR]` | A six-day Let's Encrypt certificate for the server's address, found automatically unless given |
| `--tls-cert PATH` / `--tls-key PATH` | A certificate you already have |
| `--no-tls` | Plain HTTP; only behind a proxy or an SSH tunnel |
| `--local-only` | Bind to 127.0.0.1 |
| `--no-amnezia` / `--no-openvpn` / `--no-l2tp` / `--no-ikev2` / `--no-pptp` / `--no-openconnect` / `--no-proxy-cores` | Skip that protocol's packages; the panel installs them with the first tunnel of that kind |
| `--local PATH` | Install a binary you already built |
| `--from-source` | Build the latest commit, installing Go if needed |
| `--auto` / `--manual` | The automatic install (three questions) or the manual one (every question), without being asked which |
| `-y`, `--yes` | Ask nothing |

Environment variables work too: `WUI_DOMAIN`, `WUI_SERVER_IP`, `WUI_SSL_MODE=ip|domain|none`, `WUI_ADMIN_USER`, `WUI_ADMIN_PASSWORD`, `WUI_SUB_PORT`, `WUI_DB_DRIVER=sqlite|postgres`, `WUI_ENABLE_FAIL2BAN=false`.

```bash
# cloud-init style
WUI_SSL_MODE=ip bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --port 2053 -y
```

## Behind nginx or another proxy

In the manual install, choose option 4 at the certificate question and answer **yes** to binding to 127.0.0.1. Point the proxy at `http://127.0.0.1:PORT/` and terminate TLS there. Then put the proxy's address under Settings → General → **Trusted proxy CIDRs**, so the panel reads customers' addresses from `X-Forwarded-For`.

## Running the installer again

On a server that already has the panel, running the installer again is an **upgrade**. It keeps the port, the path, the administrator and the certificate, replaces the binary, and restarts the service. Customers on WireGuard stay connected: the tunnels live in the kernel, not in the panel's process.
