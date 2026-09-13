---
description: "One certificate for *.example.com through the DNS-01 challenge with a scoped Cloudflare API token : no port 80 needed."
---

# A wildcard certificate through Cloudflare

**1.** In Cloudflare, *My Profile → API Tokens → Create Token → Edit zone DNS*, scoped to the one zone. Copy the token.

**2.**

```bash
w-ui          # → 21
```

Give the domain (`example.com`), choose **t** for a token, paste it. acme.sh asks Let's Encrypt for `example.com` and `*.example.com` over DNS-01 — port 80 is not involved — installs the pair under `/etc/wui/certs/example.com/`, and offers to set it for the panel.

**3.** Any name under the zone now works for the panel, the subscription service, or a host in Hosts.
