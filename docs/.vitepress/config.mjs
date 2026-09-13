import { defineConfig } from 'vitepress'

// The documentation site, served from GitHub Pages at /w-ui/. English is
// the root; Persian sits under /fa/ with its own sidebar, right to left.

const en = {
  label: 'English',
  lang: 'en',
  themeConfig: {
    nav: [
      { text: 'Guide', link: '/guide/install' },
      { text: 'Panel', link: '/panel/overview' },
      { text: 'Reference', link: '/reference/w-ui' },
      { text: 'GitHub', link: 'https://github.com/AbolfazlTafakori/w-ui' },
    ],
    sidebar: {
      '/': [
        {
          text: 'Getting started',
          items: [
            { text: 'What W-UI is', link: '/' },
            { text: 'Install', link: '/guide/install' },
            { text: 'After the install', link: '/guide/after-install' },
            { text: 'First tunnel, first customer', link: '/guide/first-steps' },
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
            { text: 'Engine', link: '/panel/engine' },
            { text: 'Subscription page', link: '/panel/subscription' },
            { text: 'Telegram bot', link: '/panel/telegram' },
          ],
        },
        {
          text: 'Reference',
          items: [
            { text: 'The w-ui menu', link: '/reference/w-ui' },
            { text: 'The wui binary', link: '/reference/wui' },
            { text: 'Configuration', link: '/reference/configuration' },
            { text: 'API', link: '/reference/api' },
            { text: 'How enforcement works', link: '/reference/how-it-works' },
            { text: 'Security', link: '/reference/security' },
            { text: 'Troubleshooting', link: '/reference/troubleshooting' },
          ],
        },
      ],
    },
    editLink: {
      pattern: 'https://github.com/AbolfazlTafakori/w-ui/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },
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
      { text: 'راهنما', link: '/fa/guide/install' },
      { text: 'پنل', link: '/fa/panel/overview' },
      { text: 'مرجع', link: '/fa/reference/w-ui' },
      { text: 'گیت‌هاب', link: 'https://github.com/AbolfazlTafakori/w-ui' },
    ],
    sidebar: {
      '/fa/': [
        {
          text: 'شروع',
          items: [
            { text: 'W-UI چیست', link: '/fa/' },
            { text: 'نصب', link: '/fa/guide/install' },
            { text: 'بعد از نصب', link: '/fa/guide/after-install' },
            { text: 'اولین تانل، اولین مشتری', link: '/fa/guide/first-steps' },
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
            { text: 'موتور', link: '/fa/panel/engine' },
            { text: 'صفحهٔ سابسکریپشن', link: '/fa/panel/subscription' },
            { text: 'ربات تلگرام', link: '/fa/panel/telegram' },
          ],
        },
        {
          text: 'مرجع',
          items: [
            { text: 'منوی w-ui', link: '/fa/reference/w-ui' },
            { text: 'باینری wui', link: '/fa/reference/wui' },
            { text: 'پیکربندی', link: '/fa/reference/configuration' },
            { text: 'API', link: '/fa/reference/api' },
            { text: 'اعمال محدودیت چطور کار می‌کند', link: '/fa/reference/how-it-works' },
            { text: 'امنیت', link: '/fa/reference/security' },
            { text: 'عیب‌یابی', link: '/fa/reference/troubleshooting' },
          ],
        },
      ],
    },
    editLink: {
      pattern: 'https://github.com/AbolfazlTafakori/w-ui/edit/main/docs/:path',
      text: 'ویرایش این صفحه در گیت‌هاب',
    },
    outline: { level: [2, 3], label: 'در این صفحه' },
    docFooter: { prev: 'قبلی', next: 'بعدی' },
    lastUpdated: { text: 'آخرین به‌روزرسانی' },
    returnToTopLabel: 'بازگشت به بالا',
    sidebarMenuLabel: 'فهرست',
    darkModeSwitchLabel: 'حالت تیره',
  },
}

export default defineConfig({
  title: 'W-UI',
  description: 'A WireGuard and OpenVPN panel for reselling access, laid out like 3x-ui.',
  base: '/w-ui/',
  cleanUrls: true,
  lastUpdated: true,
  head: [['link', { rel: 'icon', href: '/w-ui/favicon.svg' }]],
  locales: { root: en, fa },
  themeConfig: {
    logo: '/favicon.svg',
    search: { provider: 'local' },
    socialLinks: [{ icon: 'github', link: 'https://github.com/AbolfazlTafakori/w-ui' }],
    footer: {
      message: 'Released under the AGPL-3.0 License.',
      copyright: 'Copyright © 2026 Abolfazl Tafakori',
    },
  },
})
