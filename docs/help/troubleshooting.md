---
description: "The panel does not open, a customer cannot connect, connects without internet, limits not enforced, renewal fails, an uninstall by mistake — what to check, in order."
---

# Troubleshooting

::: tip Every message, explained
The [Errors reference](/reference/errors) lists every message the panel, the installer, `update.sh` and the `w-ui` menu can show, with what each means and what to do.
:::
Start with the log:

```bash
journalctl -u wui -n 200 --no-pager
```

and the panel's own view of itself: `w-ui settings`, and **System** on the Overview page.

## The panel does not open

- **Which address?** `w-ui settings` prints the *Access URL*. The port and path may differ from what you remember — the settings page's values win over the environment.
- **Certificate warning on the IP address.** A six-day certificate is issued for the address itself; if the browser objects, it has expired and renewal is failing: `systemctl status wui-cert-renew.timer`, then `w-ui` → 20 → 3 to renew by hand and read the error.
- **Behind a proxy, 400 or 502.** The panel is on HTTPS; proxy to `https://127.0.0.1:PORT` with `proxy_ssl_server_name on`, or give it `--no-tls` and terminate TLS in the proxy.

## A customer cannot connect

- **Is the interface up?** Interfaces page, the Enabled switch; `wg show` / `awg show` on the server.
- **Is the port open?** The provider's firewall, then `w-ui` → 23.
- **Is the customer active?** Anything but `active` has its peers removed. Raise the quota or extend the expiry.
- **Right endpoint?** The interface's *Endpoint host* is what goes into configs; a host overrides it.
- **AmneziaWG client, plain WireGuard interface** (or the reverse). The mode is per interface; the app must match.

## The customer connects but has no internet

- **Forwarding** — `sysctl net.ipv4.ip_forward` must be `1`; the installer sets it.
- **NAT interface** — the interface's *NAT interface* must be the public NIC (`ip route get 1.1.1.1` shows it).
- **Default outbound down** — with fail-closed on, customers are dropped while the hop at the top of Outbounds is unreachable. Check it, or move `direct` to the top.
- **DNS** — customers resolve through the tunnel gateway; Engine → DNS shows the upstreams.

## Limits are not enforced

Overview → **Enforcement** says *Reduced* when the kernel has no `nft_quota`. On a custom or container kernel, load or build the module; on a stock one, `modprobe nft_quota`.

## Something else took port 80 and renewal fails

`w-ui` → 20 → 1 or 6 asks for another port for the ACME listener; forward external 80 to it. Or issue through the web server already there with `acme.sh --webroot`.

## I uninstalled by mistake

`/root/wui-last-copy-<date>.tar.gz` holds the database and keys. Reinstall, stop the panel, extract the archive over `/`, fix ownership (`chown -R wui:wui /var/lib/wui`), start.

## Getting help

Open an issue with `wui version`, the OS, the kernel, the protocol, and the relevant lines from `journalctl -u wui`.
