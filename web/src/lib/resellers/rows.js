// How a reseller reads in the owner's list: how much of what they bought is
// left -- customers, traffic, days -- in the colours the customer list uses,
// where they stand in one word, which of the four filters they fall in, the
// orders the list sorts in, and the totals across the top. Pure functions of
// the rows; `now` is passed in where time matters, so a test can fix it.
import { bytes, dateTime } from '../format.js'
import { store, t } from '../store.js'

export const DAY = 86400e3
export const WARN_DAYS = 7
export const WARN_PERCENT = 85
export const CHIP_LIMIT = 2
export const STATUS_FILTERS = ['active', 'hold', 'soon', 'stopped']
export const SORTS = ['name', 'customers', 'traffic', 'timeLeft', 'newest']

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

export function usedPercent(a) {
  return a.quotaBytes ? Math.min(100, Math.round((a.usedBytes / a.quotaBytes) * 100)) : null
}
export function daysLeft(a, now = Date.now()) {
  if (!a.expiresAt) return null
  return Math.ceil((new Date(a.expiresAt).getTime() - now) / DAY)
}

// One word for where a reseller stands, first reason first: the owner's
// switch, then the date, then the traffic, then a term not yet started.
export function standing(a, now = Date.now()) {
  if (!a.enabled) return { key: 'off', color: '', label: t('admins.switchedOff') }
  if (a.role !== 'reseller') return { key: 'active', color: 'green', label: t('status.active') }
  const d = daysLeft(a, now)
  if (d !== null && d <= 0) return { key: 'ended', color: 'red', label: t('admins.termEnded') }
  if (a.quotaBytes && a.usedBytes >= a.quotaBytes) return { key: 'spent', color: 'red', label: t('admins.allowanceSpent') }
  if (!a.expiresAt && a.durationDays > 0) return { key: 'hold', color: 'blue', label: t('status.onHold') }
  if ((d !== null && d <= WARN_DAYS) || (usedPercent(a) ?? 0) >= WARN_PERCENT) {
    return { key: 'soon', color: 'orange', label: t('admins.endingSoon') }
  }
  return { key: 'active', color: 'green', label: t('status.active') }
}

export function trafficLeftTag(a) {
  if (!a.quotaBytes) return { color: 'purple', label: '∞' }
  const p = usedPercent(a)
  const label = bytes(Math.max(0, a.quotaBytes - a.usedBytes), store.locale)
  if (p >= 100) return { color: 'red', label }
  if (p >= WARN_PERCENT) return { color: 'orange', label }
  return { color: 'green', label }
}
export function barColor(a) {
  if (!a.enabled) return 'var(--faint)'
  if (!a.quotaBytes) return 'rgba(114, 46, 209, 0.35)'
  const p = usedPercent(a) ?? 0
  if (p >= 100) return 'var(--bad)'
  if (p >= WARN_PERCENT) return 'var(--warn)'
  return 'var(--ok)'
}
export function termTag(a, now = Date.now()) {
  if (!a.expiresAt && a.durationDays > 0) {
    return { color: 'blue', label: t('admins.daysOnHold', { n: nf(a.durationDays) }), title: t('admins.onHoldHint') }
  }
  const d = daysLeft(a, now)
  if (d === null) return { color: 'purple', label: '∞', title: t('admins.noEnd') }
  const title = dateTime(a.expiresAt, store.locale)
  if (d <= 0) return { color: 'red', label: t('admins.termEnded'), title }
  return { color: d <= WARN_DAYS ? 'orange' : 'green', label: t('admins.daysLeft', { n: nf(d) }), title }
}
export function customersTag(a) {
  // The limit counts users -- a customer for four users is four -- so
  // that is what it is shown against.
  if (!a.clientLimit) return { color: '', label: `${nf(a.users)} / ∞` }
  const full = a.users >= a.clientLimit
  return { color: full ? 'orange' : '', label: `${nf(a.users)} / ${nf(a.clientLimit)}` }
}

// Which of the four a reseller falls in. An administrator is active or
// switched off and nothing else: they have no ceiling to run into.
export function bucket(a, now = Date.now()) {
  const k = standing(a, now).key
  if (k === 'off' || k === 'ended' || k === 'spent') return 'stopped'
  return k
}

// The rows a search, a status and a server leave, in the order picked.
// Unlimited sorts last on "time left" and first on nothing else: it is the
// one that will never need renewing.
export function shownRows(rows, { search = '', status = '', server = '', sort = 'name' }, now = Date.now()) {
  const q = search.trim().toLowerCase()
  const list = rows.filter((a) => {
    if (status && bucket(a, now) !== status) return false
    if (server && !(a.interfaceIds || []).includes(server)) return false
    if (!q) return true
    return [a.username, a.note, a.groupName].some((v) => (v || '').toLowerCase().includes(q))
  })
  const left = (a) => (a.expiresAt ? new Date(a.expiresAt).getTime() : a.durationDays > 0 ? now + a.durationDays * DAY : Infinity)
  const by = {
    name: (x, y) => x.username.localeCompare(y.username),
    customers: (x, y) => (y.users || 0) - (x.users || 0),
    traffic: (x, y) => (y.usedBytes || 0) - (x.usedBytes || 0),
    timeLeft: (x, y) => left(x) - left(y),
    newest: (x, y) => new Date(y.createdAt || 0) - new Date(x.createdAt || 0),
  }[sort] || (() => 0)
  return [...list].sort((x, y) => by(x, y) || x.username.localeCompare(y.username))
}

// The figures across the top: every row is counted, but only resellers are
// split by standing and summed, an administrator having no allowance.
export function summarize(rows, now = Date.now()) {
  const list = rows.filter((a) => a.role === 'reseller')
  const by = (key) => list.filter((a) => standing(a, now).key === key).length
  return {
    total: rows.length,
    active: by('active') + by('hold'),
    soon: by('soon'),
    stopped: by('off') + by('ended') + by('spent'),
    customers: list.reduce((n, a) => n + (a.clients || 0), 0),
    traffic: list.reduce((n, a) => n + (a.usedBytes || 0), 0),
  }
}
