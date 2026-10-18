---
description: "Which app a customer needs for each kind of tunnel on every platform, from WireGuard to IKEv2 and the proxy apps, and how to get a configuration into it."
---

# Client apps

What a customer installs for each kind of tunnel, and how they load what you give them. The customer's subscription page has step-by-step device guides for 78 phones, routers and modems, chosen from two lists: the device, then the connection it can make. This page is the overview.

## WireGuard

| Platform | App | Import |
|----------|-----|--------|
| Android | [WireGuard](https://play.google.com/store/apps/details?id=com.wireguard.android) | **+** → *Scan from QR code*, or *Import from file* with the `.conf` |
| iOS | [WireGuard](https://apps.apple.com/app/wireguard/id1441195209) | **+** → *Create from QR code*, or *Create from file or archive* |
| Windows | [WireGuard for Windows](https://www.wireguard.com/install/) | *Import tunnel(s) from file*, or *Add empty tunnel* and paste the text |
| macOS | WireGuard from the App Store | *Import tunnel(s) from file* |
| Linux | `wireguard-tools` | `wg-quick up ./customer.conf` |

The file, the text to copy and the QR code carry the same thing: the customer's private key, their address, and your interface's public key and endpoint. Give out **one per device**, since each device has its own key.

## AmneziaWG

A WireGuard interface in *amnezia* mode needs the AmneziaWG app, not the plain WireGuard one. The obfuscation parameters are in the file, and the plain app does not understand them.

| Platform | App |
|----------|-----|
| Android | [AmneziaWG](https://play.google.com/store/apps/details?id=org.amnezia.awg) |
| iOS | [AmneziaWG](https://apps.apple.com/app/amneziawg/id6478942365) |
| Windows / macOS / Linux | [AmneziaVPN](https://amnezia.org/), importing the `.conf`, or `amneziawg-tools` |

Import works as for WireGuard: a QR code, a file, or pasted text. Every AmneziaWG file is also offered as an **AmneziaVPN key**, a link or QR code in the form the AmneziaVPN app imports, on the customer's page and in the panel.

## OpenVPN

| Platform | App |
|----------|-----|
| Android / iOS | OpenVPN Connect |
| Windows / macOS | OpenVPN Connect, or OpenVPN GUI / Tunnelblick |
| Linux | `openvpn --config customer.ovpn` |

On a phone, the customer downloads the file from the subscription page, with the ⤓ beside the device. It arrives as `device-1.ovpn`, not as a `.txt`, and opens straight in OpenVPN Connect.

The `.ovpn` the panel makes is complete: the CA, the `tls-crypt` key and the server's address are inside, so it imports with one tap. The device's **username and password** are inside too, so it connects without a prompt. A client too old to read that part of the file (OpenVPN before 2.5) asks for them, and the same pair is what to type. There is no certificate per customer to install.

## OpenConnect

OpenConnect is what Cisco Secure Client (AnyConnect) and the `openconnect` client dial: a VPN inside TLS, on port 443 by default, with DTLS over UDP when it gets through.

| Platform | App |
|----------|-----|
| Windows / macOS / iPhone / Android / ChromeOS | Cisco Secure Client |
| Linux | NetworkManager's OpenConnect plugin, or `openconnect` |
| Routers | OpenWrt's OpenConnect package |

The customer enters the server's address and their sign-in. The sign-in is masked on the subscription page; each copy button fetches its value only when pressed.

## IKEv2

IKEv2 is the VPN built into Android 12 and later, iPhone, Mac and Windows, so no app is needed there. The tunnel offers all four of Android's sign-in types: username and password, a pre-shared key, a certificate, and a certificate in EAP.

The subscription page gives each device its steps, the server's certificate and, for the certificate types, the device's own `.p12` file. Where the standard UDP ports 500 and 4500 are blocked, the strongSwan app, Linux and routers can use a port of the tunnel's own if you give it one; the built-in clients cannot be given another port.

## L2TP/IPsec

L2TP/IPsec is built into Windows, macOS, iPhone and many routers. The customer enters the server's address, their username and password, and the tunnel's pre-shared key. All three are masked on the subscription page and copied with a button, which fetches the value only when pressed and logs it.

## PPTP

PPTP is for routers and modems that have nothing else. It needs MS-CHAPv2 with 128-bit MPPE and refuses a device without encryption. It is weak by today's standards, and the panel says so wherever it is offered; give it only to a device that can use nothing better.

## Proxy apps and subscriptions

v2rayNG, v2rayN, Hiddify, NekoBox, Streisand and similar apps take the **subscription link** itself. Add it as a subscription in the app, and it reads the list of links the panel serves for it: the proxy inbounds of 3x-ui and Pasarguard nodes, and each WireGuard file as a `wireguard://` link. A file that has no link form, such as an OpenVPN profile, stays on the subscription page.

## Subscription links

Opened in a browser, the subscription link is a page with every device and every host, each with its configuration, a download and a QR code ([Subscription page](/panel/subscription)). Apps that accept a subscription link fetch it again at the interval set in Settings → Subscription, so a change you make later reaches them without a new file.

## When a customer says "it does not connect"

See [Troubleshooting → A customer cannot connect](/help/troubleshooting#a-customer-cannot-connect).
