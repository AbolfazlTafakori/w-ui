import { defineConfig } from 'vitepress'
import postcssRTLCSS from 'postcss-rtlcss'

// The documentation site, served from GitHub Pages at /w-ui/. English is
// the root; Persian sits under /fa/ with its own sidebar, right to left.
// The sections follow what the panels people already know document --
// getting started, the panel page by page, operations, worked examples,
// reference, help -- so a reader coming from another panel's docs
// finds things where they expect them.

const enSidebar = [
  {
    text: 'Getting started',
    items: [
      { text: 'What W-UI is', link: '/' },
      { text: 'Install', link: '/guide/install' },
      { text: 'After the install', link: '/guide/after-install' },
      { text: 'First login', link: '/guide/first-login' },
      { text: 'First tunnel, first customer', link: '/guide/first-steps' },
      { text: 'Client apps', link: '/guide/client-apps' },
      { text: 'Certificates', link: '/guide/certificates' },
      { text: 'Updating and uninstalling', link: '/guide/update' },
    ],
  },
  {
    text: 'The panel',
    items: [
      { text: 'Overview', link: '/panel/overview' },
      { text: 'Interfaces (inbounds)', link: '/panel/interfaces' },
      { text: 'Clients', link: '/panel/clients' },
      { text: 'Groups', link: '/panel/groups' },
      { text: 'Hosts', link: '/panel/hosts' },
      { text: 'Outbounds', link: '/panel/outbounds' },
      { text: 'Routing', link: '/panel/routing' },
      { text: 'Sharing', link: '/panel/sharing' },
      { text: 'Nodes', link: '/panel/nodes' },
      { text: 'Settings', link: '/panel/settings' },
      { text: 'Operators', link: '/panel/operators' },
      { text: 'Engine', link: '/panel/engine' },
      { text: 'Subscription page', link: '/panel/subscription' },
      { text: 'Telegram bot', link: '/panel/telegram' },
    ],
  },
  {
    text: 'Operations',
    items: [
      { text: 'Security', link: '/operations/security' },
      { text: 'Reverse proxy', link: '/operations/reverse-proxy' },
      { text: 'Ports and firewall', link: '/operations/ports-firewall' },
      { text: 'Backup and restore', link: '/operations/backup-restore' },
    ],
  },
  {
    text: 'Examples',
    collapsed: true,
    items: [
      { text: 'All examples', link: '/examples/' },
      { text: 'Certificate for a domain', link: '/examples/ssl-domain' },
      { text: 'Certificate for the IP', link: '/examples/ssl-ip' },
      { text: 'Wildcard through Cloudflare', link: '/examples/wildcard-cloudflare' },
      { text: 'All traffic through a hop', link: '/examples/hop-all-traffic' },
      { text: 'Cloudflare WARP outbound', link: '/examples/warp' },
      { text: 'Block ads and BitTorrent', link: '/examples/blocking-rules' },
      { text: 'Split routing', link: '/examples/split-routing' },
      { text: 'Backups to Telegram', link: '/examples/backup-telegram' },
      { text: 'Cloud-init install', link: '/examples/cloud-init' },
      { text: 'Move to another server', link: '/examples/move-server' },
    ],
  },
  {
    text: 'Reference',
    items: [
      { text: 'The w-ui menu', link: '/reference/w-ui' },
      { text: 'The wui binary', link: '/reference/wui' },
      { text: 'Configuration', link: '/reference/configuration' },
      { text: 'Database', link: '/reference/database' },
      { text: 'API', link: '/reference/api' },
      { text: 'How enforcement works', link: '/reference/how-it-works' },
      { text: 'Errors', link: '/reference/errors' },
    ],
  },
  {
    text: 'Help',
    items: [
      { text: 'FAQ', link: '/help/faq' },
      { text: 'Troubleshooting', link: '/help/troubleshooting' },
      { text: 'Coming from another panel', link: '/help/migration' },
      { text: 'Contributing', link: '/help/contributing' },
    ],
  },
]

const faSidebar = [
  {
    text: 'شروع',
    items: [
      { text: 'W-UI چیست', link: '/fa/' },
      { text: 'نصب', link: '/fa/guide/install' },
      { text: 'بعد از نصب', link: '/fa/guide/after-install' },
      { text: 'اولین ورود', link: '/fa/guide/first-login' },
      { text: 'اولین تانل، اولین مشتری', link: '/fa/guide/first-steps' },
      { text: 'اپ‌های کلاینت', link: '/fa/guide/client-apps' },
      { text: 'سرتیفیکیت', link: '/fa/guide/certificates' },
      { text: 'به‌روزرسانی و حذف', link: '/fa/guide/update' },
    ],
  },
  {
    text: 'پنل',
    items: [
      { text: 'نمای کلی', link: '/fa/panel/overview' },
      { text: 'اینترفیس‌ها', link: '/fa/panel/interfaces' },
      { text: 'مشتری‌ها', link: '/fa/panel/clients' },
      { text: 'گروه‌ها', link: '/fa/panel/groups' },
      { text: 'هاست‌ها', link: '/fa/panel/hosts' },
      { text: 'خروجی‌ها', link: '/fa/panel/outbounds' },
      { text: 'مسیریابی', link: '/fa/panel/routing' },
      { text: 'اشتراک‌گذاری', link: '/fa/panel/sharing' },
      { text: 'نودها', link: '/fa/panel/nodes' },
      { text: 'تنظیمات', link: '/fa/panel/settings' },
      { text: 'ادمین‌ها', link: '/fa/panel/operators' },
      { text: 'موتور', link: '/fa/panel/engine' },
      { text: 'صفحهٔ سابسکریپشن', link: '/fa/panel/subscription' },
      { text: 'ربات تلگرام', link: '/fa/panel/telegram' },
    ],
  },
  {
    text: 'عملیات',
    items: [
      { text: 'امنیت', link: '/fa/operations/security' },
      { text: 'Reverse proxy', link: '/fa/operations/reverse-proxy' },
      { text: 'پورت‌ها و فایروال', link: '/fa/operations/ports-firewall' },
      { text: 'بک‌آپ و ریستور', link: '/fa/operations/backup-restore' },
    ],
  },
  {
    text: 'نمونه‌ها',
    collapsed: true,
    items: [
      { text: 'همهٔ نمونه‌ها', link: '/fa/examples/' },
      { text: 'سرتیفیکیت برای دامنه', link: '/fa/examples/ssl-domain' },
      { text: 'سرتیفیکیت برای آی‌پی', link: '/fa/examples/ssl-ip' },
      { text: 'Wildcard از طریق کلادفلر', link: '/fa/examples/wildcard-cloudflare' },
      { text: 'همهٔ ترافیک از یک hop', link: '/fa/examples/hop-all-traffic' },
      { text: 'خروجی WARP کلادفلر', link: '/fa/examples/warp' },
      { text: 'مسدود کردن تبلیغ و بیت‌تورنت', link: '/fa/examples/blocking-rules' },
      { text: 'مسیریابی تفکیک‌شده', link: '/fa/examples/split-routing' },
      { text: 'بک‌آپ به تلگرام', link: '/fa/examples/backup-telegram' },
      { text: 'نصب با cloud-init', link: '/fa/examples/cloud-init' },
      { text: 'انتقال به سرور دیگر', link: '/fa/examples/move-server' },
    ],
  },
  {
    text: 'مرجع',
    items: [
      { text: 'منوی w-ui', link: '/fa/reference/w-ui' },
      { text: 'باینری wui', link: '/fa/reference/wui' },
      { text: 'پیکربندی', link: '/fa/reference/configuration' },
      { text: 'دیتابیس', link: '/fa/reference/database' },
      { text: 'API', link: '/fa/reference/api' },
      { text: 'اعمال محدودیت چطور کار می‌کند', link: '/fa/reference/how-it-works' },
      { text: 'خطاها', link: '/fa/reference/errors' },
    ],
  },
  {
    text: 'راهنمای مشکل',
    items: [
      { text: 'سؤالات متداول', link: '/fa/help/faq' },
      { text: 'عیب‌یابی', link: '/fa/help/troubleshooting' },
      { text: 'از پنل دیگری آمده‌اید', link: '/fa/help/migration' },
      { text: 'مشارکت', link: '/fa/help/contributing' },
    ],
  },
]

const en = {
  label: 'English',
  lang: 'en',
  themeConfig: {
    nav: [
      { text: 'Guide', link: '/guide/install', activeMatch: '^/guide/' },
      { text: 'Panel', link: '/panel/overview', activeMatch: '^/panel/' },
      { text: 'Examples', link: '/examples/', activeMatch: '^/examples/' },
      { text: 'Reference', link: '/reference/w-ui', activeMatch: '^/(reference|operations|help)/' },
    ],
    sidebar: { '/': enSidebar },
    outline: { level: [2, 3] },
  },
}

const fa = {
  label: 'فارسی',
  lang: 'fa',
  dir: 'rtl',
  link: '/fa/',
  themeConfig: {
    nav: [
      { text: 'راهنما', link: '/fa/guide/install', activeMatch: '^/fa/guide/' },
      { text: 'پنل', link: '/fa/panel/overview', activeMatch: '^/fa/panel/' },
      { text: 'نمونه‌ها', link: '/fa/examples/', activeMatch: '^/fa/examples/' },
      { text: 'مرجع', link: '/fa/reference/w-ui', activeMatch: '^/fa/(reference|operations|help)/' },
    ],
    sidebar: { '/fa/': faSidebar },
    outline: { level: [2, 3], label: 'در این صفحه' },
    docFooter: { prev: 'صفحهٔ قبلی', next: 'صفحهٔ بعدی' },
    lastUpdated: { text: 'آخرین به‌روزرسانی' },
    returnToTopLabel: 'بازگشت به بالا',
    sidebarMenuLabel: 'فهرست',
    darkModeSwitchLabel: 'حالت تیره',
    langMenuLabel: 'تغییر زبان',
    notFound: {
      title: 'صفحه پیدا نشد',
      quote: 'این آدرس چیزی ندارد. شاید جابه‌جا شده باشد.',
      linkText: 'برگشت به خانه',
    },
  },
}

export default defineConfig({
  title: 'W-UI',
  description: 'A WireGuard and OpenVPN panel for reselling access, with the classic panel layout.',
  base: '/w-ui/',
  cleanUrls: true,
  lastUpdated: true,
  // Every rule gets a mirrored [dir=rtl] twin, so the Persian site lays out
  // right to left -- sidebar, outline, code blocks, pager -- rather than only
  // reading right to left. Combined mode prefixes both directions, so the
  // two sets of rules carry the same weight.
  vite: { css: { postcss: { plugins: [postcssRTLCSS({ mode: 'combined', ltrPrefix: '[dir="ltr"]', rtlPrefix: '[dir="rtl"]' })] } } },
  head: [
    ['link', { rel: 'icon', type: 'image/png', sizes: '64x64', href: '/w-ui/favicon-64.png' }],
    ['link', { rel: 'icon', href: '/w-ui/favicon.ico' }],
    ['link', { rel: 'apple-touch-icon', href: '/w-ui/logo-192.png' }],
    ['meta', { property: 'og:image', content: 'https://abolfazltafakori.github.io/w-ui/logo-512.png' }],
    ['meta', { name: 'theme-color', content: '#0b0b0d' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'W-UI' }],
  ],
  sitemap: { hostname: 'https://abolfazltafakori.github.io/w-ui/' },
  locales: { root: en, fa },
  themeConfig: {
    logo: '/logo.png',
    siteTitle: false,
    search: {
      provider: 'local',
      options: {
        locales: {
          fa: {
            translations: {
              button: { buttonText: 'جست‌وجو', buttonAriaLabel: 'جست‌وجو' },
              modal: {
                displayDetails: 'نمایش جزئیات',
                resetButtonTitle: 'پاک کردن',
                backButtonTitle: 'بستن',
                noResultsText: 'چیزی پیدا نشد برای',
                footer: { selectText: 'انتخاب', navigateText: 'حرکت', closeText: 'بستن' },
              },
            },
          },
        },
      },
    },
    socialLinks: [{ icon: 'github', link: 'https://github.com/AbolfazlTafakori/w-ui' }],
    footer: {
      message: 'Released under the AGPL-3.0 License.',
      copyright: 'Copyright © 2026 Abolfazl Tafakori',
    },
  },
})
