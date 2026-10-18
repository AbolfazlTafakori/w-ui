// What the settings page shares with its tabs: the one form it saves and
// tracks for changes, the subscription settings saved with it, and the
// helpers more than one tab needs. Provided by the page; each tab injects it,
// edits the same form, and the page's one Save takes it all.
import { inject, provide, ref } from 'vue'

const SETTINGS = Symbol('settings')

// The value a stored secret is shown as; the server never sends one back.
export const PLACEHOLDER = '********'

// ── event notifications, in their card grid ─────────────────────────────────
// Each group is a card; each event a checkbox, some with a threshold beside.
export const eventGroups = [
  { icon: 'TeamOutlined', title: 'set.eventGroupClients', events: [
    { key: 'exhausted', label: 'set.eventExhausted' },
    { key: 'expired', label: 'set.eventExpired' },
    { key: 'expiring', label: 'set.eventExpiring', setting: 'expireDiff', min: 0, max: 3650 },
    { key: 'sharing', label: 'set.eventSharing' },
  ] },
  { icon: 'CloudServerOutlined', title: 'set.eventGroupOutbound', events: [
    { key: 'outbound.down', label: 'set.eventOutboundDown', setting: 'notifyOutboundDownThreshold', min: 1, max: 100 },
    { key: 'outbound.up', label: 'set.eventOutboundUp' },
  ] },
  { icon: 'ThunderboltOutlined', title: 'set.eventGroupPanel', events: [
    { key: 'panel', label: 'set.eventPanel' },
    { key: 'backup', label: 'set.eventBackup' },
  ] },
  { icon: 'DesktopOutlined', title: 'set.eventGroupNode', events: [
    { key: 'node.down', label: 'set.eventNodeDown' },
    { key: 'node.up', label: 'set.eventNodeUp' },
  ] },
  { icon: 'DashboardOutlined', title: 'set.eventGroupSystem', events: [
    { key: 'cpu.high', label: 'set.eventCPUHigh', setting: 'notifyCPUThreshold', min: 0, max: 100 },
    { key: 'memory.high', label: 'set.eventMemoryHigh', setting: 'notifyMemoryThreshold', min: 0, max: 100 },
  ] },
  { icon: 'SafetyOutlined', title: 'set.eventGroupSecurity', events: [
    { key: 'login', label: 'set.eventLoginAttempt' },
  ] },
]

// URI paths begin and end with a slash, as theirs sanitise them.
export function sanitizePath(v) {
  v = String(v || '').replace(/[^A-Za-z0-9_\-/]/g, '')
  return v
}

// provideSettings hands the page's state to its tabs, with the helpers that
// read and write the form. form, saved, sub and subError are the page's refs.
export function provideSettings(state) {
  const { form, saved } = state
  // Theirs show a stored secret as "configured" and offer Clear.
  function secretConfigured(key) {
    return saved.value?.[key] === PLACEHOLDER && form.value?.[key] === PLACEHOLDER
  }
  function secretClearArmed(key) {
    return saved.value?.[key] === PLACEHOLDER && form.value?.[key] === ''
  }
  function toggleClear(key) {
    form.value[key] = secretClearArmed(key) ? PLACEHOLDER : ''
  }
  // Which events a channel is told of, by the form field that lists them.
  function kindsOf(field) {
    return form.value?.[field] || []
  }
  function hasKind(field, key) {
    return kindsOf(field).includes(key)
  }
  function toggleKind(field, key) {
    const set = new Set(kindsOf(field))
    set.has(key) ? set.delete(key) : set.add(key)
    form.value[field] = [...set]
  }
  function groupCount(field, g) {
    return g.events.filter((e) => hasKind(field, e.key)).length
  }
  function toggleGroup(field, g) {
    const keys = g.events.map((e) => e.key)
    const all = keys.every((k) => hasKind(field, k))
    const set = new Set(kindsOf(field))
    keys.forEach((k) => (all ? set.delete(k) : set.add(k)))
    form.value[field] = [...set]
  }
  const ctx = {
    ...state,
    showSecret: ref({}),
    secretConfigured, secretClearArmed, toggleClear,
    hasKind, toggleKind, groupCount, toggleGroup,
  }
  provide(SETTINGS, ctx)
  return ctx
}

export function useSettings() {
  return inject(SETTINGS)
}
