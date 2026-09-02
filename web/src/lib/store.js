import { reactive } from 'vue'
import { api, getToken, setToken } from './api.js'

const LOCALE_KEY = 'wui.locale'

function storedLocale() {
  try {
    return localStorage.getItem(LOCALE_KEY)
  } catch {
    return null
  }
}

// One small reactive object rather than a state library. The panel has a
// handful of globals — who is signed in, which language, what the server
// supports — and nothing that needs more machinery than this.
export const store = reactive({
  admin: null,
  meta: null,
  locale: storedLocale() || 'en',
  direction: 'ltr',
  messages: {},
  ready: false,
  toast: null,
})

// t resolves a message key. The catalog comes from the Go binary, so the
// backend and frontend can never disagree about what a string says, and a key
// missing from Persian falls back to English server-side.
export function t(key) {
  return store.messages[key] || key
}

// tn picks the right form for a count and substitutes it.
//
// "1 customers" is the kind of thing that makes a panel feel unfinished, and
// "customer(s)" is the workaround that admits the problem rather than solving
// it. English needs two forms; Persian uses one noun for every count, so its
// two entries are simply the same sentence and this costs nothing there.
//
// The number itself is formatted for the locale, so Persian shows Persian
// digits rather than a stray Latin numeral mid-sentence.
export function tn(key, n) {
  const form = store.messages[`${key}.${n === 1 ? 'one' : 'other'}`]
  const text = form || store.messages[key] || key
  return text.replace('{n}', Number(n || 0).toLocaleString(store.locale))
}

export async function loadMessages(locale) {
  const res = await api.messages(locale)
  store.locale = locale
  store.direction = res.direction
  store.messages = res.messages

  document.documentElement.lang = locale
  document.documentElement.dir = res.direction
  try {
    localStorage.setItem(LOCALE_KEY, locale)
  } catch {
    /* not fatal: the choice just will not persist */
  }
}

export async function bootstrap() {
  store.meta = await api.meta()
  await loadMessages(store.locale)

  if (getToken()) {
    try {
      store.admin = await api.me()
    } catch {
      store.admin = null
    }
  }
  store.ready = true
}

export async function signIn(username, password, code) {
  const res = await api.login(username, password, code)

  // The password was right but a second factor is set. Nothing is signed in
  // yet; the caller asks for the code and calls again.
  if (res.needCode) return { needCode: true }

  setToken(res.token)
  store.admin = res.admin
  if (res.admin?.locale && res.admin.locale !== store.locale) {
    await loadMessages(res.admin.locale)
  }
  return res.admin
}

export function signOut() {
  setToken(null)
  store.admin = null
}

let toastTimer = null

export function notify(message, kind = 'info') {
  // The expired-session error carries no message: it has already been
  // announced once, and a second empty toast on top of it is noise.
  if (!message) return

  store.toast = { message, kind }
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (store.toast = null), 4500)
}
