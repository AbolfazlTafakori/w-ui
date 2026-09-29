// Waiting for the panel to restart, and reloading onto it.
//
// Restoring a backup and restarting the panel both end the process and let the
// service manager start it again. Reloading after a guessed number of seconds
// landed on a closed port when the start was slow, or on the old process when
// it had not ended yet -- which then went away under the page. So this waits
// for the panel to stop answering and then to answer again, and only then
// reloads.
//
// /api/meta needs no session: a restored archive brings its own accounts, and
// any answer at all from the panel means it is back.

import { apiURL } from './api.js'

// How long a panel may take to come back before the page says it has not.
export const COME_BACK_MS = 3 * 60 * 1000

async function answers() {
  try {
    const res = await fetch(apiURL('/api/meta'), { credentials: 'same-origin', cache: 'no-store' })
    return res.status < 500
  } catch {
    return false
  }
}

// waitForRestart resolves true once the panel has gone away and come back, or
// false after COME_BACK_MS without that. reload: whether to reload the page
// when it is back (the default).
//
// A panel that is never seen to go away -- one that restarted faster than the
// first look -- counts as back after a few seconds of answering.
export async function waitForRestart({ reload = true } = {}) {
  const started = Date.now()
  let wentAway = false
  let answeringSince = 0
  while (Date.now() - started < COME_BACK_MS) {
    await new Promise((r) => setTimeout(r, 1000))
    if (!(await answers())) {
      wentAway = true
      answeringSince = 0
      continue
    }
    if (!answeringSince) answeringSince = Date.now()
    if (wentAway || Date.now() - answeringSince > 8000) {
      if (reload) window.location.reload()
      return true
    }
  }
  return false
}
