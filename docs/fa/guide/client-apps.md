---
description: "مشتری برای WireGuard، AmneziaWG و OpenVPN روی هر پلتفرم چه اپی لازم دارد و چطور کانفیگ را واردش می‌کند."
---

# اپ‌های کلاینت

مشتری چه چیزی نصب می‌کند، به ازای هر نوع تانل، و چیزی را که به او می‌دهی چطور بارگذاری می‌کند.

## WireGuard

| پلتفرم | اپ | وارد کردن |
|--------|----|-----------|
| اندروید | [WireGuard](https://play.google.com/store/apps/details?id=com.wireguard.android) | **+** → *Scan from QR code*، یا *Import from file* با `.conf` |
| iOS | [WireGuard](https://apps.apple.com/app/wireguard/id1441195209) | **+** → *Create from QR code*، یا *Create from file or archive* |
| ویندوز | [WireGuard for Windows](https://www.wireguard.com/install/) | *Import tunnel(s) from file*، یا *Add empty tunnel* و چسباندن متن |
| macOS | WireGuard از App Store | *Import tunnel(s) from file* |
| لینوکس | `wireguard-tools` | `wg-quick up ./customer.conf` |

فایل کانفیگ، متن قابل کپی و QR همه یک چیز را دارند: کلید خصوصی مشتری، آدرسش، و کلید عمومی و endpoint اینترفیس تو. **برای هر دستگاه یکی** بده — هر دستگاه کلید خودش را دارد.

## AmneziaWG

اینترفیس وایرگارد در حالت *amnezia* به کلاینت AmneziaWG نیاز دارد نه نسخهٔ ساده؛ پارامترهای ابهام داخل کانفیگ‌اند و اپ ساده آن‌ها را نمی‌فهمد.

| پلتفرم | اپ |
|--------|----|
| اندروید | [AmneziaWG](https://play.google.com/store/apps/details?id=org.amnezia.awg) |
| iOS | [AmneziaWG](https://apps.apple.com/app/amneziawg/id6478942365) |
| ویندوز / macOS / لینوکس | [AmneziaVPN](https://amnezia.org/) (وارد کردن `.conf`)، یا `amneziawg-tools` |

وارد کردن مثل وایرگارد است: QR، فایل، یا متن چسبانده.

## OpenVPN

| پلتفرم | اپ |
|--------|----|
| اندروید / iOS | OpenVPN Connect |
| ویندوز / macOS | OpenVPN Connect، یا OpenVPN GUI / Tunnelblick |
| لینوکس | `openvpn --config customer.ovpn` |

فایل `.ovpn` که پنل می‌سازد خودکفاست — CA، کلید `tls-crypt` و آدرس سرور داخلش هستند — پس با یک ضربه وارد می‌شود. **نام کاربری و رمز** دستگاه داخل خود فایل است، پس بدون پرسیدن چیزی وصل می‌شود؛ همان زوج را می‌شود در کلاینت‌های قدیمی (OpenVPN قبل از 2.5) دستی تایپ کرد. سرتیفیکیت جداگانه‌ای برای نصب نیست.

## لینک سابسکریپشن

لینک سابسکریپشن در مرورگر صفحه‌ای با همهٔ دستگاه‌ها و هاست‌ها باز می‌کند، هر کدام با کانفیگ، دانلود و QR ([صفحهٔ سابسکریپشن](/fa/panel/subscription)). اپ‌هایی که URL سابسکریپشن وایرگارد می‌پذیرند با فاصله‌ای که در تنظیمات → سابسکریپشن گذاشته‌ای دوباره می‌گیرند.

## وقتی مشتری می‌گوید «وصل نمی‌شود»

[عیب‌یابی → مشتری وصل نمی‌شود](/fa/help/troubleshooting#مشتری-وصل-نمی‌شود).
