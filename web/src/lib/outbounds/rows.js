// How an outbound reads in its list: the ways a hop can be tested, how long
// ago its figure was taken, the country it leaves from, its row menu, and
// the shape the list is exported and imported in. Pure functions; `now`
// and the locale are passed in where they matter.
import { t, tn } from '../store.js'

export const MODES = [
  { value: 'tcp', label: 'TCP' },
  { value: 'http', label: 'HTTP' },
  { value: 'real', label: () => t('outbound.modeRealDelay') },
]
export function modeLabel(m) {
  const found = MODES.find((x) => x.value === m)
  return typeof found?.label === 'function' ? found.label() : found?.label || m.toUpperCase()
}

// A latency figure is only meaningful next to when it was taken. One from an
// hour ago next to a hop that has since died reads as proof it is fine.
export function checkedAgo(o, now = Date.now()) {
  if (!o.lastCheckAt) return ''
  const secs = Math.max(0, (now - new Date(o.lastCheckAt).getTime()) / 1000)
  if (secs < 60) return t('time.justNow')
  if (secs < 3600) return tn('time.minutesAgo', Math.floor(secs / 60))
  return tn('time.hoursAgo', Math.floor(secs / 3600))
}

// The country as a flag and a name in the operator's own language, the way
// theirs shows it. Two regional-indicator code points make the flag.
export function countryFlag(code) {
  const c = (code || '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(c)) return ''
  return String.fromCodePoint(...[...c].map((ch) => 0x1f1e6 + ch.charCodeAt(0) - 65))
}
export function countryName(code, locale = 'en') {
  const c = (code || '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(c)) return ''
  try {
    return new Intl.DisplayNames([locale || 'en'], { type: 'region' }).of(c) || c
  } catch {
    return c
  }
}

// Their items, in their order: Move to top (only past the first row), Move
// up, Move down, Reset Traffic, Delete. Enable is ours; theirs has no switch
// on an outbound, so it sits here rather than in a column they do not have.
export function menuFor(o, idx, count) {
  const items = []
  if (idx > 0) items.push({ key: 'top', icon: 'VerticalAlignTopOutlined', label: t('outbound.moveToTop') })
  items.push(
    { key: 'up', icon: 'ArrowUpOutlined', label: t('outbound.moveUp'), disabled: idx === 0 },
    { key: 'down', icon: 'ArrowDownOutlined', label: t('outbound.moveDown'), disabled: idx === count - 1 },
    { key: 'reset', icon: 'RetweetOutlined', label: t('outbound.resetTraffic') },
    {
      key: 'toggle',
      icon: o.enabled ? 'StopOutlined' : 'PlayCircleOutlined',
      label: o.enabled ? t('action.disable') : t('action.enable'),
      disabled: o.builtin,
    },
    { divider: true },
    { key: 'del', icon: 'DeleteOutlined', label: t('action.delete'), danger: true, disabled: o.builtin },
  )
  return items
}

// What leaves is what can be re-created: the two built-ins are skipped
// because every panel already has them, and secrets are not in the listing
// to begin with. Their export is the same shape -- an array of outbounds.
export function exportText(outbounds) {
  return JSON.stringify(
    outbounds
      .filter((o) => !o.builtin)
      .map((o) => ({
        tag: o.tag,
        kind: o.kind,
        enabled: o.enabled,
        address: o.address,
        username: o.username || undefined,
        peerPubKey: o.peerPubKey || undefined,
        hopAddress: o.hopAddress || undefined,
        hopDns: o.hopDns || undefined,
        hopMtu: o.hopMtu || undefined,
        note: o.note || undefined,
      })),
    null,
    2,
  )
}

// What a paste brings in: an array, or an object carrying one under
// "outbounds" -- both forms theirs accepts. Null when it is neither, or not
// JSON at all; anything in the list that is not an object is left out.
export function parseImport(text) {
  let parsed
  try {
    parsed = JSON.parse(text)
  } catch {
    return null
  }
  const list = Array.isArray(parsed) ? parsed : Array.isArray(parsed?.outbounds) ? parsed.outbounds : null
  return list && list.filter((item) => item && typeof item === 'object')
}
