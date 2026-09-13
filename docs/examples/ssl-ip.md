---
description: "HTTPS on the server's own address with no domain at all : Let's Encrypt's six-day certificate, renewed every six hours."
---

# A certificate for the IP, no domain

Let's Encrypt issues certificates for IP addresses under its *short-lived* profile: valid about six days, meant to be renewed automatically.

**At install** it is the default — press enter on the certificate question.

**Later:**

```bash
w-ui          # → 20 → 6
```

Confirm the detected address, optionally add an IPv6, and give a port for the HTTP-01 listener (80 unless something holds it; then forward external 80 to the one you name).

**Renewal:** `wui-cert-renew.timer` runs acme.sh every six hours; acme.sh renews when the certificate is six days old. The panel re-reads the files when they change. Check:

```bash
systemctl list-timers wui-cert-renew.timer
openssl s_client -connect 203.0.113.9:41234 </dev/null 2>/dev/null | openssl x509 -noout -dates
```

::: tip
Port 80 has to stay reachable — every renewal answers a challenge on it.
:::
