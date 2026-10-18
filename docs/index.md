---
layout: home

hero:
  name: W-UI
  text: Sell VPN by the gigabyte
  tagline: The classic panel layout, with the same pages, menus and management script. The difference is that the kernel enforces each data limit, not a program reading a counter every few seconds.
  image:
    src: /logo.png
    alt: W-UI
  actions:
    - theme: brand
      text: Quick install
      link: /guide/install
    - theme: alt
      text: The panel
      link: /panel/overview
    - theme: alt
      text: GitHub
      link: https://github.com/AbolfazlTafakori/w-ui

features:
  - icon: 🧱
    title: Limits the kernel enforces
    details: A customer's quota is an nftables quota object on their own address. The packet that crosses it is dropped in the kernel, and the panel is never in the data path.
    link: /reference/how-it-works
    linkText: How it works
  - icon: 🔀
    title: Seven protocols in one panel
    details: WireGuard, and AmneziaWG where DPI blocks plain WireGuard; OpenVPN; L2TP/IPsec, IKEv2 and PPTP for the VPN built into Windows, iPhone, Android and routers; and OpenConnect for the Cisco app. Several tunnels of each on one server.
    link: /panel/interfaces
    linkText: Tunnels
  - icon: 🌐
    title: Other servers, from this panel
    details: Other W-UI servers, and 3x-ui and Pasarguard panels, become nodes of this one. You make a customer once and put them on whichever servers you choose.
    link: /panel/nodes
    linkText: Nodes
  - icon: 🧭
    title: The layout you already know
    details: Interfaces, clients, hosts, outbounds, routing, balancers, DNS, the settings tabs, and the w-ui menu numbered 0 to 28, each where you expect it and doing the same thing.
    link: /panel/overview
    linkText: The panel
  - icon: 🔐
    title: HTTPS from the first minute
    details: The installer gets a Let's Encrypt certificate for your domain, or for the server's own IP address, and renews it on its own. The panel starts using the renewed one without a restart.
    link: /guide/certificates
    linkText: Certificates
  - icon: 🤖
    title: A Telegram bot for you and your customers
    details: It tells you when a customer is running out of data and when a backup lands. Any customer whose Telegram id is on their plan can ask it about their own account.
  - icon: 🌍
    title: English and Persian
    details: Every string in both languages and both directions, three themes (light, dark and pure black), and a subscription page in each customer's own language.
---

## Why another panel?

::: tip What W-UI does differently
The difference is where the limit is enforced.

Most panels read a byte counter every second or two and cut the customer off once the number passes their quota. Between two readings the customer keeps downloading at full speed: about **25 MB** over the quota on a 100 Mbit line, about **250 MB** on a gigabit one.

W-UI hands the limit to the kernel as a quota object in `nftables`. Whatever the line speed, the overshoot is **one packet**.

→ [How it works](/reference/how-it-works)
:::

## Install with one command

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

The installer first asks whether to install manually or automatically. The automatic install asks three questions (the database, a domain, a certificate) and does the rest on its own: the packages and every protocol, kernel forwarding, a check that this server's kernel supports quotas, a service account without root, a hardened systemd unit, the certificate, and the `w-ui` command. At the end it shows the panel's address, the username and the password.

::: info After the install
When it finishes, the panel is reachable over HTTPS and the `w-ui` command works on the server. [What to do next](/guide/after-install)
:::
