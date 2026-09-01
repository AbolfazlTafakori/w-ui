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
  const digits = scaled < 10 && i > 0 ? 2 : scaled < 100 && i > 0 ? 1 : 0
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

export function bytesToGigabytes(b) {
  const v = Number(b || 0)
  return v === 0 ? '' : +(v / 1024 ** 3).toFixed(2)
}

export function date(value, locale = 'en') {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleDateString(locale, { year: 'numeric', month: 'short', day: 'numeric' })
}

export function dateTime(value, locale = 'en') {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(locale, {
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
