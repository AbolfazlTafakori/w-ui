import { onMounted, onUnmounted, ref, watch } from 'vue'

// Keep a page's data current without getting in the operator's way.
//
// A list of customers that was fetched once is a list that is wrong within
// seconds: somebody comes online, a quota runs down, another admin adds a row.
// The panel already recomputes all of that every couple of seconds on the
// server; the page just was not asking.
//
// Written once, here, rather than as a setInterval in each view. The last time
// a small thing was defined per-view it ended up defined twice and missing from
// four pages, which is a mistake worth only making once.
//
// Three rules the naive version gets wrong:
//
//   1. A hidden tab polls nothing. A panel left open on a second monitor should
//      not be the busiest client this server has.
//   2. Coming back to the tab refreshes at once rather than waiting out the
//      remainder of an interval, so what you look at is never stale.
//   3. While the operator is busy — a form open, a confirmation waiting — the
//      poll is skipped. Data moving under an open dialog is disorienting, and
//      worse, it can overwrite what they are in the middle of changing.
//
// The callback is always passed `true`, meaning "this is a poll": views use
// that to keep the navigation progress bar dark, since nobody is waiting on it.
export function useLive(fn, { every = 5000, busy = () => false } = {}) {
  let timer = null
  let running = false

  async function tick() {
    // Overlapping polls on a slow link would queue up and arrive together.
    if (running || document.hidden || busy()) return
    running = true
    try {
      await fn(true)
    } catch {
      // A failed poll is not worth interrupting anyone over. The page keeps
      // showing what it last had, which is better than an error where the data
      // used to be, and the next tick will try again.
    } finally {
      running = false
    }
  }

  function onVisibility() {
    if (document.hidden) return
    // Straight away, so returning to the tab never shows a stale reading.
    tick()
  }

  onMounted(() => {
    timer = setInterval(tick, every)
    document.addEventListener('visibilitychange', onVisibility)
  })

  onUnmounted(() => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', onVisibility)
  })

  // Returned so a view can refresh immediately after an action of its own.
  return { refresh: tick }
}

// Merge a freshly fetched list into the one on screen.
//
// Replacing the array wholesale would work and would also throw away anything
// the operator has in flight: a switch they just flipped that the server has
// not confirmed yet would snap back to its old value for one tick and then
// forward again. `skip` is the set of rows mid-request, and they are left
// exactly as they are until their own request settles.
//
// Rows that appeared or disappeared — another admin adding a customer, one
// being deleted — change the list itself, so that case replaces it.
export function mergeRows(current, incoming, skip = new Set()) {
  if (!Array.isArray(current) || !Array.isArray(incoming)) return incoming

  const sameSet =
    current.length === incoming.length &&
    current.every((row, i) => row.id === incoming[i].id)

  if (!sameSet) return incoming

  const byID = new Map(incoming.map((row) => [row.id, row]))
  for (const row of current) {
    if (skip.has(row.id)) continue
    const fresh = byID.get(row.id)
    if (fresh) Object.assign(row, fresh)
  }
  return current
}

// True only once `source()` has been true for `after` milliseconds.
//
// For loading indicators. A request that comes back in 40ms should leave the
// screen alone: showing a skeleton and removing it again inside two frames is
// a flash that reads as a rendering fault, and it is more distracting than the
// wait it was meant to cover.
//
// The delay is one-directional. Becoming busy waits; becoming idle is instant,
// because a spinner that lingers after the data has arrived is the one thing
// worse than a spinner that flashed.
export function useDelayed(source, { after = 160 } = {}) {
  const shown = ref(false)
  let timer = null

  watch(
    source,
    (busy) => {
      clearTimeout(timer)
      if (!busy) {
        shown.value = false
        return
      }
      timer = setTimeout(() => {
        shown.value = true
      }, after)
    },
    { immediate: true },
  )

  onUnmounted(() => clearTimeout(timer))
  return shown
}
