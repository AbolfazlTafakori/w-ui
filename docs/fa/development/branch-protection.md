---
description: "تنظیمات GitHub که برای main و تگ‌های ریلیز روشن می‌شوند، تا چیزی که CI و بازبینی ندیده‌اند به ریلیز نرسد."
---

# محافظت از main و ریلیزها

این‌ها تنظیمات GitHub هستند که یک بار در **Settings** مخزن انجام می‌شوند. هنوز روشن نیستند؛ وقتی هر جاب CI زیر حداقل یک بار روی `main` سبز شد روشنشان کن.

## یک ruleset برای `main`

**Settings ← Rules ← Rulesets ← New branch ruleset**، با هدف `main`:

- [ ] **Restrict deletions** و **Block force pushes**.
- [ ] **Require a pull request before merging** — یک تأیید وقتی نگه‌دارندهٔ دوم هست؛ با push کامیت تازه تأییدها رد شوند.
- [ ] **Require status checks to pass**، همراه با **Require branches to be up to date**. چک‌ها، با نامی که هر جاب CI نشان می‌دهد:
  - *Frontend builds and matches the committed bundle*
  - *Go vet, static analysis and tests*
  - *Installer and menu behave*
  - *Clean install on …* — هر ورودی ماتریس
  - *Upgrade from every earlier release (PostgreSQL)*
  - *Update from … by installer*، *by panel*، *by menu* — هر ورودی
  - *Panel and node across releases …* — هر ورودی
- [ ] **Require linear history**.
- [ ] Bypass: هیچ‌کس. مالک هم مثل بقیه از طریق pull request ادغام می‌کند؛ hotfix هم.

## همان چک‌ها قبل از `dev-latest`

در `.github/workflows/ci.yml`، جاب `dev-release` زیر `needs` فهرست چیزهایی است که منتظرشان می‌ماند. وقتی `upgrade`، `upgrade-postgres` و `node-compat` سبز اجرا شدند اضافه‌شان کن، تا کانال dev فقط بیلدهایی را ببرد که تست‌های ارتقا را هم گذرانده‌اند.

## یک ruleset برای تگ‌های ریلیز

**New tag ruleset**، با هدف `v*`:

- [ ] **Restrict creations** فقط برای نگه‌دارنده‌ها، **Restrict updates** و **Restrict deletions** — تگ منتشرشده هرگز جابه‌جا نمی‌شود.

گردش‌کار ریلیز علاوه بر این تگی را که CI روی کامیتش در `main` سبز نشده رد می‌کند (ببین [چک‌لیست ریلیز](./release-checklist)).

## کلید امضا

- [ ] `WUI_SIGNING_KEY` را از secret مخزن به یک **environment** به نام `release` ببر، با **Required reviewers** و محدود کردن **Deployment branches and tags** به تگ‌های `v*`؛ و `environment: release` را روی جاب ریلیز بگذار. آن‌وقت اجرای گردش‌کار روی هر ref دیگری نمی‌تواند کلید را بخواند.
- [ ] `dev-release` با همان کلید امضا می‌کند: یا همان environment را به آن بده، یا یک کلید جدا برای بیلدهای توسعه.
