---
description: "یک سرتیفیکیت برای *.example.com از طریق چالش DNS-01 با توکن API محدودشدهٔ کلادفلر : بدون نیاز به پورت 80."
---

# Wildcard از طریق کلادفلر

**۱.** در کلادفلر، *My Profile → API Tokens → Create Token → Edit zone DNS*، محدود به همان یک zone. توکن را کپی کن.

**۲.**

```bash
w-ui          # → 21
```

دامنه (`example.com`) را بده، **t** را برای توکن انتخاب کن، بچسبان. acme.sh از Let's Encrypt برای `example.com` و `*.example.com` روی DNS-01 می‌خواهد — پورت 80 درگیر نیست — جفت را زیر `/etc/wui/certs/example.com/` نصب می‌کند و پیشنهاد می‌دهد برای پنل ست شود.

**۳.** حالا هر نامی زیر zone برای پنل، سرویس سابسکریپشن یا یک هاست در هاست‌ها کار می‌کند.
