---
description: "هر کاری که پنل می‌کند از طریق HTTP API خودش است : مسیر خصوصی‌ای نیست که رابط استفاده کند و دیگران نتوانند."
---

# API

هر کاری که پنل می‌کند از طریق HTTP API خودش است — مسیر خصوصی‌ای نیست که رابط استفاده کند و دیگران نتوانند.

## احراز هویت

یا برای توکن نشست وارد شو:

```bash
curl -X POST 'https://panel:2053/PATH/api/auth/login' \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"…"}'
```

یا از **توکن API** استفاده کن — همانی که نصاب چاپ کرد، یا یکی از نودها → توکن‌ها یا `wui token issue --name NAME` — و این‌طور بفرست:

```
Authorization: Bearer wui_…
```

## مستندات داخل پنل است

**API** در سایدبار همهٔ endpointها را گروه‌بندی‌شده، با بدنهٔ نمونه و دستور `curl` با آدرسی که با آن به پنل رسیده‌ای لیست می‌کند. صفحه از همان جدولی ساخته می‌شود که مسیرها از آن ثبت می‌شوند، پس فقط endpointهایی را توصیف می‌کند که وجود دارند.

`GET /api/docs` همان را به JSON برمی‌گرداند.

## چند تا که زیاد لازم می‌شود

| | |
|--|--|
| `GET /api/overview/full` | تله‌متری، وضعیت پنل و موجودی در یک درخواست |
| `GET /api/clients?search=&status=&group=&page=&perPage=` | مشتری‌ها |
| `POST /api/clients` | ساختن؛ `telegramId`، `quotaBytes`، `expiresAt`، `deviceLimit`، `interfaceIds` |
| `POST /api/clients/{id}/reset` | بازنشانی ترافیک |
| `GET /api/clients/{id}/configs` · `GET /api/devices/{id}/profiles` | همهٔ کانفیگ‌ها، به ازای دستگاه و هاست |
| `GET /api/interfaces` · `POST` · `PATCH /{id}` · `DELETE /{id}` | تانل‌ها |
| `GET /api/outbounds` · `POST /api/outbounds/{id}/check` | hopها و probe |
| `GET /api/routing` · `POST /api/routing/rules/order` | قوانین |
| `GET /api/template?section=` · `PUT` | کل پیکربندی |
| `POST /api/backups` · `GET /api/backups/{name}` | بک‌آپ |
| `GET /api/system` · `GET /api/system/history` | سرور |

خطاها به شکل `{"error": "…", "field": "…"}` با status معنادار برمی‌گردند؛ خطای اعتبارسنجی فیلد را نام می‌برد.
