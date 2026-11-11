// The languages the panel can be read in, in one place so the login screen,
// the sidebar switcher and the General page never disagree about what a tag is
// called or which flag stands for it. The list of locales the build actually
// ships is the backend's (store.meta.locales); this only names the ones it may
// contain. A tag with no entry falls back to showing itself, so a new locale
// added to the catalog is never nameless.
const LOCALES = {
  en: { name: 'English', flag: '🇬🇧' },
  fa: { name: 'فارسی', flag: '🇮🇷' },
  zh: { name: '中文', flag: '🇨🇳' },
  ru: { name: 'Русский', flag: '🇷🇺' },
  tr: { name: 'Türkçe', flag: '🇹🇷' },
}

export function localeName(tag) {
  return LOCALES[tag]?.name || tag
}

export function localeFlag(tag) {
  return LOCALES[tag]?.flag || '🌐'
}
