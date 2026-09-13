---
description: "بدون ترمینال نصب کن : از cloud-init، اسکریپت provisioning یا ابزار مدیریت ناوگان : و نتیجه را از یک فایل بردار."
---

# نصب بدون سؤال با cloud-init

هر سؤال یک پرچم یا متغیر دارد؛ `-y` هیچ نمی‌پرسد.

```yaml [cloud-config]
#cloud-config
runcmd:
  - >
    WUI_SSL_MODE=ip WUI_ADMIN_USER=ops
    bash -c "bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --port 2053 -y"
```

وقتی تمام شد، هر چه مدیر لازم دارد در `/etc/wui/install-result.env` (mode 600) است:

```bash
. /etc/wui/install-result.env
echo "$WUI_ACCESS_URL"   # https://203.0.113.9:2053/aBcDeFgHiJkLmNoPqR/
echo "$WUI_API_TOKEN"    # برای اولین درخواست API
```

متغیرها: `WUI_DOMAIN`، `WUI_SERVER_IP`، `WUI_SSL_MODE=ip|domain|none`، `WUI_ADMIN_USER`، `WUI_ADMIN_PASSWORD`، `WUI_ENABLE_FAIL2BAN=false`. پرچم‌ها: [نصب → بدون سؤال](/fa/guide/install#نصب-بدون-سؤال).

اولین درخواست API، برای اثبات اینکه بالاست:

```bash
curl -s -H "Authorization: Bearer $WUI_API_TOKEN" "$WUI_ACCESS_URL"api/system | head -c 200
```
