---
description: "The panel does not open, a customer cannot connect or connects without internet, limits are not enforced, renewal fails, an uninstall by mistake: what to check, in order."
---

# Troubleshooting

::: tip Every message, explained
The [Errors reference](/reference/errors) lists every message the panel, the installer, `update.sh` and the `w-ui` menu can show, with what each means and what to do.
:::

Start with the log:

```bash
journalctl -u wui -n 200 --no-pager
```

Then look at the panel's own view of itself: `w-ui settings`, **System** on the Overview page, and *Settings → Engine* for whether limits are applied.

## The panel does not open

- **Which address?** `w-ui settings` prints the *Access URL*. The port and path may differ from what you remember; the values on the settings page win over the environment.
- **Certificate warning on the IP address.** A six-day certificate is issued for the address itself. If the browser objects, it has expired and renewal is failing. Run `systemctl status wui-cert-renew.timer`, then `w-ui` → 20 → 3 to renew by hand and read the error.
- **Behind a proxy, 400 or 502.** The panel serves HTTPS. Proxy to `https://127.0.0.1:PORT` with `proxy_ssl_server_name on`, or install with `--no-tls` and terminate TLS in the proxy. See [Reverse proxy](/operations/reverse-proxy).

## A customer cannot connect

- **Is the interface up?** Check the Enabled switch on the Interfaces page, and `wg show` or `awg show` on the server.
- **Is the port open?** Check the provider's firewall, then `w-ui` → 23. L2TP, PPTP and IKEv2 need more than one port, and PPTP needs GRE; see [Ports and firewall](/operations/ports-firewall).
- **Is the customer active?** A customer in any other state has their devices taken off the tunnels. Raise the quota or extend the expiry.
- **The right endpoint?** The interface's **Endpoint** is what goes into configurations, and a [host](/panel/hosts) overrides it for WireGuard and OpenVPN.
- **The wrong app for the mode.** An AmneziaWG interface needs the AmneziaWG app, and a plain WireGuard interface the WireGuard app. The mode is set per interface.
- **Held off?** A file used on more devices at once than the plan's **Users** is held off for two minutes. The log says `connection limit reached; device held off`.
- **Two L2TP devices behind one address.** L2TP/IPsec cannot carry two devices that reach the server from the same public address at once; only one of them works. Give those customers IKEv2, WireGuard or OpenConnect. See [Interfaces](/panel/interfaces#l2tp-ipsec-two-devices-behind-one-address).

## The customer connects but has no internet

- **Forwarding.** `sysctl net.ipv4.ip_forward` must be `1`; the installer sets it.
- **Egress interface.** The interface's **Egress interface** must be the public network card; `ip route get 1.1.1.1` shows which it is.
- **Default outbound down.** With *Block traffic while the default outbound is down* on, customers' traffic is dropped while the hop at the top of Outbounds is unreachable. Check the hop, or move `direct` to the top.
- **DNS.** Customers resolve through the tunnel gateway; Engine → DNS shows the upstream servers.
- **PPTP connects and carries nothing.** GRE is blocked on the way, often by the provider's firewall.

## Limits are not enforced

A yellow notice at the top of every page, and **Data limits** under *Settings → Engine*, say when quota enforcement is not running here. With no `nft_quota` in the kernel, limits are applied by the panel one tick late instead of by the kernel at the byte. On a stock kernel, run `modprobe nft_quota`; on a custom or container kernel, load or build the module. *Engine → Generated* shows the program the kernel is asked to run.

## Something else took port 80 and renewal fails

`w-ui` → 20 → 1 or 6 asks for another port for the ACME listener; forward external port 80 to it. Or issue the certificate through the web server already there, with `acme.sh --webroot`.

## I uninstalled by mistake

`/root/wui-last-copy-<date>.tar.gz` holds the database and keys. Reinstall, then restore it like any backup: Overview → **Backup & Restore** → **Choose a file**, or `w-ui backup` → 2.

Only if the panel could not make its own archive at uninstall time is that file a plain copy of `/var/lib/wui` and `/etc/wui`. In that case, stop the panel, extract the file over `/`, run `chown -R wui:wui /var/lib/wui`, and start the panel.

## Getting help

Open an issue with `wui version`, the OS, the kernel, the protocol, and the relevant lines from `journalctl -u wui`.
