// How a customer's row reads at a glance: whether they are online, what they
// are moving, and the colour and words of their status, what is left, and
// when they run out. Pure functions of the customer, so the list and the
// cards say the same, and tests can hold them to the server's own lines.
import { bytes, dateTime, percent, relative } from './format.js'
import { store, t } from './store.js'

// Online is what the reconciler sees moving right now -- bytes in the last
// window, counted every two seconds -- rather than a handshake written to
// the database a flush later. A device that connects shows within a poll.
export const clientOnline = (c) => (c.onlineNow || 0) > 0
export const usedPercent = (c) => percent(c.usedBytes, c.quotaBytes)

// What the customer is moving right now, each way, as the server measures it
// from the kernel's own counters every tick -- up is what they send, down
// what they receive. Absent while they move nothing. It used to be worked out
// here from two readings of the stored total, inside the render: every
// re-render took a new reading, so the column read "—" nearly always.
export const speedActive = (c) => !!c.speed && (c.speed.up > 0 || c.speed.down > 0)
export function speedText(c) {
  const rate = (v) => `${bytes(v || 0, store.locale)}/s`
  return `↑ ${rate(c.speed.up)} / ↓ ${rate(c.speed.down)}`
}

// Their traffic bar: green while there is room, orange within the warning
// band, red when out; a disabled customer's bar is grey; unlimited is a
// purple wash.
export function barColor(c) {
  if (c.status === 'disabled') return 'var(--faint)'
  if (!c.quotaBytes) return 'rgba(114, 46, 209, 0.35)'
  const p = usedPercent(c) ?? 0
  if (p >= 100) return 'var(--bad)'
  if (p >= DEPLETING_AT) return 'var(--warn)'
  return 'var(--ok)'
}
// The three cells below use the classic panel's own colour rules, so a row reads at a
// glance: purple is unlimited, green healthy, orange running low, red stopped.

export function statusTag(c) {
  // Ended plans say why: out of data, or past their date. The switch beside
  // them is off, because the panel switched them off.
  if (c.status === 'exhausted') return { color: 'red', label: t('status.exhausted') }
  if (c.status === 'expired') return { color: 'red', label: t('status.expired') }
  // Their own plan is fine but the reseller who sold it is paused, so they
  // cannot connect. Said here, or "Active" would be a lie told on every row.
  // One short word in the column; the reason is on the tag's tooltip.
  if (c.ownerPaused) {
    return {
      color: 'red',
      label: t('client.paused'),
      // The switch is shown off while paused, so the tooltip is where their
      // own setting -- what they return to -- is still told.
      title: (store.admin?.role === 'reseller' ? t('client.accountPausedHint') : t('client.resellerPausedHint'))
        + ' ' + (c.status === 'disabled' ? t('client.pausedStaysOff') : t('client.pausedComesBack')),
    }
  }
  if (c.status !== 'disabled' && clientOnline(c)) return { color: 'green', label: t('status.online'), dot: true }
  if (c.status === 'disabled') return { color: 'grey', label: t('status.disabled') }
  // A plan that has not started: on hold until the first connection.
  if (!c.expiresAt && c.startOnFirstUse && c.durationDays > 0 && !c.activatedAt) {
    return { color: 'blue', label: t('status.onHold') }
  }
  // "Running low" earns its own state: it is the moment to sell a renewal,
  // which is worth surfacing before the customer notices anything.
  const p = usedPercent(c)
  if (p != null && p >= DEPLETING_AT) return { color: 'orange', label: t('stat.depleting') }
  return { color: 'grey', label: t('status.offline') }
}

// The server's own line for running low (depletingPercent in the clients
// service), so a row turns orange exactly when the summary above and the
// Running low filter start counting it.
export const DEPLETING_AT = 80

export function remainingTag(c) {
  if (!c.quotaBytes) return { color: 'purple', label: '∞' }
  const p = usedPercent(c)
  const left = bytes(Math.max(0, c.quotaBytes - c.usedBytes), store.locale)
  if (p >= 100) return { color: 'red', label: left }
  if (p >= DEPLETING_AT) return { color: 'orange', label: left }
  return { color: 'green', label: left }
}

export function expiryTag(c) {
  // A plan waiting for its first connection has no date yet. Showing the
  // unlimited mark here would read as "never expires", which is the opposite
  // of a thirty-day plan that simply has not started.
  if (!c.expiresAt && c.startOnFirstUse && c.durationDays > 0) {
    return { color: 'blue', label: `${c.durationDays}d`, title: t('client.notStartedHint') }
  }
  if (!c.expiresAt) return { color: 'purple', label: '∞' }
  const ms = new Date(c.expiresAt) - Date.now()
  const label = relative(c.expiresAt, store.locale)
  if (ms <= 0) return { color: 'red', label }
  if (ms < 3 * 86400e3) return { color: 'orange', label }
  return { color: 'green', label }
}

// The tooltip has to agree with the tag: "never expires" under a badge that
// says 30d would leave the operator with two different answers.
// Their tooltip on the status: when the customer was last seen.
export function lastOnlineTitle(c) {
  let last = null
  for (const a of c.accounts || []) {
    if (a.lastHandshake && (!last || a.lastHandshake > last)) last = a.lastHandshake
  }
  return `${t('client.menu.lastOnline')}: ${last ? dateTime(last, store.locale) : '-'}`
}

export function expiryTitle(c) {
  if (c.expiresAt) return dateTime(c.expiresAt, store.locale)
  if (c.startOnFirstUse && c.durationDays > 0) return t('client.notStartedHint')
  return t('client.neverExpires')
}

// How many of a customer's tunnels or groups a row names before the rest fold
// into a +N, so a customer on five is still one row high.
export const INBOUND_CHIP_LIMIT = 1

// The tunnels a customer is on, each once, as the row's chips.
export function inboundChips(c, interfaces = []) {
  const names = []
  const seen = new Set()
  for (const a of c.accounts || []) {
    if (seen.has(a.interfaceId)) continue
    seen.add(a.interfaceId)
    const i = interfaces.find((x) => x.id === a.interfaceId)
    names.push({ name: i?.name || `#${a.interfaceId}`, protocol: i?.protocol || c.protocol })
  }
  return names
}
