// Light, dark and pure black, cycled from the sidebar.
//
// The mode is applied to the document rather than held in a component, so it is
// set before the first paint and the panel never flashes the wrong colours on
// its way to the right ones.

import { ref } from 'vue'

const KEY = 'wui.theme'
const MODES = ['dark', 'ultra', 'light']

export const themeMode = ref(read())

function read() {
  try {
    const saved = localStorage.getItem(KEY)
    if (MODES.includes(saved)) return saved
  } catch {
    // Private windows and blocked site data both throw. Falling back to the
    // default is a much smaller problem than failing to start.
  }
  return 'dark'
}

// apply writes the mode onto the document.
//
// Two hooks rather than one, because they answer different questions: the body
// class says which way round the palette runs, and the attribute says whether
// this is the pure-black variant of it.
export function apply(mode) {
  const root = document.documentElement
  const body = document.body

  // Both elements. Anything that reads a token at :root level — the page
  // background among them — resolves against html, and setting only body left
  // those on the palette they had before.
  const name = mode === 'light' ? 'light' : 'dark'
  for (const el of [root, body]) {
    el.classList.remove('dark', 'light')
    el.classList.add(name)
  }

  if (mode === 'ultra') root.setAttribute('data-theme', 'ultra-dark')
  else root.removeAttribute('data-theme')

  // So a browser paints form controls and scrollbars to match rather than
  // leaving a white scrollbar down the side of a black page.
  root.style.colorScheme = mode === 'light' ? 'light' : 'dark'
}

export function setTheme(mode) {
  if (!MODES.includes(mode)) return
  themeMode.value = mode
  apply(mode)
  try {
    localStorage.setItem(KEY, mode)
  } catch {
    /* see above: the choice simply does not survive a reload */
  }
}

// cycleTheme steps through the three in a fixed order, so the button is
// predictable rather than a menu that has to be opened to be understood.
export function cycleTheme() {
  const next = MODES[(MODES.indexOf(themeMode.value) + 1) % MODES.length]
  setTheme(next)
}

// initTheme runs before the app mounts.
export function initTheme() {
  apply(themeMode.value)
}
