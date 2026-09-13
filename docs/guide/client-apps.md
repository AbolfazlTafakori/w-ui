---
description: "Which app a customer needs for WireGuard, AmneziaWG and OpenVPN on every platform, and how to get a config into it."
---

# Client apps

What a customer installs, per tunnel type, and how they load what you hand them.

## WireGuard

| Platform | App | Import |
|----------|-----|--------|
| Android | [WireGuard](https://play.google.com/store/apps/details?id=com.wireguard.android) | **+** → *Scan from QR code*, or *Import from file* with the `.conf` |
| iOS | [WireGuard](https://apps.apple.com/app/wireguard/id1441195209) | **+** → *Create from QR code*, or *Create from file or archive* |
| Windows | [WireGuard for Windows](https://www.wireguard.com/install/) | *Import tunnel(s) from file*, or *Add empty tunnel* and paste the text |
| macOS | WireGuard from the App Store | *Import tunnel(s) from file* |
| Linux | `wireguard-tools` | `wg-quick up ./customer.conf` |

The config file, the copyable text, and the QR code all carry the same thing: the customer's private key, their address, and your interface's public key and endpoint. Hand out **one per device** — each device has its own key.

## AmneziaWG

A WireGuard interface in *amnezia* mode needs the AmneziaWG client, not the plain one; the obfuscation parameters are in the config and the plain app does not understand them.

| Platform | App |
|----------|-----|
| Android | [AmneziaWG](https://play.google.com/store/apps/details?id=org.amnezia.awg) |
| iOS | [AmneziaWG](https://apps.apple.com/app/amneziawg/id6478942365) |
| Windows / macOS / Linux | [AmneziaVPN](https://amnezia.org/) (import the `.conf`), or `amneziawg-tools` |

Import is the same as WireGuard: QR, file, or pasted text.

## OpenVPN

| Platform | App |
|----------|-----|
| Android / iOS | OpenVPN Connect |
| Windows / macOS | OpenVPN Connect, or the OpenVPN GUI / Tunnelblick |
| Linux | `openvpn --config customer.ovpn` |

The `.ovpn` the panel produces is self-contained — CA, `tls-crypt` key, and the server address are inside — so it imports with one tap. The customer signs in with the **username and password** shown in Client information; there is no per-client certificate to install.

## Subscription links

The subscription link opens in a browser as a page with every device and every host, each with its config, download and QR ([Subscription page](/panel/subscription)). Apps that accept a subscription URL for WireGuard configs re-fetch it on the interval you set in Settings → Subscription.

## When a customer says "it does not connect"

[Troubleshooting → A customer cannot connect](/help/troubleshooting#a-customer-cannot-connect).
