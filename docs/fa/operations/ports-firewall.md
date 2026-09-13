---
description: "W-UI کدام پورت‌ها را استفاده می‌کند و قوانین آمادهٔ ufw و nftables برای باز کردن فقط همان‌ها."
---

# پورت‌ها و فایروال

فقط آنچه استفاده می‌کنی را باز کن.

## پورت‌ها

| پورت | پروتکل | برای |
|------|--------|------|
| `22` | TCP | SSH — باز نگه دار |
| *پورت پنل* | TCP | پنل؛ موقع نصب رندوم، `w-ui settings` چاپش می‌کند |
| *به ازای اینترفیس* | UDP | پورت هر اینترفیس WireGuard / AmneziaWG |
| `443` یا *انتخابی* | TCP یا UDP | OpenVPN، به ازای اینترفیس |
| `80` | TCP | چالش ACME، موقع صدور و هر تمدید |
| *پورت سابسکریپشن* | TCP | فقط وقتی سرویس سابسکریپشن لیسنر جدا دارد |

## ufw

```bash
ufw allow 22/tcp
ufw allow 41234/tcp          # پورت پنل
ufw allow 51820/udp          # هر اینترفیس وایرگارد
ufw allow 443/tcp            # OpenVPN روی TCP، اگر هست
ufw allow 80/tcp             # تمدید سرتیفیکیت
ufw --force enable
```

`w-ui` → **23. Firewall Management** همین را از منو انجام می‌دهد و می‌تواند پورت تانل‌ها را از اینترفیس‌های روشن بخواند.

::: warning
قبل از فعال کردن سیاست default-deny، SSH را اجازه بده، و از یک نشست دوم تست کن تا خودت را بیرون نیندازی.
:::

## nftables

```nft [/etc/nftables.conf]
table inet filter {
  chain input {
    type filter hook input priority 0; policy drop;
    ct state established,related accept
    iif lo accept
    tcp dport { 22, 80, 41234 } accept
    udp dport { 51820 } accept
    icmp type echo-request accept
  }
}
```

پنل جدول‌های خودش (`inet wui`، `inet wui_policy`) را کنار جدول تو نگه می‌دارد و هرگز به `filter` دست نمی‌زند.

## فایروال ابری

هتزنر، دیجیتال‌اوشن، AWS و بقیه *قبل* از رسیدن بسته به سرور فیلتر می‌کنند. همان پورت‌ها را آن‌جا هم باز کن؛ نصاب نمی‌تواند این را برایت بکند و همین را می‌گوید.
