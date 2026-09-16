---
description: "هر پیامی که پنل، نصاب، اسکریپت آپدیت و منوی w-ui می‌توانند نشان دهند، معنی هر کدام، و راه حلش."
---

# خطاها

هر خطای W-UI یک جمله است که می‌گوید چه چیزی غلط است. این صفحه همهٔ آن‌ها را، به تفکیک جایی که می‌بینی‌شان، با معنی و راه حل فهرست می‌کند. پیام‌ها عیناً نقل شده‌اند (به انگلیسی، همان‌طور که پنل نشان می‌دهد)؛ `…` جای مقداری است که عوض می‌شود (نام، عدد، مسیر).

## خطاها کجا ظاهر می‌شوند

| کجا | چطور |
|---|---|
| **پنل** | یک toast قرمز، یا — برای فیلدی که تایپ کرده‌ای — پیام زیر همان ورودی. |
| **API** | JSON: `{"error": "…"}`؛ برای یک ورودی، `{"error": "…", "field": "name"}`. وضعیت `400` ورودی بد، `401` وارد نشده، `403` رد شده، `404` نیست، `413` خیلی بزرگ، `429` قفل شده، `500` تقصیر خود پنل (جزئیات در لاگ است، هرگز در جواب). |
| **لاگ پنل** | `journalctl -u wui`. `level=ERROR` چیزی که شکست خورده؛ `level=WARN` چیزی که دانستنش لازم است؛ هر دو `error=` با علت اصلی را دارند. |
| **نصاب** | `error: …` قرمز، و نصب می‌ایستد. هر چه قبلش بود انجام شده؛ بعدش هیچ. |
| **`update.sh`** | همان؛ آپدیت ناموفق پنل قبلی را در حال اجرا می‌گذارد. |
| **منوی `w-ui`** | `[ERR] …` قرمز. |

خطایی که می‌گوید **internal error** باگ یا سرور خراب است، نه ورودی بد: در `journalctl -u wui -n 50` خطی که با `request failed` شروع می‌شود را بخوان و با همان خط گزارش کن.

## ورود و سشن‌ها

| پیام | معنی | چه کنی |
|---|---|---|
| `incorrect username or password` | یکی از دو تا اشتباه است. عمداً برای هر دو یک جواب داده می‌شود تا نام کاربری به‌تنهایی قابل حدس نباشد. | هر دو را چک کن. ریست: `w-ui` → ۷، یا `wui admin reset --username NAME`. |
| `that code is not right` / `that code is not right. Check your phone's clock is correct and try the next one.` | کد دومرحله‌ای نخواند. هر کد ۳۰ ثانیه اعتبار دارد؛ گوشی‌ای که ساعتش عقب/جلو است کدهایی می‌سازد که سرور رد می‌کند. | منتظر کد بعدی بمان. ساعت گوشی را روی خودکار بگذار. اگر اپ احراز هویت را از دست داده‌ای: `w-ui` → ۷ و به «حذف دومرحله‌ای» **y** بگو. |
| `that password or code is not right` | روشن/خاموش کردن دومرحله‌ای رمز فعلی (و اگر روشن است، کد) را می‌خواهد. | دوباره وارد کن. |
| `too many attempts from this address; try again in … ` (429) | بعد از چند تلاش ناموفق، ورود برای آن آدرس و آن حساب محدود می‌شود. | زمان گفته‌شده صبر کن. اگر تو نبودی، `w-ui` → ۲۲ نشان می‌دهد چه کسی در می‌زند. |
| `your session has ended; sign in again` | توکنی فرستاده نشده، یا کوکیِ توکن نیست — توکنی که از یک مرورگر به مرورگر دیگر کپی شود به‌تنهایی کار نمی‌کند. | در همین مرورگر دوباره وارد شو. |
| `session expired, sign in again` | عمر توکن تمام شده (تنظیمات → امنیت → مدت سشن) یا توکنی نیست که این پنل صادر کرده باشد. | دوباره وارد شو. |
| `you were signed out everywhere; sign in again` | از وقتی این توکن صادر شده، رمز عوض شده یا **خروج از همه‌جا** زده شده. | دوباره وارد شو. |
| `that access token is not valid` | توکن API `wui_…` که باطل شده یا هرگز وجود نداشته. | از صفحهٔ API یکی بساز، یا `wui token issue --name NAME`. |
| `not signed in` | `/api/auth/me` بدون سشن صدا زده شده. | وارد شو. |
| `the current password is incorrect` (403) | تغییر رمز، رمز قبلی را می‌خواهد. | دوباره وارد کن؛ فراموش شده: `w-ui` → ۷. |
| `the new password must be at least 8 characters` |  | رمز بلندتری انتخاب کن. |
| `the username can be at most 64 characters` |  | کوتاه‌ترش کن. |
| `give a new username, a new password, or both` | فرم تغییر خالی ارسال شده. | چیزی که باید عوض شود را پر کن. |

## درخواست‌هایی که پنل نمی‌تواند بخواند

| پیام | معنی | چه کنی |
|---|---|---|
| `the request body could not be read as JSON` | بدنهٔ درخواست JSON معتبر نیست. | از اسکریپت: کوتیشن‌ها را چک کن؛ `Content-Type: application/json` بفرست. از پنل: صفحه را رفرش کن — نسخه‌ها ناهماهنگ‌اند. |
| `the request had no body` | یک POST/PUT بدون بدنه رسیده. | سند را بفرست. |
| `that request is too large` (413) | بدنه‌ها تا ۱ مگابایت‌اند؛ آپلودها (بک‌آپ، پروفایل) سقف بزرگ‌تر خودشان را دارند. | برای بک‌آپ از endpoint آپلود استفاده کن؛ برای بقیه، درخواست غلط است. |
| `this request carried a field this server does not know: …. The panel and its interface are probably different versions` | JSON کلیدی دارد که سرور نمی‌شناسد. | بعد از آپدیت پنل را رفرش کن؛ از اسکریپت، آن فیلد را حذف کن. |
| `unsupported language "…"; available: …` | `/api/i18n/xx` برای زبانی که وجود ندارد. | `en` یا `fa`. |
| `that is not a range this panel keeps. Ask for 5m, 1h, 6h, 24h, 48h or 7d` | تاریخچه برای بازه‌ای درخواست شده که نگه داشته نمی‌شود. | یکی از همان‌ها. |
| `action must be enable, disable or delete` | عمل گروهی با فعل ناشناخته. | یکی از سه تا. |
| `no file was sent` / `that file could not be read, or it is larger than this panel accepts` | آپلود بدون فایل، یا بزرگ‌تر از سقف. | فایل را پیوست کن؛ بک‌آپ بزرگ‌تر از سقف، بک‌آپ W-UI نیست. |

## مشتری‌ها (کلاینت‌ها)

| پیام | معنی | چه کنی |
|---|---|---|
| `name is required` |  | به مشتری نام بده. |
| `choose at least one server for this customer` | هیچ اینترفیسی تیک نخورده. | تانل‌(های) مجاز مشتری را تیک بزن. |
| `Not found: …` | آی‌دی اینترفیسی که وجود ندارد — معمولاً صفحهٔ کهنه بعد از حذف. | رفرش کن؛ تانل موجود را انتخاب کن. |
| `one of those inbounds does not exist` / `choose at least one inbound` | همان، در ساخت گروهی. |  |
| `device limit must be between 1 and 50` | اتصال هم‌زمان. | عددی در همان بازه. |
| `… devices requested; at most … per customer` | سقف فایل دستگاه ۶۴ است. |  |
| `expiry is in the past` | تاریخ انقضا گذشته است. | تاریخ آینده بده، یا خالی بگذار تا منقضی نشود. |
| `unknown reset cycle "…"` |  | `none`، `daily`، `weekly` یا `monthly`. |
| `the duration cannot be negative` | روزهای شروع-با-اولین-اتصال زیر صفر. | صفر یا بیشتر. |
| `this customer already has a device called "…"` | نام دستگاه برای هر مشتری یکتاست. | نام دیگری. |
| `there is no group called "…"` | قانون یا عمل گروهی گروهی را نام برده که نیست. | اول گروه را بساز، یا نام را درست کن. |
| `a group called "…" already exists` / `a group needs a name` / `that name is too long` |  |  |
| `a tag prefix can only contain letters, digits, - and _` / `a tag cannot contain a comma or a space` |  |  |
| `an OpenVPN username and password only apply when the customer is on an OpenVPN tunnel` | برای مشتری‌ای که تانل OpenVPN ندارد نام کاربری/رمز وارد شده. | یک تانل OpenVPN تیک بزن، یا فیلدها را خالی بگذار. |
| `an OpenVPN username is 3 to 48 characters` / `an OpenVPN username can only contain letters, digits, - _ . and @ (found "…")` |  | چیزی مثل `roya` یا `roya.k`. |
| `the username "…" is already used on …` | نام کاربری روی هر تانل OpenVPN یکتاست. | نام دیگری. |
| `an OpenVPN password is 6 to 64 characters` / `an OpenVPN password cannot contain spaces` |  |  |
| `a subscription id is 8 to 64 characters` / `a subscription id can only contain letters, digits, - and _ (found "…")` | شناسهٔ اشتراکی که در تب Credentials تایپ شده. | چیزی مثل `roya-2024-link`، یا خالی بگذار تا ساخته شود. |
| `the subscription id "…" belongs to another customer` | رازِ لینک هر مشتری یکتاست. | شناسهٔ دیگری. |
| `device limit reached` (400) | مشتری همین حالا ۶۴ فایل دستگاه دارد، بیشترین ممکن. | دستگاهی را حذف کن. |
| `address pool exhausted` (400) | زیرشبکهٔ تانل آدرس آزاد ندارد. | زیرشبکهٔ بزرگ‌تر روی اینترفیس، یا اینترفیس دیگر. |
| `no interfaces configured; create one before adding customers` (log) | مشتری قبل از هر تانلی ساخته شده. | اول یک اینترفیس بساز. |

## اینترفیس‌ها (تانل‌ها)

| پیام | معنی | چه کنی |
|---|---|---|
| `a tunnel needs a name` / `name is required` |  |  |
| `a tunnel called "…" already exists on …` / `a tunnel called "…" already exists on this server` | نام‌ها در هر سرور یکتا هستند. | نام دیگری. |
| `a tunnel name is at most 15 characters; "…" is …` / `a tunnel name can only contain letters, digits, - and _ (found "…"); it names a network device, not a host` | نام، نام دستگاه کرنل می‌شود (`wg0`)؛ دامنه این‌جا موقع بالا آمدن شکست می‌خورد. | چیزی مثل `wg0` یا `ir443`؛ دامنه در **Endpoint** می‌رود. |
| `… overlaps …, the subnet of tunnel "…"; every tunnel on a server needs its own range` | دو تانل روی یک رنج به کرنل دو مسیر برای یک آدرس می‌دهد. | رنج متفاوت، مثل `10.67.0.0/16`. |
| `"…" is too small for the … devices on this tunnel` | زیرشبکهٔ جدید دستگاه‌های موجود را جا نمی‌دهد. | رنج بزرگ‌تر. |
| `unknown protocol "…"` |  | `wireguard` یا `openvpn`. |
| `no driver available for "…" on this server` | کرنل یا باینری آن پروتکل این‌جا نیست (`wg`/`awg`/`openvpn`). | نصبش کن: نصاب را دوباره اجرا کن. |
| `listen port … is out of range` |  | ۱ تا ۶۵۵۳۵. |
| `port … is already in use on this server (…).` `Something else is listening there — another VPN, or another program.` `Choose a different port` | پورت UDP/TCP قابل bind نبود. | پورت دیگری، یا آنچه پورت را گرفته متوقف کن (`ss -lunp`). |
| `the panel is not allowed to bind port … (…). ` | زیر ۱۰۲۴ بدون capability. | یونیت نصاب `CAP_NET_BIND_SERVICE` می‌دهد؛ یونیت دستی هم باید بدهد. |
| `subnet "…": …` | CIDR نیست، یا خیلی کوچک است. | مثل `10.9.0.0/24`. |
| `endpoint host is required` / `endpoint host is required; it is what clients dial` | آدرسی که مشتری‌ها به آن وصل می‌شوند. | نام یا آدرس عمومی سرور. |
| `"…" needs a port, as in vpn.example.com:51820` / `"…" has no host part` / `"…" is not an address the panel can read` | endpoint یا host بدشکل. |  |
| `MTU … is out of range (576-9000)` |  |  |
| `AmneziaWG mode applies to WireGuard only` / `unknown mode "…"` |  |  |
| `… needs AmneziaWG, and this kernel has no …` | حالت مبهم‌سازی ماژول `amneziawg` یا `amneziawg-go` می‌خواهد. | نصاب را دوباره اجرا کن (هر دو را نصب می‌کند)؛ یا وایرگارد ساده. |
| `… already exists and belongs to another WireGuard …` / `… already exists and is not a tunnel this panel …` | اینترفیس کرنلی با این نام هست و مال ما نیست. | نام دیگر، یا اینترفیس سرگردان را حذف کن (`ip link del`). |
| `… did not come up within …` / `interface did not start` (log) | درایور نتوانست لینک را بالا بیاورد. | `error=` خط لاگ: معمولاً پورت، ماژول یا مجوز. |
| `… still carries … interface(s); remove them first` | حذف نودی که هنوز تانل دارد. | اول تانل‌ها را حذف کن. |
| `… device(s) still use "…"; remove those clients first` | حذف اینترفیسی که مشتری دارد. | اول جابه‌جا یا حذفشان کن. |

## هاست‌ها و گروه‌های هاست

| پیام | معنی | چه کنی |
|---|---|---|
| `a host has to belong to an interface` / `there is no interface …` |  | تانل را انتخاب کن. |
| `give the host a name so the list can be read later` / `give the host a name; it is what the config is called` |  |  |
| `a host needs the address customers will dial` / `"…" is not a host name or address` / `"…" is not an address` |  | hostname یا IP. |
| `… is not a port number` |  | ۱ تا ۶۵۵۳۵، یا ۰ برای ارث از تانل. |
| `… already has a host called "…"` |  | نام دیگری. |
| `the name is longer than 256 characters` / `the description is longer than 64 characters` |  |  |
| `"…" is not a format; the formats are …` | حذف فرمت کانفیگی که وجود ندارد. | یکی از فرمت‌های فهرست. |

## Outboundها، بالانسرها و مسیریابی

| پیام | معنی | چه کنی |
|---|---|---|
| `an outbound needs a tag; routing rules refer to it by that name` |  |  |
| `a tag can only contain letters, digits, - and _ (found "…")` / `that tag is too long` |  |  |
| `"…" is the name of a built-in outbound` | `direct` و `block` رزرو شده‌اند. | تگ دیگری. |
| `an outbound called "…" already exists` / `an outbound is already called "…"` |  |  |
| `"…" is not an outbound kind this panel serves` |  | نوعی که فرم پیشنهاد می‌دهد. |
| `an outbound of this kind needs an address to reach` / `"…" is not a URL. It should look like https://vpn2.example.com:2096` |  |  |
| `a WireGuard hop needs the upstream peer's public key` / `that is not a WireGuard key` | کلید ۴۴ کاراکتر base64 است. | کلید را از کانفیگ upstream بچسبان. |
| `an OpenVPN outbound needs the client profile (.ovpn)` / `the OpenVPN profile has no remote line` |  | کل پروفایل را بچسبان. |
| `a … outbound needs its outbound object; paste the JSON or a share link` |  |  |
| `not a share link, a WireGuard configuration, an OpenVPN profile or an Xray outbound` / `nothing to import` | جعبهٔ import متن را نشناخت. | یکی از آن‌ها، کامل. |
| `that … link is malformed` / `that vmess link is not base64` / `that vmess link does not carry JSON` / `that ss link has no host and port` / `that is not valid JSON: …` | لینک اشتراکی که parse نمی‌شود. | دوباره از منبعش کپی کن. |
| `"…" links are not something this panel can run` / `protocol "…" is not one this panel can run` | scheme‌ای که پنل موتوری برایش ندارد. |  |
| `an MTU of … is outside the usable range of 576 to 1500` / `a keepalive of … seconds is outside 0 to 65535` / `"…" is not a range; it should look like 0.0.0.0/0` |  |  |
| `"…" does not resolve, so there is nothing to test` / `give a domain or address to test` / `"…" is not a member of …` | دیالوگ تست. |  |
| `could not bring up an outbound hop` (log) / `outbound hop process died; restarting` (log) | پروسهٔ hop شکست خورد. | `error=` می‌گوید: کلید بد، upstream غیرقابل‌دسترس، باینری غایب. |
| `a balancer needs a tag; rules refer to it by that name` / `a balancer called "…" already exists` |  |  |
| `a balancer needs at least one outbound to send traffic to` / `there is no outbound called "…"` / `"…" has no device to balance over; a balancer's members are hops` | اعضا باید hop‌های موجود باشند. |  |
| `"…" is not a strategy; use random or leastPing` |  |  |
| `"…" has no device; the fallback has to be a hop` |  |  |
| `balancer "…" still includes "…"; remove it from the balancer first` / `… routing rule(s) still send traffic to "…"; change or remove them first` | حذف چیزی که هنوز ارجاع دارد. | اول ارجاع‌ها را بردار. |
| `"…" is built in and cannot be removed` |  |  |
| `give the rule a comment so the list can be read later` | قانون‌ها نام لازم دارند. |  |
| `the rule matches nothing as written; fill in at least one criterion` | همهٔ فیلدهای تطبیق خالی‌اند. | دست‌کم یکی از مبدأ، مقصد، دامنه، پورت، مشتری، گروه، اینترفیس. |
| `"…" is not a network the router matches; use tcp, udp or icmp` / `icmp has no ports; drop the ports or pick tcp or udp` |  |  |
| `there is no outbound or balancer called "…"` |  |  |
| `a client is named by id here, and "…" is not one` / `an inbound is named by id here, and "…" is not one` / `there is no inbound with id …` | قانون‌ها مشتری و تانل را با شماره صدا می‌زنند. | آی‌دی از فهرست. |
| `"…" is a balancer; the default has to be an outbound. Point a rule at the balancer instead` |  |  |
| `"…" has no dot in it, so it is not a domain name` / `"…" is too long to be a domain name` |  |  |
| `unknown domain strategy "…"` / `unknown query strategy "…"` |  | یکی که فرم پیشنهاد می‌دهد. |
| `DNS needs at least one server to forward to` / `DNS server …: port … is out of range` |  |  |
| `traffic routing inactive: outbounds and routing rules are stored but not applied` (log) | موتور مسیریابی بالا نیامد (`nftables` و `CAP_NET_ADMIN` لازم دارد). | آنچه `error=` می‌گوید را درست کن؛ تا آن موقع ترافیک مستقیم می‌رود. |

## تنظیمات سابسکریپشن

| پیام | معنی | چه کنی |
|---|---|---|
| `"…" is already used by the panel itself` / `the path cannot start with /api/, which the panel serves` | مسیر سابسکریپشن روی پنل سایه می‌انداخت. | مسیر دیگری، مثل `/subscribe/`. |
| `a path can only contain letters, digits, - and _ (found "…")` / `that path is too short to be worth having` |  | دست‌کم دو کاراکتر. |
| `an update interval of … hours is outside the useful range of 1 to 168` |  |  |
| `"…" is not a template; choose one of …` |  | یکی از تمپلیت‌های فهرست. |
| `that notice is too long` / `that title is too long` |  |  |
| `a certificate and its key go together` | یکی از دو مسیر بدون دیگری. | هر دو، یا هیچ‌کدام. |
| `"…" is not an IP address` | آدرس گوش دادن. | یک آدرس، یا خالی برای همه. |
| `… is not a port number` |  |  |
| `a subscription needs a URL to fetch` / `that is not an http or https URL` / `too many redirects` / `outbound subscription fetch failed` (log) | سابسکریپشن‌های outbound. | URL را در مرورگر چک کن. |
| `subscription certificate is unusable; serving it plain` (log) | فایل‌های سرتیفیکیت لیسنر سابسکریپشن خوانده نشدند. | مسیر یا مجوز را درست کن (`chown wui`). |
| `subscription listener failed` (log) | پورتش bind نشد. | پورت دیگر، یا آزادش کن. |

## تنظیمات پنل

| پیام | معنی | چه کنی |
|---|---|---|
| `panel port … is out of range` |  | ۱ تا ۶۵۵۳۵. |
| `listen IP "…" is not an address` |  |  |
| `the URI path is one segment, like /panel/` | مسیر پایه وسطش اسلش دارد. | یک بخش. |
| `session duration must be between 1 and … minutes` / `page size must be between 0 and 1000` |  |  |
| `unknown language "…"` / `unknown time zone "…"` / `unknown calendar "…"` / `unknown log level "…"` / `unknown log format "…"` |  | یکی که فرم پیشنهاد می‌دهد. |
| `trusted proxy "…" is not an address or a CIDR` |  | مثل `10.0.0.5` یا `10.0.0.0/8`. |
| `the collection interval is 0 to 3600 seconds` / `the online window is 10 to 86400 seconds` / `the probe interval is 10 to 86400 seconds` / `the stale TTL cannot be negative` |  |  |
| `expiry must be between 0 and … days` / `device limit must be between 1 and …` | پیش‌فرض‌های مشتری جدید. |  |
| `backup interval must be between 0 and … hours` / `keep between 0 and 365 backups` |  |  |
| `notifications need a chat id` / `unknown bot language "…"` / `notification thresholds cannot be negative` / `a threshold is a percentage, 0 to 100` / `notification time: …` | تنظیمات تلگرام. |  |
| `the Telegram API server must begin with http:// or https://` / `the external traffic URI must begin with http:// or https://` / `the test URL must begin with http:// or https://` |  |  |
| `a mail server is required to send email` / `a from address is required to send email` / `at least one recipient is required to send email` / `mail port … is out of range` / `unknown mail encryption "…"` |  |  |
| `could not deliver a notification` / `telegram bot could not send` / `telegram bot could not poll` (log) | تلگرام در دسترس نبود، یا توکن غلط است. | `error=`: 401 یعنی توکن بد؛ timeout یعنی شبکه. |

## نودها

| پیام | معنی | چه کنی |
|---|---|---|
| `a node needs a name` / `a node called "…" already exists` |  |  |
| `a node needs an address` / `"…" is not a URL. It should look like https://vpn2.example.com:2096` |  | URL کامل پنل دیگر، با پورت. |
| `"…" is a private address; turn on …` | آدرس loopback یا خصوصی به‌عنوان نود رد شد. | آدرس عمومی، یا نودهای خصوصی را در تنظیمات فعال کن. |
| `a node needs an access token. Create one on that …` / `an access token is needed` |  | روی پنل دیگر: API → توکن جدید. |
| `pinning needs a certificate to pin. Fetch it from the node, or paste a sha256/… fingerprint` |  |  |
| `there is no server with id …` / `this panel's own entry cannot be removed` |  |  |
| `this node only accepts a managing panel that presents a client certificate` (401) / `that client certificate was not signed by the authority this node trusts` | نود mTLS می‌خواهد و این پنل سرتیفیکیت نداد، یا سرتیفیکیت اشتباه داد. | تنظیمات → نودها در هر دو طرف: سرتیفیکیت را دوباره رد و بدل کن. |
| `node became unreachable` / `node is not in step with this panel` (log) | نود جواب نمی‌دهد، یا دیتایش واگرا شده. | پنل خودِ نود را چک کن؛ در poll بعدی sync می‌شود. |
| `node address is plain HTTP; its token travels unencrypted` (log) |  | به نود سرتیفیکیت بده و `https://` بزن. |

## بک‌آپ و ریستور

| پیام | معنی | چه کنی |
|---|---|---|
| `"…" is not a backup file` / `"…" not found` | نام یکی از ماها نیست، یا رفته. | از فهرست انتخاب کن. |
| `"…" is not a gzip archive` / `"…" is damaged: …` | دانلود ناقص یا فایل اشتباه. | دوباره دانلود کن. |
| `"…" holds no database, so it is not a W-UI backup` | آرشیو نه `wui.db` دارد نه دامپ قابل حمل. | آرشیو W-UI نیست. |
| `"…" contains a file far too large to be one of ours` / `"…" expands to more than this can restore` | محافظ اندازه در برابر آرشیو دست‌کاری‌شده. |  |
| `"…" tries to write outside the data directory` | path traversal در آرشیو. | آرشیو W-UI نیست. |
| `the uploaded file is empty` |  |  |
| `could not save the current state before restoring: …` | کپی ایمنی شکست خورد، پس چیزی ریستور نشد. | فضای دیسک، یا مجوز پوشهٔ بک‌آپ. |
| `this backup was written by a newer panel (format …); update first` | دامپ قابل حمل جدیدتر از فهم این پنل است. | `w-ui update`، بعد ریستور. |
| `not a portable backup: …` | `wui-export.json` خوانا نیست. |  |
| `the restored backup could not be loaded into the database; the panel is running on the data it had` (log) | دامپ import نشد؛ به‌صورت `.restore-import.json.failed` نگه داشته شد. | `error=` جدول و ستون را می‌گوید؛ گزارشش کن. |
| `the archive holds neither a database file for this engine nor a portable dump; only the other files are restored` (log) | آرشیو قدیمی در موتور دیگر ریستور شد. | در یک پنل SQLite ریستورش کن، آن‌جا بک‌آپ تازه بگیر، همان را ریستور کن. |
| `restored, but this server's own addresses could not be put back` (log) | آدرس‌های آرشیو ماندند. | endpoint هر اینترفیس را چک کن. |
| `scheduled backup failed` / `could not snapshot the database; archiving the live file instead` (log) |  | فضای دیسک یا مجوز؛ دومی فقط هشدار است. |
| `snapshots are only available for sqlite` | در لاگ PostgreSQL دیده می‌شود؛ آن‌جا دامپ دیتا را حمل می‌کند. | هیچ. |

## استارت پنل

این‌ها پروسه را تمام می‌کنند؛ `journalctl -u wui -n 30` نشانشان می‌دهد.

| پیام | معنی | چه کنی |
|---|---|---|
| `another W-UI panel is already running here: …` `Two panels on one machine overwrite each other's firewall rules, and neither notices, so this one will not start` | نسخهٔ دومی استارت شده. | دیگری را متوقف کن، یا از همان استفاده کن. اجرای دستی `wui` وقتی سرویس بالاست همین را می‌دهد. |
| `config: WUI_LISTEN must not be empty` / `config: unknown database driver "…", want sqlite or postgres` / `config: WUI_DB_SOURCE is required for driver "…"` / `config: unsupported locale "…", want en or fa` | محیط در `/etc/systemd/system/wui.service` یا `/etc/wui/*.env`. | متغیر را درست کن. |
| `config: WUI_BASE_PATH "…" must be a single path segment` / `config: WUI_BASE_PATH "…" may only contain letters, digits, and - _ . ~` / `config: WUI_BASE_PATH may not be "…"; it collides with the API` |  |  |
| `config: WUI_TLS_CERT is set without WUI_TLS_KEY` / `config: WUI_TLS_KEY is set without WUI_TLS_CERT` / `config: the certificate and key do not form a usable pair: …` |  | هر دو فایل، هم‌خوان. |
| `config: WUI_COLLECT_INTERVAL is …, minimum is 1s` |  |  |
| `database: open …: …` | دیتابیس باز نشد. | SQLite: مجوز `/var/lib/wui`؛ PostgreSQL: سرور خاموش است یا `db.env` غلط — `w-ui` → ۲۵ → ۳. |
| `database: migrate: …` | اسکیما به‌روز نشد. | با همان خط گزارش کن؛ راه برگشت، ریستور آخرین بک‌آپ است. |
| `http server: …` | پورت پنل bind نشد. | پورت دیگر، یا آزادش کن (`ss -ltnp`). |
| `frontend placeholder embedded; run npm run build in web/ and rebuild` (log) | باینری‌ای که بدون رابط وب ساخته شده. | از بیلد ریلیز استفاده کن. |
| `created first admin account; this password is shown once` (log) | خطا نیست: دیتابیس تازه یک مدیر ساخت. | رمز را یادداشت کن. |

## موتور اعمال محدودیت

| پیام | معنی | چه کنی |
|---|---|---|
| `this kernel has no nft_quota support` / `quota enforcement inactive: limits are recorded but not applied` | کرنل `nft_quota` ندارد، پس محدودیت حجم به polling می‌افتد و overshoot می‌کند. | کرنل استاندارد توزیع؛ `modprobe nft_quota`. صفحهٔ Overview حالت را نشان می‌دهد. |
| `…: nft not found on PATH; install nftables` / `…: cannot read the ruleset (needs CAP_NET_ADMIN): …` |  | نصاب nftables را نصب و capability را می‌دهد؛ یونیت دستی هر دو را لازم دارد. |
| `…: nftables is Linux-only and this panel is running on …` |  |  |
| `rate limiting inactive: speed limits are recorded but not applied` / `this kernel has no HTB scheduler` | کرنل HTB ندارد. | کرنلی با `sch_htb`؛ تا آن موقع محدودیت سرعت اعمال نمی‌شود. |
| `something cleared our ruleset; rewriting it` / `the shaping hierarchy disappeared; rebuilding` (log) | برنامهٔ دیگری nftables یا tc را flush کرده. | پنل بازسازی کرد؛ پیدا کن چه چیزی flush می‌کند (`ufw`، پنل VPN دیگر) و متوقفش کن. |
| `enforce: reduced enforcement` | بخشی از ruleset اعمال نشد. | خط `error=`. |

## تانل‌ها در زمان اجرا

| پیام | معنی | چه کنی |
|---|---|---|
| `wgdriver: WireGuard is only available on Linux` / `ovpndriver: OpenVPN is only available on Linux` |  |  |
| `ovpndriver: openvpn is not installed` / `ovpndriver: /dev/net/tun is missing; load the tun module` |  | نصاب را دوباره اجرا کن، یا `modprobe tun`. |
| `ovpndriver: interface … has no certificates; recreate it` | PKI تانل رفته (از آرشیوی بدون آن ریستور شده، یا حذف شده). | تانل را حذف و دوباره بساز؛ مشتری‌ها پروفایل جدید می‌گیرند. |
| `ovpndriver: the server process would not start` / `ovpndriver: the server process is not running (last pid …)` |  | `journalctl -u wui` خروجی خود OpenVPN را کنارش دارد. |
| `wgdriver: awg syncconf …` / `wgdriver: configure …` | کرنل پیکربندی را رد کرد. | معمولاً آدرس تکراری یا کلید نامعتبر در یک peer؛ `error=` می‌گوید کدام. |
| `some peers were skipped because their keys could not be parsed` (log) | اکانت یک مشتری کلید بد دارد. | آن دستگاه را حذف و دوباره اضافه کن. |
| `openvpn transport changed; every customer needs their configuration again` (log) | تانل بین UDP و TCP جابه‌جا شده. | پروفایل جدید بده. |
| `this server has used its whole transfer allowance; customers are off it` (log) | سهمیهٔ خودِ نود (تنظیمات → نودها) تمام شده. | بالا ببر یا منتظر ریست بمان. |

## نصاب

| پیام | معنی | چه کنی |
|---|---|---|
| `run as root (sudo bash …)` |  | `sudo`. |
| `unsupported distribution: …` / `unsupported architecture: …` / `cannot read /etc/os-release; unsupported system` | فقط خانوادهٔ Debian/Ubuntu/RHEL روی amd64 یا arm64. |  |
| `unknown option: …` |  | `bash install.sh --help`. |
| `--db must be sqlite or postgres, not …` |  |  |
| `port … is already served by …; pass --port with a free one` | پورت پنل را چیز دیگری گرفته. | `--port N`، یا بگذار خودش انتخاب کند. |
| `could not install base packages` / `wireguard-tools did not install; the panel cannot serve WireGuard without it` / `nft missing; quota enforcement cannot run without nftables` | apt/dnf شکست خورد. | شبکه و منابع پکیج را چک کن؛ دستور نشان‌داده‌شده را دستی اجرا کن تا دلیل را ببینی. |
| `download failed` / `checksum mismatch for …: the download is not the file that was released` |  | دوباره امتحان کن؛ ناهماهنگیِ مداوم یعنی mirror دست‌کاری‌شده — نصب نکن. |
| `--from-source needs Go on PATH` / `could not install Go; use --local <path> with a prebuilt binary` / `build failed` / `could not fetch the source` / `could not unpack the source` / `the source archive does not look like this project` | ساخت از سورس. | از ریلیز استفاده کن، یا `--local` با باینری‌ای که خودت ساختی. |
| `no such file: …` / `installed binary is not executable` | `--local` به چیز قابل‌استفاده‌ای اشاره نمی‌کند. |  |
| `could not install PostgreSQL` / `could not initialise PostgreSQL` / `PostgreSQL did not come up; see: journalctl -u postgresql` / `could not create the database role` / `could not create the database` / `could not set the database password` | مرحلهٔ PostgreSQL. | لاگ نام‌برده؛ SQLite یک پرچم فاصله دارد (`--db sqlite`). |
| `service failed to start — see: journalctl -u wui -n 50 --no-pager` | پنل بالا نیامد. | خطوط آخر لاگ یکی از پیام‌های *استارت پنل* در بالاست. |
| `no answer — the terminal closed before the questions were finished` | stdin وسط سؤال تمام شد. | در ترمینال اجرا کن، یا `-y` و پرچم‌ها را بده. |
| `cancelled — nothing was changed` / `nothing was deleted` | گفتی نه. |  |
| `cannot read randomness from /dev/urandom` |  | کانتینر خراب؛ آن‌جا نصب نکن. |
| `port 80 is already served by …` + `no terminal to ask for another port — skipping the certificate` | Let's Encrypt پورت ۸۰ می‌خواهد و گرفته است؛ بدون ترمینال، نصب بدون TLS ادامه می‌دهد. | بعدش: `w-ui` → ۲۰، که پورت جایگزین می‌پرسد. |

## `update.sh`

| پیام | معنی | چه کنی |
|---|---|---|
| `W-UI is not installed on this machine; run install.sh instead` |  |  |
| `no release … with a build for … was found` | تگ وجود ندارد، یا برای این معماری باینری ندارد. | نام تگ را چک کن؛ `dev-latest` برای بیلد چرخشی. |
| `download failed` / `checksum mismatch for …` |  | دوباره امتحان کن. |
| `signature check failed: the download is not a build this project signed` | امضای باینری با کلید پروژه نمی‌خواند. چیزی نصب نشد. | نصبش نکن. اگر ریلیزهای خودت را می‌سازی، با کلید خودت امضا کن. |
| `the installed panel cannot check signatures; the checksum is what was verified` (warning) | پنل نصب‌شده بدون کلید عمومی ساخته شده. | بی‌ضرر؛ بعد از این آپدیت، بعدی با امضا چک می‌شود. |
| `the panel did not come back — see: journalctl -u wui -n 50 --no-pager` |  | پیام‌های *استارت پنل*. |

## منوی `w-ui`

| پیام | معنی | چه کنی |
|---|---|---|
| `Please install the panel first` / `Panel installed, Please do not reinstall` | گزینه پنل لازم دارد، یا از قبل داری. |  |
| `Please enter the correct number [0-28]` |  |  |
| `get current settings error, please check logs` | `wui setting show` شکست خورد — دیتابیس باز نشد. | `journalctl -u wui`؛ روی PostgreSQL، روشن است؟ |
| `panel Failed to start, Probably because it takes longer than two seconds to start, Please check the log information later` / `Panel restart failed, …` / `Panel stop failed, …` | وضعیت ظرف دو ثانیه عوض نشد. | لحظه‌ای بعد `w-ui status`؛ بعد لاگ. |
| `w-ui Failed to set Autostart` / `w-ui Failed to cancel autostart` | `systemctl enable/disable` شکست خورد. | فایل یونیت نیست: نصاب را دوباره اجرا کن. |
| `Failed to download script, Please check whether the machine can connect Github` / `could not fetch the installer` |  | شبکه به گیت‌هاب. |
| `Domain name cannot be empty. Please try again.` / `Invalid domain format: …` / `No domain given; cancelled` |  |  |
| `Port … is busy; cannot proceed with issuance.` / `Invalid port provided.` | پورت چالش ACME. | پورت آزاد دیگر؛ باید از اینترنت قابل‌دسترس باشد. |
| `Certificate issuance failed, script exiting...` / `Issuing certificate failed, please check logs` / `Failed to issue certificate for IP: …` / `IP certificate setup failed.` | Let's Encrypt رد کرد یا به سرور نرسید. | پورت ۸۰ (یا آنچه دادی) از اینترنت باز؛ DNS به این‌جا؛ rate-limit نشده (۵ در هفته برای هر نام). لاگ acme.sh در `/root/.acme.sh/`. |
| `Certificate installation failed, script exiting...` / `Installing certificate failed, exiting.` / `Certificate files not found after installation` / `Error: Certificate or private key file not found for …` | فایل‌ها در `/etc/wui/certs/<name>/` ننشستند. | مجوزها؛ گزینه را دوباره اجرا کن. |
| `Failed to install acme.sh` / `Install acme failed, please check logs.` / `Installation of acme.sh failed.` / `install socat failed, please check logs` |  | شبکه؛ `curl https://get.acme.sh` دستی. |
| `Auto renew failed, certificate details:` / `Auto update setup failed, script exiting...` | hook تمدید یا آپگرید acme.sh. | جزئیات چاپ‌شده زیرش. |
| `Backup failed: …` / `backup failed: …` / `Restore failed: …` / `restore failed: …` | از `wui backup`؛ متن بعد از دونقطه پیام خود پنل است، در بخش *بک‌آپ و ریستور*. |  |
| `The panel did not come back; see: journalctl -u … -n 50` / `the panel did not come up on …; see: …` | بعد از ریستور یا جابه‌جایی بین موتورها. | لاگ؛ وضعیت قبلی نگه داشته شده — خطی که همراهش چاپ شده می‌گوید چطور برگردی. |
| `PostgreSQL is not installed` / `PostgreSQL is not set up for the panel yet; choose 1 first` / `PostgreSQL install failed` |  | اول گزینهٔ ۲۵ → ۱. |
| `No such file` | مسیر ریستور وجود ندارد. |  |
| `ERROR: You must be root to run this script!` |  | `sudo w-ui`. |
