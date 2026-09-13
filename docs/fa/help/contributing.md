---
description: "چطور W-UI را از سورس بسازی، تست‌هایش را اجرا کنی و تغییری بفرستی."
---

# مشارکت

## ساخت

```bash
git clone https://github.com/AbolfazlTafakori/w-ui
cd w-ui/web && npm ci && npx vite build && cd ..
CGO_ENABLED=0 go build -o wui ./cmd/wui
```

فرانت‌اند داخل باینری embed می‌شود، پس اول آن را بساز. Go 1.24+، Node 22+.

## اجرای محلی

```bash
WUI_DATA_DIR=./data WUI_LISTEN=127.0.0.1:2096 ./wui
```

روی ماشینی بدون nftables پنل اجرا می‌شود و enforcement را «در دسترس نیست» گزارش می‌کند — هر صفحه کار می‌کند، محدودیت‌ها اعمال نمی‌شوند.

برای فرانت‌اند با hot reload: `cd web && npm run dev` که `/api` را به باینری پروکسی می‌کند.

## تست

```bash
go vet ./... && go test ./...
bash scripts/test-install-questions.sh install.sh
bash scripts/test-install-output.sh install.sh
```

CI این‌ها را اجرا می‌کند، به‌علاوهٔ `staticcheck`، `govulncheck`، بیلد فرانت‌اند که باید با بسته‌بندی کامیت‌شده بخواند، و نصب تمیز روی هفت توزیع.

## فرستادن تغییر

یک pull request به `main` با اینکه چه عوض شد، چرا، و چطور تست شد. پیام کامیت را به سبک لاگ نگه دار: یک جمله دربارهٔ اینکه تغییر برای مدیر پنل چه می‌کند.

## مستندات

این سایت `docs/` است — VitePress، انگلیسی در ریشه و فارسی زیر `fa/`. `cd docs && npm ci && npm run dev`. هر صفحه به هر دو زبان هست؛ هر دو را اضافه کن.
