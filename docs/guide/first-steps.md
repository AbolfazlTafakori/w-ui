# First tunnel, first customer

A fresh panel is empty on purpose: no interfaces, no customers, one administrator. Three steps get a customer connected.

## 1. Create an interface

**Interfaces → Add Inbound.** An interface is one tunnel: a subnet, a port, and the public hostname customers dial.

| Field | Note |
|-------|------|
| Protocol | `wireguard` or `openvpn` |
| Mode | `standard`, or `amnezia` — obfuscated WireGuard for networks that block the plain one |
| Subnet | `10.66.0.0/16` gives ~65 000 addresses; `.1` becomes the gateway |
| Listen port | UDP for WireGuard; OpenVPN can be TCP 443, which passes where little else does |
| Endpoint host | the hostname or IP that goes into customer configs |
| NAT interface | your public NIC, usually `eth0` — masquerading needs it |
| MTU | `1420` suits most networks |

The keypair is generated for you. The private key never appears in any config you can copy out of the panel. For OpenVPN, a certificate authority, server certificate and `tls-crypt` key are generated in-process and stored with the interface — a database backup is a complete backup.

## 2. Create a customer

**Clients → Add Clients.** One client is one customer; each gets one device (account) per allowed device, each with its own key and address.

| Field | Note |
|-------|------|
| Name | what you will search for |
| Protocol | chosen per client |
| Interface(s) | which tunnels the customer can use |
| Total traffic | `0` = unlimited; enforced in the kernel |
| Expiry | `0` = never; or **start on first use** with a number of days |
| Device limit | how many separate configs to issue |
| Reset cycle | `none`, `daily`, `weekly`, `monthly` |
| Speed limit | per-customer HTB class on every device their traffic leaves by |
| Telegram id | lets the customer ask the bot about their own plan |

**Bulk:** the same form makes many at once — a prefix and a count.

## 3. Hand out the config

Open the customer's row:

- **QR code** — one per device, per host; phones scan it with the WireGuard or AmneziaWG app.
- **Client information** — every link, every download, the subscription link.
- **Subscription link** — one URL a customer keeps; the page behind it lists every device and every host, with QR codes, in their language and your chosen template. Apps that speak subscriptions re-fetch it.

## What the statuses mean

| Status | Meaning |
|--------|---------|
| `active` | working |
| `disabled` | switched off by you |
| `exhausted` | hit the data quota |
| `expired` | past the expiry date |

Anything other than `active` has its peers removed from the kernel, so the customer stops passing traffic. Raising the quota or extending the expiry brings them straight back — no restart, no reissued config.

## How OpenVPN customers differ

WireGuard identifies a customer by key. OpenVPN customers get a username and password, and there are **no per-client certificates**: creating one appends a credential, not a certificate to issue and revoke. Three properties follow:

- **one credential serves one person** — `duplicate-cn` is off; a second login disconnects the first;
- **addresses are pinned** through `client-config-dir`, so the nftables quota keeps counting the right person across reconnects;
- **cutting someone off is immediate** — the credential is removed *and* the live session is killed over the management socket.

Adding or removing a customer never restarts the server, and restarting or upgrading the panel does not disconnect anyone.
