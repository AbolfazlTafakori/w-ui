---
layout: home

hero:
  name: W-UI
  text: وایرگارد و OpenVPN، فروش به گیگابایت
  tagline: چیدمان کلاسیک پنل — همان صفحه‌ها، همان منوها، همان اسکریپت مدیریت — با محدودیت حجمی که کرنل اعمال می‌کند، نه یک poller.
  image:
    src: /logo.png
    alt: W-UI
  actions:
    - theme: brand
      text: نصب سریع
      link: /fa/guide/install
    - theme: alt
      text: نمای پنل
      link: /fa/panel/overview
    - theme: alt
      text: گیت‌هاب
      link: https://github.com/AbolfazlTafakori/w-ui

features:
  - icon: 🧱
    title: محدودیتی که کرنل اعمال می‌کند
    details: سهمیهٔ هر مشتری یک quota object در nftables روی آدرس اوست. بسته‌ای که از سقف رد شود همان‌جا در کرنل دور ریخته می‌شود؛ پنل در مسیر داده نیست.
    link: /fa/reference/how-it-works
    linkText: چطور کار می‌کند
  - icon: 🔀
    title: WireGuard، AmneziaWG، OpenVPN
    details: چند اینترفیس روی یک سرور؛ وایرگارد مبهم‌شده جایی که DPI نسخهٔ ساده را می‌بندد؛ OpenVPN روی TCP 443 جایی که هیچ چیز دیگری رد نمی‌شود.
    link: /fa/panel/interfaces
    linkText: اینترفیس‌ها
  - icon: 🧭
    title: همان چیدمانی که بلدی
    details: Inbounds، Clients، Hosts، Outbounds، Routing، Balancers، DNS، تب‌های تنظیمات، منوی w-ui با شماره‌های ۰ تا ۲۸ — همه در همان جا، با همان کار.
    link: /fa/panel/overview
    linkText: پنل
  - icon: 🔐
    title: HTTPS از دقیقهٔ اول
    details: نصاب برای دامنه‌ات یا برای خودِ آی‌پی سرور از Let's Encrypt سرتیفیکیت می‌گیرد، بی‌مراقب تمدید می‌کند، و پنل نسخهٔ تمدیدشده را بدون ریستارت برمی‌دارد.
    link: /fa/guide/certificates
    linkText: سرتیفیکیت‌ها
  - icon: 🤖
    title: تلگرام، دوطرفه
    details: اعلان برای مشتری‌هایی که حجمشان تمام می‌شود و بک‌آپ‌هایی که گرفته می‌شود — و رباتی که مدیر و مشتری‌ها می‌توانند از آن بپرسند.
  - icon: 🌍
    title: فارسی و انگلیسی
    details: تمام متن‌ها، هر دو جهت، سه تم، و صفحهٔ سابسکریپشن مشتری به زبان خودش.
---

## چرا نه یک پنل موجود؟

::: tip برتری W-UI
فرق در *جایی است که محدودیت اعمال می‌شود*.

بیشتر پنل‌ها هر چند ثانیه یک شمارندهٔ بایت را می‌خوانند و وقتی عدد از سهمیه گذشت مشتری را غیرفعال می‌کنند. بین دو خواندن، مشتری با سرعت کامل دانلود می‌کند — حدود **۲۵ مگابایت** اضافه روی خط ۱۰۰ مگابیت، **۲۵۰ مگابایت** روی گیگابیت.

W-UI محدودیت را به‌صورت quota object در `nftables` به کرنل می‌دهد؛ اضافه‌مصرف **یک بسته** است، با هر سرعتی.

← [چطور کار می‌کند](/fa/reference/how-it-works)
:::

## نصب با یک خط

```bash
bash <(curl -Ls https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/install.sh)
```

چهار سؤال می‌پرسد و بقیه را خودش انجام می‌دهد: پکیج‌ها، فورواردینگ کرنل، بررسی اینکه این کرنل quota object را می‌فهمد، یک حساب سرویس بی‌امتیاز، یک unit سخت‌شدهٔ systemd، سرتیفیکیت، و دستور `w-ui`.

::: info بعدش چه می‌شود
وقتی نصاب تمام شد، پنل روی HTTPS بالاست و دستور `w-ui` روی سیستم هست. [بعدش چه می‌شود](/fa/guide/after-install).
:::
