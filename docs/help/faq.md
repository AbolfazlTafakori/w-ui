---
description: "Frequently asked questions : licence, supported systems, client apps, how it compares with Xray panels and Marzban."
---

# FAQ

## Is W-UI free and open source?

Yes — [AGPL-3.0](https://github.com/AbolfazlTafakori/w-ui/blob/main/LICENSE). Use it, change it, sell access through it; if you run a modified version as a service, offer your users its source.

## What does it run on?

Ubuntu 22.04 / 24.04, Debian 12 / 13, AlmaLinux 9, Rocky 9, Fedora 41, on x86-64 and arm64 — each one installed clean in CI on every release. Other systemd distributions generally work; the installer says what it could not do.

## How is it different from an Xray panel?

An Xray panel manages Xray — VLESS, VMess, Trojan, REALITY. W-UI manages **WireGuard, AmneziaWG and OpenVPN**, with the same pages, menus and script so you do not relearn anything. Its other difference is enforcement: data limits are nftables quota objects the kernel applies, not a counter polled every few seconds. [How enforcement works](/reference/how-it-works).

## And from Marzban?

Marzban is also an Xray panel with a Python backend and a multi-admin model. W-UI is a single Go binary with no runtime dependencies, one administrator, and WireGuard / OpenVPN as its protocols.

## Which client apps work?

The official WireGuard app on every platform, AmneziaWG for obfuscated interfaces, OpenVPN Connect for OpenVPN. [Client apps](/guide/client-apps).

## Can customers see how much they have used?

Yes — their subscription link opens a page with usage, remaining data, expiry and every config. [Subscription page](/panel/subscription).

## Do I need a domain?

No. The installer gets a Let's Encrypt certificate for the server's own address (six days, renewed automatically). A domain is nicer to hand out, not required.

## Does restarting the panel disconnect customers?

No. Tunnels are kernel objects; the panel only restarts an interface whose configuration actually changed. Upgrades, `systemctl restart wui`, and `w-ui` → 15 all keep customers connected.

## How many customers can one server carry?

The limit is the link and the CPU, not the panel: enforcement costs a flat amount per customer in nftables, and the reconciler's two-second tick is a few SQL statements. Thousands of customers on one small VPS is ordinary.

## Where do I get help?

[Troubleshooting](/help/troubleshooting) first; then a [GitHub issue](https://github.com/AbolfazlTafakori/w-ui/issues) with `wui version`, the OS, and the relevant log lines.
