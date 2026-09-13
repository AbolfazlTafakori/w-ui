---
description: "Install without a terminal : from cloud-init, a provisioning script, or a fleet tool : and pick the result up from a file."
---

# Unattended install with cloud-init

Every question has a flag or a variable; `-y` asks nothing.

```yaml [cloud-config]
#cloud-config
runcmd:
  - >
    WUI_SSL_MODE=ip WUI_ADMIN_USER=ops
    bash -c "bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --port 2053 -y"
```

When it finishes, everything the operator needs is in `/etc/wui/install-result.env` (mode 600):

```bash
. /etc/wui/install-result.env
echo "$WUI_ACCESS_URL"   # https://203.0.113.9:2053/aBcDeFgHiJkLmNoPqR/
echo "$WUI_API_TOKEN"    # for the first API call
```

Variables: `WUI_DOMAIN`, `WUI_SERVER_IP`, `WUI_SSL_MODE=ip|domain|none`, `WUI_ADMIN_USER`, `WUI_ADMIN_PASSWORD`, `WUI_ENABLE_FAIL2BAN=false`. Flags: see [Install → Unattended](/guide/install#unattended-installs).

A first API call, to prove it is up:

```bash
curl -s -H "Authorization: Bearer $WUI_API_TOKEN" "$WUI_ACCESS_URL"api/system | head -c 200
```
