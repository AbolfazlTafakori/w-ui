---
description: "Frequently asked questions: licence, supported systems, protocols, client apps, and how W-UI compares with Xray panels such as 3x-ui and Pasarguard."
---

# FAQ

## Is W-UI free and open source?

Yes, under [AGPL-3.0](https://github.com/AbolfazlTafakori/w-ui/blob/main/LICENSE). Use it, change it, and sell access through it. If you run a modified version as a service, offer your users its source.

## What does it run on?

Ubuntu 22.04, 24.04 and 26.04, Debian 12 and 13, AlmaLinux 9, Rocky Linux 9 and Fedora 41, on x86-64 and arm64. Each is installed clean in CI on every release. Other systemd distributions generally work, and the installer says what it could not do.

## Which protocols does it serve?

WireGuard, AmneziaWG (an obfuscated WireGuard), OpenVPN, OpenConnect (the Cisco AnyConnect protocol), IKEv2, L2TP/IPsec and PPTP. The last four need no app on most devices, since phones, computers and routers have them built in. Through a 3x-ui or Pasarguard [node](/panel/nodes), a customer's one subscription can also carry VLESS, VMess, Trojan, Shadowsocks and the other Xray protocols.

## How is it different from an Xray panel?

An Xray panel manages Xray proxies. W-UI manages VPN tunnels, with pages, menus and a script laid out the way operators of those panels already know. Its other difference is enforcement: data limits are nftables quota objects the kernel applies, not a counter polled every few seconds. See [How it works](/reference/how-it-works).

It does not replace an Xray panel; it can sit over one. Add a 3x-ui or Pasarguard panel as a node, bring in its users, and sell one plan across your tunnels and its inbounds.

## Can several people run one panel?

Yes. There is one owner, any number of panel administrators who look after every customer, and [resellers](/panel/operators) who sell within a ceiling of customers, traffic and time that you set.

## Which client apps work?

The WireGuard and AmneziaWG apps, OpenVPN Connect, Cisco Secure Client or OpenConnect, and the VPN built into Android, iPhone, Mac, Windows and most routers for IKEv2, L2TP/IPsec and PPTP. See [Client apps](/guide/client-apps).

## Can customers see how much they have used?

Yes. Their subscription link opens a page with their usage, remaining data, expiry and every configuration, with setup steps for their own device. See [Subscription page](/panel/subscription).

## Do I need a domain?

No. The installer gets a Let's Encrypt certificate for the server's own address, valid six days and renewed automatically. A domain is nicer to hand out, but not required.

## Does restarting the panel disconnect customers?

No. Tunnels are kernel objects, and the panel only restarts an interface whose configuration actually changed. Updates, `systemctl restart wui` and `w-ui` → 15 all keep customers connected.

## How many customers can one server carry?

The limit is the link and the CPU, not the panel. Enforcement costs a flat amount per customer in nftables, and the reconciler's two-second tick is a few SQL statements. Thousands of customers on one small VPS is ordinary.

## Where do I get help?

Start with [Troubleshooting](/help/troubleshooting). Then open a [GitHub issue](https://github.com/AbolfazlTafakori/w-ui/issues) with `wui version`, the OS and the relevant log lines.
