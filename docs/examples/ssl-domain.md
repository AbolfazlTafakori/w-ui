---
description: "Point a name at the server and get a Let's Encrypt certificate for it, from the installer or from the menu."
---

# A certificate for a domain

**1.** Add an `A` record for `panel.example.com` pointing at the server, and wait until `dig +short panel.example.com` returns it.

**2.** Port 80 must reach the server (provider firewall too).

**3.** At install, choose **1** and give the name. Or later:

```bash
w-ui          # → 20 → 1 → the domain
```

The menu issues it with acme.sh over HTTP-01, installs it under `/etc/wui/certs/panel.example.com/`, hands it to the panel and restarts. Renewal is automatic.

**4.** Check:

```bash
curl -sI https://panel.example.com:41234/ | head -1
w-ui settings | grep -E 'cert|Access'
```

The IP address still reaches the same port, only with a certificate warning — for both, see [wildcard through Cloudflare](/examples/wildcard-cloudflare) or issue for the IP as well.
