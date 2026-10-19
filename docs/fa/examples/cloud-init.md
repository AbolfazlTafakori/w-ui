---
description: "نصب بدون ترمینال، از cloud-init، یک اسکریپت آماده‌سازی یا ابزار مدیریت چند سرور، و برداشتن نتیجه از یک فایل."
---

# نصب بدون سؤال با cloud-init

برای هر سؤالی که نصب‌کننده می‌پرسد یک پرچم یا متغیر هست، و `-y` هیچ سؤالی نمی‌پرسد. نصب بدون سؤال همه‌ی پروتکل‌ها را نصب می‌کند.

```yaml [cloud-config]
#cloud-config
runcmd:
  - >
    WUI_SSL_MODE=ip WUI_ADMIN_USER=ops
    bash -c "bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh) --port 2053 -y"
```

وقتی نصب تمام شد، هر چه مدیر لازم دارد در `/etc/wui/install-result.env` است، با مجوز 600:

```bash
. /etc/wui/install-result.env
echo "$WUI_ACCESS_URL"   # https://203.0.113.9:2053/aBcDeFgHiJkLmNoPqR/
echo "$WUI_API_TOKEN"    # برای اولین درخواست API
```

متغیرها: `WUI_DOMAIN`، `WUI_SERVER_IP`، `WUI_SSL_MODE=ip|domain|none`، `WUI_ADMIN_USER`، `WUI_ADMIN_PASSWORD`، `WUI_ENABLE_FAIL2BAN=false`. پرچم‌ها در [نصب ← بدون سؤال](/fa/guide/install#نصب-بدون-سؤال) آمده‌اند.

اولین درخواست API، برای اینکه مطمئن شوید پنل بالاست:

```bash
curl -s -H "Authorization: Bearer $WUI_API_TOKEN" "$WUI_ACCESS_URL"api/system | head -c 200
```
