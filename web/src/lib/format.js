import { store } from './store.js'
// Formatting helpers. All of them take the active locale so Persian renders
// with its own digits and calendar conventions.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function bytes(n, locale = 'en') {
  const v = Number(n || 0)
  if (v === 0) return '0 B'

  let i = 0
  let scaled = v
  while (scaled >= 1024 && i < UNITS.length - 1) {
    scaled /= 1024
    i++
  }
  // Two decimals from GB up, so a figure moves by about ten megabytes;
  // below that, precision scales with the size.
  const digits = i >= 3 ? 2 : scaled < 10 && i > 0 ? 2 : scaled < 100 && i > 0 ? 1 : 0
  return `${scaled.toLocaleString(locale, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })} ${UNITS[i]}`
}

// bitrate uses decimal units because that is how link speeds are sold and how
// tc interprets a rate, unlike the binary units used for volume.
export function bitrate(bitsPerSec, locale = 'en') {
  const v = Number(bitsPerSec || 0)
  if (v === 0) return '—'
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps']
  let i = 0
  let scaled = v
  while (scaled >= 1000 && i < units.length - 1) {
    scaled /= 1000
    i++
  }
  return `${scaled.toLocaleString(locale, { maximumFractionDigits: 1 })} ${units[i]}`
}

export function gigabytesToBytes(gb) {
  const v = Number(gb)
  return Number.isFinite(v) && v > 0 ? Math.round(v * 1024 ** 3) : 0
}

// The data units a plan is sold in. Binary, as every panel and every client
// app counts them, so "1 GB" here is the 1 GB the customer's phone shows.
export const DATA_UNITS = { MB: 1024 ** 2, GB: 1024 ** 3, TB: 1024 ** 4 }

// quotaToUnit picks the unit a stored quota reads best in: whole numbers
// where they exist, otherwise the largest unit that leaves at most two
// decimals. 500 MB comes back as 500 MB, not 0.49 GB.
export function quotaToUnit(bytes) {
  const b = Number(bytes || 0)
  if (b <= 0) return { value: '', unit: 'GB' }
  for (const unit of ['TB', 'GB', 'MB']) {
    const v = b / DATA_UNITS[unit]
    if (v >= 1 && Math.abs(v - Math.round(v * 100) / 100) < 1e-9) return { value: +v.toFixed(2), unit }
  }
  return { value: +(b / DATA_UNITS.MB).toFixed(2), unit: 'MB' }
}

// unitToBytes turns what was typed, in the chosen unit, into bytes.
export function unitToBytes(value, unit) {
  const v = Number(value)
  if (!Number.isFinite(v) || v <= 0) return 0
  return Math.round(v * (DATA_UNITS[unit] || DATA_UNITS.GB))
}

// The time units a plan is sold in, in hours.
export const TIME_UNITS = { hours: 1, days: 24, months: 24 * 30 }

export function durationToUnit(hours) {
  const h = Number(hours || 0)
  if (h <= 0) return { value: '', unit: 'days' }
  for (const unit of ['months', 'days', 'hours']) {
    const v = h / TIME_UNITS[unit]
    if (v >= 1 && Math.abs(v - Math.round(v * 100) / 100) < 1e-9) return { value: +v.toFixed(2), unit }
  }
  return { value: +(h / TIME_UNITS.days).toFixed(2), unit: 'days' }
}

export function unitToHours(value, unit) {
  const v = Number(value)
  if (!Number.isFinite(v) || v <= 0) return 0
  return v * (TIME_UNITS[unit] || TIME_UNITS.days)
}

export function bytesToGigabytes(b) {
  const v = Number(b || 0)
  return v === 0 ? '' : +(v / 1024 ** 3).toFixed(2)
}

// calendarLocale is the locale dates are written in: the interface language,
// with the Persian calendar attached when the settings ask for it.
export function calendarLocale(locale = 'en') {
  let picker = 'gregorian'
  try {
    picker = store.panel?.datepicker || 'gregorian'
  } catch {
    /* the store is not up yet */
  }
  return picker === 'jalalian' ? 'fa-IR-u-ca-persian-nu-latn' : locale
}

export function date(value, locale = 'en') {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleDateString(calendarLocale(locale), { year: 'numeric', month: 'short', day: 'numeric' })
}

export function dateTime(value, locale = 'en') {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(calendarLocale(locale), {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// relative renders "in 12 days" / "3 hours ago", which is what an operator
// scanning an expiry column actually needs to know.
export function relative(value, locale = 'en') {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'

  const diffMs = d.getTime() - Date.now()
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  const table = [
    ['year', 365 * 24 * 3600e3],
    ['month', 30 * 24 * 3600e3],
    ['day', 24 * 3600e3],
    ['hour', 3600e3],
    ['minute', 60e3],
  ]
  for (const [unit, ms] of table) {
    if (Math.abs(diffMs) >= ms) return rtf.format(Math.round(diffMs / ms), unit)
  }
  return rtf.format(Math.round(diffMs / 1000), 'second')
}

export function percent(used, total) {
  const u = Number(used || 0)
  const t = Number(total || 0)
  if (t <= 0) return null
  return Math.min(100, Math.round((u / t) * 100))
}

// isOnline mirrors the server's rule: a peer counts as present if it handshook
// within the window after which WireGuard treats a session as stale.
export function isOnline(lastHandshake) {
  if (!lastHandshake) return false
  const d = new Date(lastHandshake)
  if (Number.isNaN(d.getTime())) return false
  return Date.now() - d.getTime() < 3 * 60 * 1000
}
