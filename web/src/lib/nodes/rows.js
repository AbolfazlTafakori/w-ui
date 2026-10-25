// How a node reads in its list, and the shape of its form: the figures over
// the list, how long ago it was seen, how long it has been up, how its
// latency is judged, and the form for adding or editing one with the request
// it becomes. Pure functions; `now` is passed in where time matters.
import { t, tn } from '../store.js'
import { bytesToGigabytes, gigabytesToBytes } from '../format.js'

// A 3x-ui or Pasarguard node: a panel with users of its own to bring in.
export const takesImport = (n) => n.kind === 'xui' || n.kind === 'pasarguard' || n.kind === 'remote'

export function totals(nodes) {
  const remote = nodes.filter((n) => n.kind !== 'local')
  // Averaged over the ones that answered: including a node that timed out
  // would report a latency nobody experienced.
  const live = remote.filter((n) => n.reachable && n.latencyMs > 0)
  return {
    all: nodes.length,
    online: nodes.filter((n) => n.kind === 'local' || n.reachable).length,
    offline: remote.filter((n) => !n.reachable).length,
    latency: live.length ? Math.round(live.reduce((a, n) => a + n.latencyMs, 0) / live.length) : null,
  }
}

export function ago(iso, now = Date.now()) {
  if (!iso) return t('node.never')
  const secs = Math.round((now - new Date(iso)) / 1000)
  if (secs < 60) return t('node.justNow')
  if (secs < 3600) return tn('node.minutesAgo', Math.round(secs / 60))
  if (secs < 86400) return tn('node.hoursAgo', Math.round(secs / 3600))
  return tn('node.daysAgo', Math.round(secs / 86400))
}

export function uptime(secs) {
  if (!secs) return '—'
  const d = Math.floor(secs / 86400)
  const h = Math.floor((secs % 86400) / 3600)
  return d > 0 ? `${d}d ${h}h` : `${h}h ${Math.floor((secs % 3600) / 60)}m`
}

// Latency is coloured the way a person judges it: usable, sluggish, painful.
export function latencyTone(ms) {
  if (!ms) return 'grey'
  if (ms < 120) return 'green'
  if (ms < 400) return 'orange'
  return 'red'
}

// The form for a node: blank to add one, or filled from one being edited.
// The token is deliberately blank on an edit: it is stored as given and
// never sent back, so the field means "replace it" rather than "here is
// what it is". The same for a sign-in password.
export function nodeForm(n = null) {
  if (!n) {
    return {
      kind: 'remote', name: '', address: '', token: '', note: '', username: '', password: '',
      usageCoefficient: 1, dataLimitGB: '', resetDay: '',
      tlsMode: 'verify', tlsPin: '', tlsServerName: '', allowPrivateAddress: false,
    }
  }
  return {
    kind: n.kind,
    username: n.username || '',
    password: '',
    name: n.name,
    address: n.address,
    token: '',
    note: n.note || '',
    usageCoefficient: n.usageCoefficient || 1,
    tlsMode: n.tlsMode || 'verify',
    allowPrivateAddress: !!n.allowPrivateAddress,
    tlsPin: n.tlsPin || '',
    tlsServerName: n.tlsServerName || '',
    dataLimitGB: bytesToGigabytes(n.dataLimitBytes),
    resetDay: n.resetDay || '',
  }
}

// What the form is sent as: numbers as numbers, text trimmed, and the
// allowance in bytes rather than the gigabytes it was typed in.
export function nodeBody(form) {
  return {
    ...form,
    usageCoefficient: Number(form.usageCoefficient) || 1,
    tlsMode: form.tlsMode || 'verify',
    allowPrivateAddress: !!form.allowPrivateAddress,
    tlsPin: (form.tlsPin || '').trim(),
    tlsServerName: (form.tlsServerName || '').trim(),
    dataLimitBytes: gigabytesToBytes(form.dataLimitGB),
    resetDay: Number(form.resetDay) || 0,
    dataLimitGB: undefined,
  }
}
