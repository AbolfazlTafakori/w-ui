---
layout: home

hero:
  name: W-UI
  text: WireGuard & OpenVPN, sold by the gigabyte
  tagline: The classic panel layout — the same pages, menus and management script — with data limits the kernel enforces, not a poller.
  image:
    src: /logo.png
    alt: W-UI
  actions:
    - theme: brand
      text: Quick Install
      link: /guide/install
    - theme: alt
      text: Panel Overview
      link: /panel/overview
    - theme: alt
      text: GitHub
      link: https://github.com/AbolfazlTafakori/w-ui

features:
  - icon: 🧱
    title: Limits the kernel enforces
    details: A customer's quota is an nftables quota object on their address. The packet that crosses it is dropped by the kernel; the panel is not in the data path.
    link: /reference/how-it-works
    linkText: How it works
  - icon: 🔀
    title: WireGuard, AmneziaWG, OpenVPN
    details: Several interfaces on one server, obfuscated WireGuard where DPI blocks the plain one, OpenVPN over TCP 443 where nothing else gets through.
    link: /panel/interfaces
    linkText: Interfaces
  - icon: 🧭
    title: The layout you already know
    details: Inbounds, clients, hosts, outbounds, routing, balancers, DNS, the settings tabs, the w-ui menu numbered 0 to 28 — in the same places, doing the same things.
    link: /panel/overview
    linkText: The panel
  - icon: 🔐
    title: HTTPS from the first minute
    details: The installer gets a Let's Encrypt certificate for your domain or for the server's own address, renews it unattended, and the panel picks the renewed one up without a restart.
    link: /guide/certificates
    linkText: Certificates
  - icon: 🤖
    title: Telegram, both ways
    details: Notifications for customers running out and backups landing — and a bot the operator and the customers can ask.
  - icon: 🌍
    title: English and Persian
    details: Every string, both directions, three themes, and a customer-facing subscription page in the customer's language.
---

## Why not just use an existing panel?

::: tip The W-UI Advantage
The difference is *where the limit is enforced*.

Most panels poll a byte counter every couple of seconds and disable the customer once the number crosses their quota. Between two polls the customer keeps transferring at full speed — roughly **25 MB** of overshoot on a 100 Mbit link, **250 MB** on a gigabit one.

W-UI programs the limit into the kernel as an `nftables` quota object; the overshoot is **one packet**, whatever the link speed.

→ [How it works](/reference/how-it-works)
:::

## Install in one line

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

It asks four questions, then does the rest: packages, kernel forwarding, a check that this kernel can hold quota objects, an unprivileged service account, a hardened systemd unit, a certificate, and a `w-ui` command.

::: info What happens next
Once the installer finishes, the panel is running over HTTPS and the `w-ui` command is on your path. [Read what happens next](/guide/after-install).
:::
