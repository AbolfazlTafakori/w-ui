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
  navigating: false,
  // The few panel settings the rest of the pages consult: how many rows a
  // table page holds, which calendar dates are shown in, and how close to
  // the line a customer has to be before they are coloured as running out.
  panel: { pageSize: 25, datepicker: 'gregorian', expireDiff: 3, trafficDiff: 1 },
})

// loadPanelSettings refreshes store.panel from the server. Quiet on failure:
// the defaults are right for most installs, and a page must not stop
// rendering because a settings read did not.
export async function loadPanelSettings() {
  try {
    const res = await api.get('/api/settings', { background: true })
    const s = res?.settings || {}
    store.panel = {
      pageSize: Number.isFinite(s.pageSize) ? s.pageSize : 25,
      datepicker: s.datepicker || 'gregorian',
      expireDiff: s.expireDiff ?? 3,
      trafficDiff: s.trafficDiff ?? 1,
    }
  } catch {
    /* keep the defaults */
  }
}

// t resolves a message key. The catalog comes from the Go binary, so the
// backend and frontend can never disagree about what a string says, and a key
// missing from Persian falls back to English server-side.
// True while a page is changing or its first load is in flight. The shell draws
// a bar from it, so a click on the sidebar is acknowledged immediately rather
// than leaving the old page on screen until the new one has its data.
export function setNavigating(on) {
  store.navigating = on
}

export function t(key, vars) {
  let text = store.messages[key] || key
  // {name} placeholders, filled from the second argument when one is given;
  // a number is written the locale's way.
  if (vars) {
    for (const [k, v] of Object.entries(vars)) {
      text = text.split(`{${k}}`).join(typeof v === 'number' ? v.toLocaleString(store.locale) : String(v))
    }
  }
  return text
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
  store.messages = res.messages

  // The layout is never mirrored. the classic panel keeps the same left-to-right page in
  // Persian -- labels on the left, controls on the right, the sidebar where
  // it is -- and only the text itself runs right to left, which the browser
  // does on its own per line. Mirroring the whole page put every control on
  // the wrong side of the one the operator was comparing against.
  store.direction = 'ltr'
  document.documentElement.lang = locale
  document.documentElement.dir = 'ltr'
  try {
    localStorage.setItem(LOCALE_KEY, locale)
  } catch {
    /* not fatal: the choice just will not persist */
  }
}

// setLocale changes the interface language and remembers the choice. For a
// signed-in operator it is written to their account, so the language follows
// them to another browser and is not undone at the next sign-in -- which is
// what made a switch here look like it never stuck. Persisting is quiet on
// failure: the local change already took effect, and the account write is only
// about carrying it across sessions.
export async function setLocale(locale) {
  if (!locale || locale === store.locale) return
  await loadMessages(locale)
  if (store.admin) {
    try {
      store.admin = await api.updateMe({ locale })
    } catch {
      /* the local choice stands; it just will not follow to another browser */
    }
  }
}

// True once the operator has picked a language on the sign-in screen this
// session. It separates a deliberate choice from a tag merely left in local
// storage, so the choice carries into the session and onto the account while an
// account's stored language still wins when nobody picked. Reset after sign-in.
let localePicked = false

// pickLoginLocale is the sign-in screen's language switch: it changes the
// language now and marks the choice so signIn keeps it.
export async function pickLoginLocale(locale) {
  localePicked = true
  await loadMessages(locale)
}

export async function bootstrap() {
  store.meta = await api.meta()
  await loadMessages(store.locale)

  if (getToken()) {
    try {
      store.admin = await api.me()
      // The account's own language is authoritative once signed in, so a
      // reload lands on the language the operator chose, on any browser.
      if (store.admin?.locale && store.admin.locale !== store.locale) {
        await loadMessages(store.admin.locale)
      }
      loadPanelSettings()
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
  loadPanelSettings()
  // The server says more about itself to someone signed in -- the version,
  // the state of the kernel engines -- so ask again now that we are.
  api.meta({ background: true }).then((m) => { store.meta = m }).catch(() => {})
  if (localePicked && store.locale !== res.admin?.locale) {
    // They chose a language on the sign-in screen on purpose. Honour it and
    // write it to the account, so it is not undone here and follows them on.
    // (A language merely left over in local storage is not a choice, so an
    // account's own language still wins when nobody picked.)
    try {
      store.admin = await api.updateMe({ locale: store.locale })
    } catch {
      /* the local choice stands even if the account write did not */
    }
  } else if (res.admin?.locale && res.admin.locale !== store.locale) {
    await loadMessages(res.admin.locale)
  }
  localePicked = false
  return res.admin
}

export function signOut() {
  setToken(null)
  store.admin = null
}

let toastTimer = null

// kind is one of info, success, warning or error: the toast has a look for
// each, and anything else is drawn as plain info.
export function notify(message, kind = 'info') {
  // The expired-session error carries no message: it has already been
  // announced once, and a second empty toast on top of it is noise.
  if (!message) return

  store.toast = { message, kind }
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (store.toast = null), 4500)
}
