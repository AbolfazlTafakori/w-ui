# Install

One command, as root, on a fresh server:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

## What it needs

| | |
|---|---|
| OS | Ubuntu 22.04 / 24.04, Debian 12 / 13, AlmaLinux 9, Rocky 9, Fedora 41 — every one is installed clean in CI on each release |
| Architecture | x86-64 or arm64 |
| Kernel | one with `nft_quota` — stock Ubuntu, Debian and most VPS kernels have it; the installer checks and says so |
| Ports | the panel port (TCP), one UDP port per WireGuard interface, TCP 443 if you use OpenVPN, and port 80 reachable for a Let's Encrypt certificate |

It installs `nftables`, `wireguard-tools`, AmneziaWG, OpenVPN, `easy-rsa` and `qrencode`, enables IPv4/IPv6 forwarding, creates an unprivileged `wui` user, and registers a hardened systemd unit.

## The questions

It asks first, then works unattended:

1. **The panel port** — a random free one unless you name it. It refuses a port something else already listens on.
2. **The URL path** — a random 18-character prefix. Everything outside it answers 404, the sign-in page included.
3. **The administrator** — name and password both generated unless you set them.
4. **How the panel is reached:**

   ```
   1) Let's Encrypt for Domain (90-day validity, auto-renews)
   2) Let's Encrypt for IP Address (6-day validity, auto-renews)   ← default
   3) Custom SSL Certificate (path to existing files)
   4) Skip SSL (advanced — behind reverse proxy / SSH tunnel only)
   ```

   Whatever you choose, the panel answers on the one port — by the address and by the domain alike. See [Certificates](/guide/certificates).

5. Whether to install OpenVPN and AmneziaWG alongside WireGuard.

Press enter through all of it and nothing about the result is guessable: random port, random path, random administrator, generated password, a certificate for the server's own address.

## Unattended installs

Every answer is also a flag, and `--yes` skips the questions:

| Flag | Effect |
|------|--------|
| `--port N` | Listen on port N |
| `--path SEG` / `--no-path` | The URL prefix, or none |
| `--username NAME` / `--password PASS` | The administrator |
| `--domain NAME` | Let's Encrypt for this domain |
| `--ip-cert [ADDR]` | Six-day Let's Encrypt certificate for the server's address (auto-detected unless given) |
| `--tls-cert PATH` / `--tls-key PATH` | A certificate you already have |
| `--no-tls` | Plain HTTP — only behind a proxy or an SSH tunnel |
| `--local-only` | Bind to 127.0.0.1 |
| `--no-amnezia` / `--no-openvpn` | Skip those packages |
| `--local PATH` | Install a binary you already built |
| `--from-source` | Build the latest commit (installs Go if needed) |
| `-y`, `--yes` | Ask nothing |

Environment variables work too: `WUI_DOMAIN`, `WUI_SERVER_IP`, `WUI_SSL_MODE=ip|domain|none`, `WUI_ADMIN_USER`, `WUI_ADMIN_PASSWORD`, `WUI_ENABLE_FAIL2BAN=false`.

```bash
# cloud-init style
WUI_SSL_MODE=ip bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --port 2053 -y
```

## Behind nginx or another proxy

Choose option 4 and answer **yes** to binding to 127.0.0.1. Then point the proxy at `http://127.0.0.1:PORT/` and terminate TLS there. The panel sets `X-Forwarded-*` aware headers when `WUI_TRUSTED_PROXIES` names the proxy (Settings → Security).

## Re-running the installer

Running it again on a server that already has the panel is an **upgrade**: it keeps the port, the path, the administrator and the certificate, replaces the binary, and restarts the service. Customers on WireGuard are not disconnected — the tunnels live in the kernel, not in the panel process.
