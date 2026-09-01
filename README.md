# W-UI

A management panel for WireGuard and OpenVPN, built for reselling access:
per-customer data quotas, expiry dates, device limits, and downloadable configs.

Admin-only. There is no customer portal — you create a client in the panel and
hand out the config or QR code yourself.

---

## Why not just use an existing panel

The difference is *where the limit is enforced*.

Most panels poll a byte counter every couple of seconds and disable the customer
once the number crosses their quota. Between two polls the customer keeps
transferring at full speed. On a 100 Mbit link that is roughly 25 MB of
overshoot; on a gigabit link, 250 MB. If you sell a 1 GB plan, a fast customer
silently gets 1.25 GB.

W-UI programs the limit into the kernel itself, as an `nftables` quota object
attached to the customer's address:

```
quota q_c14 { over 1073741824 bytes used 402653184 bytes }
```

The kernel drops the packet that crosses the ceiling. The panel is not in the
data path, so the overshoot is one packet rather than one polling interval, and
it stays that way whether the customer has a 10 Mbit or a 10 Gbit link.

The panel still polls — but only to *read* usage for the UI and to decide when
to remove a peer, never to enforce the byte limit.

> **This depends on your kernel having `nft_quota`.** Stock Ubuntu, Debian and
> Hetzner kernels do. Minimal, custom and container kernels sometimes do not.
> `install.sh` tests for it and warns loudly if it is missing; without it, limits
> fall back to polling and overshoot returns. The Overview page and
> `GET /api/system` both report which mode you are in.

---

## Status

| Phase | Scope | State |
|-------|-------|-------|
| 1 | Data model, address allocator, driver and enforcement contracts | done |
| 2 | nftables enforcement engine and reconciler | done |
| 3 | WireGuard / AmneziaWG kernel driver | done |
| 4 | OpenVPN driver | done |
| 5 | Bandwidth rate limiting (`tc`) | not started |
| 6 | Backups, sharing detection, Telegram notifications | not started |

**What works today:** WireGuard, AmneziaWG and OpenVPN, end to end. The panel
brings up a real interface, writes accounts to it, hands out working configs,
meters traffic, and cuts customers off when their quota or expiry is reached.

**What does not:** bandwidth rate limiting is stored on the client but not
applied. Multi-node is modelled in the schema but there is no Nodes page, so one
panel drives one server.

---

## Requirements

- Linux with `nftables` and the `nft_quota` kernel module
- `wireguard-tools` (and `amneziawg` if you want the obfuscated mode)
- `openvpn` and a `tun` device, if you sell OpenVPN
- `CAP_NET_ADMIN` — the panel does not need to run as root
- A public IPv4 address and an open UDP port

Tested on Ubuntu 24.04.

---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh | sudo bash
```

The installer does the whole server, not just the panel: it installs
`nftables`, `wireguard`, `wireguard-tools`, `amneziawg`, `openvpn`, `easy-rsa`
and `qrencode`, enables IPv4 and IPv6 forwarding, verifies that the kernel can
actually hold quota objects, creates an unprivileged `wui` system user, and
installs a hardened systemd unit.

| Flag | Effect |
|------|--------|
| `--port N` | Listen on port N instead of 2096 |
| `--local PATH` | Install a binary you already built |
| `--from-source` | Build from the current checkout (needs Go) |
| `--no-amnezia` | Skip the AmneziaWG packages |
| `--no-openvpn` | Skip OpenVPN and easy-rsa |
| `--uninstall` | Remove the panel, keep the data |
| `--purge` | Remove the panel and the data |

Uninstalling never removes WireGuard or OpenVPN — other things on the server may
be using them.

On first start the panel prints a generated admin password to its log **once**:

```bash
sudo journalctl -u wui | grep -A6 "First run"
```

The systemd unit runs the panel as `wui`, not root, with
`AmbientCapabilities=CAP_NET_ADMIN`, `ProtectSystem=strict` and a single
writable path (`/var/lib/wui`).

---

## Configuration

Everything is environment variables — there is no config file to keep in sync.

| Variable | Default | Meaning |
|----------|---------|---------|
| `WUI_LISTEN` | `127.0.0.1:2096` | Address to serve on |
| `WUI_DATA_DIR` | `./data` | Where the database and state live |
| `WUI_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `WUI_DB_SOURCE` | `<data dir>/wui.db` | DSN or file path |
| `WUI_COLLECT_INTERVAL` | `2s` | How often usage is read (minimum `1s`) |
| `WUI_DEFAULT_LOCALE` | `en` | `en` or `fa` |
| `WUI_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WUI_LOG_FORMAT` | `text` | `text` or `json` |
| `WUI_DEBUG` | `false` | Verbose diagnostics |

The default listen address is loopback on purpose. Put it behind nginx or
Caddy with TLS rather than exposing it directly.

---

## Using it

### 1. Create an interface

**Interfaces → New.** An interface is one tunnel: a subnet, a UDP port, and the
public hostname customers will dial.

| Field | Note |
|-------|------|
| Subnet | `10.66.0.0/16` gives you ~65k addresses; `.1` becomes the gateway |
| Listen port | Must be open in your provider's firewall (UDP) |
| Endpoint host | The hostname or IP that goes into customer configs |
| NAT interface | Your public NIC, usually `eth0` — needed for masquerading |
| MTU | `1420` is right for most networks |
| Mode | `standard`, or `amnezia` for obfuscation where WireGuard is blocked |

The keypair is generated for you. The private key never appears in any config
you can copy out of the panel.

### 2. Create a client

**Clients → New.** A client is one customer. Each client gets one device
(account) per allowed device, each with its own keypair and address.

| Field | Note |
|-------|------|
| Protocol | Chosen per client, at creation time |
| Quota | `0` means unlimited |
| Expiry | `0` means never |
| Device limit | How many separate configs to issue |
| Reset cycle | `none`, `daily`, `weekly`, `monthly` |

### 3. Hand out the config

Open the client and use **Share** for each device — a `.conf` file, a copyable
block, or a QR code for phones.

### Statuses

| Status | Meaning |
|--------|---------|
| `active` | Working |
| `disabled` | Switched off by you |
| `exhausted` | Hit the data quota |
| `expired` | Past the expiry date |

Anything other than `active` has its peers removed from the kernel, so the
customer stops passing traffic. Raising the quota or extending the expiry brings
them straight back — no restart, no reissued config.

### How OpenVPN accounts work

WireGuard has no concept of a username, so a WireGuard customer is identified by
a key. OpenVPN customers get a username and password instead, and the panel is
built around that difference rather than papering over it.

There are **no per-client certificates**. The server runs with
`verify-client-cert none`, so creating a customer means appending a line to a
credential file — not issuing, tracking and revoking a certificate. Every
interface generates its own certificate authority, server certificate and
`tls-crypt` key when you create it, in-process, with no `easy-rsa` directory to
keep in sync. That material lives in the interface row, so a database backup is a
complete backup.

Three properties follow from the configuration and are worth knowing:

- **One credential cannot serve two people.** `duplicate-cn` is off, so a second
  login on the same username disconnects the first. The protocol enforces it;
  the panel does not have to detect it after the fact.
- **Addresses are pinned.** Each account gets a fixed address through
  `client-config-dir`, so a customer keeps the same address across reconnects and
  the nftables quota attached to it keeps counting the right person. Without
  this, the address would come from a pool and change on every reconnect.
- **Cutting someone off is immediate.** Removing them rewrites the credential
  file *and* kills their live session over the management socket. Removing the
  credential alone would only stop their next login, which is no use for a
  customer who has already run out of data.

Adding or removing a customer never restarts the server, because a restart would
disconnect everyone else on the interface. The server process is also started
detached and adopted again by name on the next run, so **restarting or upgrading
the panel does not disconnect anyone** — the panel compares a fingerprint of the
configuration and only restarts when the interface itself actually changed.

---

## How it works

The database is the source of truth. Kernel state is derived from it and rebuilt
to match, rather than being mutated as events arrive. Every two seconds the
reconciler runs three steps:

1. **Collect** — atomically read and zero the nftables counters
   (`nft -j reset counters`), then write usage to the database through a single
   serialized writer.
2. **Evaluate** — decide in SQL which clients are now over quota or past expiry.
3. **Apply** — push the desired peer set and the desired ruleset to the kernel.

This means the panel is self-healing. If the machine reboots, someone flushes
`nftables` by hand, or the panel is killed mid-write, the next tick rebuilds
everything from the database. It also means a panel restart drops nobody: the
driver never deletes or recreates a link or a peer that is already correct.

```
internal/
  api/          HTTP handlers, auth, JSON
  backend/      driver contract and registry
    ovpndriver/ OpenVPN (credential files plus the management socket)
    wgdriver/   WireGuard + AmneziaWG (netlink for standard, awg for amnezia)
  database/     GORM models, migrations, settings
  enforce/      nftables ruleset generator and applier
  ipam/         address allocation
  reconciler/   the loop above; the only thing that touches the data plane
  service/      business rules for clients, groups, interfaces, profiles
  ovpnconf/     OpenVPN renderer and in-process certificate authority
  wgconf/       the single config renderer, shared by panel and driver
  web/          the embedded frontend
web/            Vue 3 + Vite source
```

Two design notes worth knowing if you plan to modify it:

- **Peers are diffed, never replaced.** WireGuard keeps session state per peer.
  Replacing the whole peer list on every tick tears down live handshakes, and the
  tunnel then looks connected from both ends while moving no traffic at all.
  `wgdriver` sends only what changed; `TestUnchangedInterfaceIssuesNoPeerOperations`
  pins this.
- **The quota drop comes before the counter.** Otherwise dropped bytes would be
  billed to the customer who never received them.
- **The credential alphabet is one constant.** The generator and the shell script
  that validates a login share it. When they disagreed during development, the
  panel issued passwords that were rejected at login with nothing in any log
  explaining why.

The binary is CGO-free and embeds the frontend, so it cross-compiles to a single
static file with no runtime dependencies.

---

## Building from source

```bash
cd web && npm install && npm run build && cd ..
go build -o bin/wui ./cmd/wui
```

The frontend build writes into `internal/web/dist`, which `go:embed` picks up —
so `npm run build && go build` produces one binary carrying both halves.

For a server:

```bash
GOOS=linux GOARCH=amd64 go build -o bin/wui-linux-amd64 ./cmd/wui
```

During frontend work, run the Vue dev server instead and let it proxy the API:

```bash
cd web && npm run dev
```

---

## Tests

```bash
go test ./...
```

Unit tests cover the ruleset generator, the peer diff, the config renderer, the
address allocator and the reconciler.

`test/` holds two integration scripts that run against a real kernel in Docker.
They need a privileged container because they create network interfaces and
namespaces:

```bash
GOOS=linux GOARCH=amd64 go build -o bin/wui-linux-amd64 ./cmd/wui

docker run --rm --privileged --cap-add=NET_ADMIN -v "$PWD:/src" ubuntu:24.04 \
  bash -c 'apt-get update -qq && apt-get install -y -qq wireguard-tools iproute2 \
    curl nftables python3 iputils-ping && bash /src/test/interface.sh'
```

- `test/interface.sh` — the panel creates a real `wireguard` link, writes peers,
  removes them when a client is disabled, restores them when re-enabled, and the
  server key in the issued config matches the kernel's.
- `test/tunnel.sh` — builds a client namespace and an "internet" namespace, then
  checks that a real handshake completes, that traffic actually reaches the far
  side through the tunnel, that it is billed to the right customer, that the
  customer is cut off at their quota, and that topping them up restores service.
- `test/openvpn.sh` — the same for OpenVPN, using a real `openvpn` client that
  logs in with the username and password the panel issued. It also checks that
  the client receives the address the panel pinned, that a second login on the
  same credential evicts the first, that cutting a customer off drops their live
  session and refuses their next login, and that restarting the panel adopts the
  running server instead of disconnecting everyone.

Note that `nft_quota` is absent from the WSL2 kernel, so running these under
Docker Desktop on Windows will show quota overshoot. That is the kernel, not the
panel — `test/tunnel.sh` prints the overshoot so it is not mistaken for a pass.

---

## Security

- Passwords are stored as bcrypt hashes; the first-run password is shown once
  and is not recoverable.
- Sessions are JWTs.
- Interface and device private keys are never included in any config the panel
  renders for copying or display — only in the client's own file, which is the
  one place it belongs.
- The panel binds to loopback by default. Terminate TLS in front of it.
- Run it behind a firewall that exposes only your tunnel ports and your reverse
  proxy.

---

## License

Copyright (c) 2026 Abolfazl Tafakori. All rights reserved.

The source is public so you can read it, audit it, and see how the enforcement
works. It is not open source: no permission is granted to use, copy, modify,
redistribute or sell it, in whole or in part. If you want to use it, get in
touch.

See [LICENSE](LICENSE).
