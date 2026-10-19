---
description: "Why a quota enforced by the kernel overshoots by one packet where a polled counter overshoots by megabytes, how the reconciler keeps kernel state equal to the database, and what runs each protocol."
---

# How enforcement works

## The difference from a polling panel

Most panels poll a byte counter every couple of seconds and disable the customer once the number crosses their quota. Between two polls the customer keeps transferring at full speed. On a 100 Mbit link that is roughly 25 MB of overshoot; on a gigabit link, 250 MB. If you sell a 1 GB plan, a fast customer silently gets 1.25 GB.

W-UI programs the limit into the kernel itself, as an `nftables` quota object attached to the customer's address:

```
quota q_c14 { over 1073741824 bytes used 402653184 bytes }
```

The kernel drops the packet that crosses the ceiling. The panel is not in the data path, so the overshoot is one packet rather than one polling interval, and it stays that way whether the customer has a 10 Mbit or a 10 Gbit link.

The panel still polls, but only to *read* usage for the interface and to decide when to take a device off its tunnel, never to enforce the byte limit.

Every protocol is billed the same way. Each device, whatever it connects with, comes up on its own address inside the tunnel, and the kernel counts its traffic by that address.

## The reconciler

The database is the source of truth. Kernel state is derived from it and rebuilt to match, rather than changed piece by piece as events arrive. Every two seconds:

1. **Collect:** read and zero the nftables counters atomically, then write usage to the database through a single serialised writer.
1. **Evaluate:** decide in SQL which customers are now over quota or past expiry.
1. **Apply:** push the desired set of devices and the desired ruleset to the kernel and the tunnels.

So the panel heals itself. If the machine reboots, someone flushes nftables by hand, or the panel is killed mid-write, the next tick rebuilds everything from the database. And a panel restart drops nobody: the drivers never delete or recreate a link or a device that is already correct.

## What runs each protocol

| Protocol | Run by |
|----------|--------|
| WireGuard | The kernel's own `wireguard` module, driven by the panel. |
| AmneziaWG | The `amneziawg` kernel module, or the userspace `amneziawg-go` when the module cannot be built on this kernel. |
| OpenVPN | `openvpn`, one process per interface. |
| L2TP/IPsec | strongSwan for IPsec, xl2tpd and pppd for L2TP. |
| IKEv2 | strongSwan, the same instance an L2TP tunnel on the server uses. |
| OpenConnect | ocserv. |
| PPTP | W-UI's own PPTP server (`wui pptpd`), with pppd for each call. |

The panel runs as an unprivileged user and cannot start strongSwan, ocserv or pppd. For the last four protocols it describes the tunnel in a file in its data directory, and a root agent (`wui l2tp-agent`, run by `wui-l2tp.service`) carries it out. Systemd starts the agent the moment such a tunnel exists, and it stops on its own when none is left, so nothing runs and no port is held on a server without one. The agent treats everything it reads from the panel's directory as untrusted and checks it in full before any of it reaches a file. It reports back who is connected, and ends the sessions the panel asks it to end.

## Design notes

- **Peers are diffed, never replaced.** WireGuard keeps session state per peer. Replacing the whole list on every tick tears down live handshakes, and the tunnel then looks connected from both ends while moving no traffic. The driver sends only what changed.
- **Limits cover traffic to the panel's own host, not just through it.** Packets that end on this machine never reach the forward hook. Without an input rule, a resolver running here would carry customer data for free, and a cut-off customer could still reach every service on the box.
- **The quota drop comes before the counter.** Otherwise dropped bytes would be billed to a customer who never received them.
- **Speed limits classify by mark, not by filter.** The nftables chain that already exists per customer stamps the packet with its HTB class, so the cost of classifying stays flat as customers are added.
- **Egress is policy routing.** Each outbound is a marked routing table. The default outbound's mark goes on everything unmatched, DNS included, and when that outbound is down, customers' traffic is dropped rather than leaked from the server's own address.

## What needs the kernel

`nft_quota` for exact limits, HTB (`sch_htb`) for speed limits, and `wireguard` for kernel WireGuard. AmneziaWG falls back to a userspace implementation when its module cannot be built. The installer tests each and says what it found, and the **Enforcement** line on the Overview page says which mode you are in.

## Sharing a server with other projects

W-UI is written to be a guest on a machine that may already run other things: a website behind nginx, Docker, another VPN, a database. What it touches is its own, and it puts its own back when something else removes it.

| | What W-UI does | What it leaves alone |
|---|---|---|
| **Firewall** | Its own nftables tables only: `inet wui` (limits), `inet wui_policy` (routing) and `ip wui_nat` (the tunnels' address translation). In ufw or firewalld, when one is on, it opens the tunnels' ports and closes only what it opened. | Every other table and chain; it never flushes the ruleset. The installer opens ports only in a firewall that is already on, and never turns one on. |
| **Routing** | Marks in `0xa7xxxx`, tables from 47000, rules at priority 20000 and up. | Any rule whose mark *or* table is not in its range; such a rule is never removed or flushed. |
| **Speed limits** | A queue on its own tunnel devices. | The server's network card, and any device whose queue something else set up. |
| **Tunnel ranges** | A new tunnel's subnet is refused if it overlaps a network already on the machine, such as Docker's bridge, another VPN or the server's own, and the refusal names the device. | |
| **Ports** | A random free port for the panel and the subscription service. A tunnel is refused a port something already uses. | Ports other programs hold, port 80 included. An automatic install that finds port 80 taken skips the certificate rather than asking for it. |
| **DNS** | Answers only on the tunnels' own gateway addresses. | Port 53 on every other address, `systemd-resolved` included. |
| **Daemons** | strongSwan, xl2tpd, ocserv and pppd are installed and left off; the agent runs them from its own files. | A strongSwan, xl2tpd or pptpd you already run; a tunnel that needs its port is refused, with the reason. |
| **PostgreSQL** | A role and a database called `wui`, and two lines for them first in `pg_hba.conf`. | Every other database, role and line; other projects' logins are not changed. |
| **Certificates** | Its own certificates in acme.sh, from Let's Encrypt, named on each request. | acme.sh's default authority, and other projects' certificates. A clean-up after a failed request cannot reach beyond the panel's own. |
| **Kernel** | IPv4 and IPv6 forwarding, in `/etc/sysctl.d/99-wui.conf`. | Every other setting. |
| **The service** | Runs as its own unprivileged user, with the rest of the system read-only to it. | |

It works the other way round too. When something else on the server removes what the panel set up, such as a firewall reload whose configuration begins with `flush ruleset` (Debian's default `/etc/nftables.conf` does, so a plain `systemctl restart nftables` is enough), or another VPN resetting the routing rules, the panel notices within two seconds and puts its own back, logging `removed by something else on this server`. Without that, every limit, every switched-off customer and every route out of the tunnels would have stopped working with nothing said.
