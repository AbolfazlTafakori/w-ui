<script setup>
// The settings page, laid out the way 3x-ui lays its own out: a card with
// Save and Restart Panel and the standing warning, then a card holding the
// category the sidebar chose -- General, Authentication, Telegram Bot, Email,
// Subscription -- each a row of tabs over lists of setting rows.
import { computed, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { api, apiURL, getToken } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { makeQR } from '../lib/qr.js'
import { relative } from '../lib/format.js'
import { store, t, loadMessages, loadPanelSettings, notify, signOut } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import Toggle from '../components/Toggle.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'

const router = useRouter()

// ── which page ──────────────────────────────────────────────────────────────
const categories = ['general', 'security', 'telegram', 'email', 'subscription']
// Pages this view still renders that are not in the settings menu: reached
// from Maintenance and the overview.
const asideTabs = ['backups', 'engine', 'logs', 'system']
const props = defineProps({ tab: { type: String, default: '' } })

function known(slug) {
  if (slug === 'notify') slug = 'telegram' // the old name of the page
  return categories.includes(slug) || asideTabs.includes(slug) ? slug : ''
}
function tabFromHash() {
  return known((location.hash || '').replace(/^#/, '')) || 'general'
}
const active = ref(known(props.tab) || tabFromHash())
watch(
  () => props.tab,
  (v) => {
    const next = known(v)
    if (next && next !== active.value) {
      active.value = next
      if (next === 'logs') loadLogs()
    }
  },
)
window.addEventListener('hashchange', () => {
  if (!props.tab) active.value = tabFromHash()
})

// The tab inside each category, as theirs: each remembers its own.
const inner = ref({ general: '1', security: '1', telegram: '1', email: '1', subscription: '1' })

// ── the settings themselves ─────────────────────────────────────────────────
const info = ref(null)
const loading = ref(true)
const loadError = ref(null)
// `saved` is what the server last confirmed; `form` is what is on screen.
const saved = ref(null)
const defaults = ref(null)
const form = ref(null)
const showWait = useDelayed(computed(() => loading.value && !form.value))
const busy = ref(false)

// The subscription service is stored on its own; it rides along with the
// one Save so the page has one button, as theirs has.
const sub = ref(null)
const subSaved = ref(null)
const subError = ref({})

// The looks the customer's page can wear. Chosen here; every customer's
// page follows on its next opening.
const SUB_TEMPLATES = ['classic', 'aurora', 'waves', 'network', 'minimal', 'midnight']
// A one-time link, opened in a new tab: the page is served without the
// panel's session, so the tab cannot carry it.
async function previewTemplate(key) {
  try {
    const res = await api.post('/api/subscription/preview', { template: key })
    window.open(res.url, '_blank', 'noopener')
  } catch (e) {
    notify(e.message, 'error')
  }
}

const outboundTags = ref([])
const balancerTags = ref([])

onMounted(() => {
  load()
  loadTokens()
  loadTargets()
})

async function load() {
  loading.value = true
  try {
    const [sys, cfg, subCfg] = await Promise.all([
      api.get('/api/system'),
      api.get('/api/settings'),
      api.get('/api/subscription', { background: true }).catch(() => null),
    ])
    info.value = sys
    saved.value = cfg.settings
    defaults.value = cfg.defaults
    form.value = clone(cfg.settings)
    if (subCfg) {
      subSaved.value = subCfg
      sub.value = clone(subCfg)
    }
    await loadBackups()
    await loadMe()
    loadError.value = null
  } catch (e) {
    loadError.value = e
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}

async function loadTargets() {
  try {
    const obs = await api.get('/api/outbounds', { background: true })
    outboundTags.value = (obs.items || obs || []).filter((o) => o.kind !== 'block').map((o) => o.tag)
  } catch {
    outboundTags.value = []
  }
  try {
    const bs = await api.get('/api/balancers', { background: true })
    balancerTags.value = (bs.items || bs || []).map((b) => b.tag)
  } catch {
    balancerTags.value = []
  }
}

const clone = (v) => JSON.parse(JSON.stringify(v))
const same = (a, b) => JSON.stringify(a) === JSON.stringify(b)
const panelDirty = computed(() => !!form.value && !!saved.value && !same(form.value, saved.value))
const subDirty = computed(() => !!sub.value && !!subSaved.value && !same(sub.value, subSaved.value))
const dirty = computed(() => panelDirty.value || subDirty.value)

function num(v, fallback = 0) {
  const n = Number(v)
  return Number.isFinite(n) ? n : fallback
}

async function save() {
  busy.value = true
  subError.value = {}
  try {
    if (panelDirty.value) {
      const f = form.value
      const res = await api.put('/api/settings', {
        ...f,
        webPort: num(f.webPort),
        sessionMaxAge: num(f.sessionMaxAge, 720),
        pageSize: num(f.pageSize, 25),
        expireDiff: num(f.expireDiff),
        trafficDiff: num(f.trafficDiff),
        defaultQuotaBytes: num(f.defaultQuotaBytes),
        defaultExpiryDays: num(f.defaultExpiryDays),
        defaultDeviceLimit: num(f.defaultDeviceLimit, 1),
        defaultRateBitsPerSec: num(f.defaultRateBitsPerSec),
        backupEveryHours: num(f.backupEveryHours),
        backupKeep: num(f.backupKeep),
        mailPort: num(f.mailPort),
        notifyCPUThreshold: num(f.notifyCPUThreshold),
        notifyMemoryThreshold: num(f.notifyMemoryThreshold),
        notifyOutboundDownThreshold: num(f.notifyOutboundDownThreshold),
      })
      saved.value = res.settings
      defaults.value = res.defaults
      form.value = clone(res.settings)
      loadPanelSettings()
      if (res.settings.defaultLocale !== store.locale) await loadMessages(res.settings.defaultLocale)
    }
    if (subDirty.value) {
      const s = sub.value
      const res = await api.put('/api/subscription', { ...s, port: num(s.port), updateHours: num(s.updateHours, 12) })
      subSaved.value = res
      sub.value = clone(res)
    }
    notify(t('settings.saved'), 'success')
  } catch (e) {
    if (e.field) subError.value = { [e.field]: e.message }
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

// Restart Panel: only when nothing is unsaved, as theirs -- a restart
// applies what was saved, so it would otherwise throw away the edit.
const askRestart = ref(false)
const restarting = ref(false)
async function restartPanel() {
  askRestart.value = false
  restarting.value = true
  try {
    await api.post('/api/panel/restart')
  } catch (e) {
    restarting.value = false
    notify(e.message, 'error')
    return
  }
  // Wait for it to come back, then reload onto whatever address it now has.
  const started = Date.now()
  const probe = async () => {
    try {
      await api.get('/api/meta', { background: true })
      window.location.reload()
    } catch {
      if (Date.now() - started < 60000) setTimeout(probe, 1500)
      else restarting.value = false
    }
  }
  setTimeout(probe, 2500)
}

const pendingRoute = ref(null)
const leaving = ref(false)
onBeforeRouteLeave((to) => {
  if (!dirty.value || leaving.value) return true
  pendingRoute.value = to.fullPath
  return false
})
async function discardAndGo() {
  leaving.value = true
  const to = pendingRoute.value
  pendingRoute.value = null
  await router.push(to)
  leaving.value = false
}
async function saveAndGo() {
  await save()
  if (!dirty.value) await discardAndGo()
  else pendingRoute.value = null
}

// A value equal to what the panel ships as gets their "Default" tag.
function isDefault(key) {
  if (!form.value || !defaults.value) return false
  return form.value[key] === defaults.value[key]
}

// Quota and rate are stored in bytes and bits. Nobody types either.
const quotaGB = computed({
  get: () => (form.value?.defaultQuotaBytes ? form.value.defaultQuotaBytes / 1024 ** 3 : 0),
  set: (v) => {
    form.value.defaultQuotaBytes = Math.max(0, Math.round(Number(v) * 1024 ** 3)) || 0
  },
})
const rateMbit = computed({
  get: () => (form.value?.defaultRateBitsPerSec ? form.value.defaultRateBitsPerSec / 1e6 : 0),
  set: (v) => {
    form.value.defaultRateBitsPerSec = Math.max(0, Math.round(Number(v) * 1e6)) || 0
  },
})

// URI paths begin and end with a slash, as theirs sanitise them.
function sanitizePath(v) {
  v = String(v || '').replace(/[^A-Za-z0-9_\-/]/g, '')
  return v
}

// ── security warnings ────────────────────────────────────────────────────────
const alertVisible = ref(true)
const warnings = ref([])
onMounted(async () => {
  try {
    const res = await api.get('/api/security/warnings', { background: true })
    warnings.value = (res.warnings || []).map((w) => w.title || w.detail || String(w))
  } catch {
    warnings.value = []
  }
})

// ── secrets: theirs show "configured" and offer Clear ────────────────────────
const PLACEHOLDER = '********'
function secretConfigured(key) {
  return saved.value?.[key] === PLACEHOLDER && form.value?.[key] === PLACEHOLDER
}
function secretClearArmed(key) {
  return saved.value?.[key] === PLACEHOLDER && form.value?.[key] === ''
}
function toggleClear(key) {
  form.value[key] = secretClearArmed(key) ? PLACEHOLDER : ''
}
const showSecret = ref({})

// ── event notifications, in their card grid ─────────────────────────────────
// Each group is a card; each event a checkbox, some with a threshold beside.
const eventGroups = [
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

// ── notification time: their preset select, interval or crontab ─────────────
const notifyMode = computed({
  get: () => {
    const v = form.value?.notifyRunTime || ''
    if (v.startsWith('@every ')) return 'every'
    if (['@hourly', '@daily', '@weekly', '@monthly'].includes(v)) return v
    return 'custom'
  },
  set: (m) => {
    if (m === 'every') form.value.notifyRunTime = '@every 30m'
    else if (m === 'custom') form.value.notifyRunTime = '0 30 8 * * *'
    else form.value.notifyRunTime = m
  },
})
const everyNum = computed({
  get: () => Number((form.value?.notifyRunTime || '').replace('@every ', '').replace(/[a-z]+$/, '')) || 1,
  set: (n) => {
    form.value.notifyRunTime = `@every ${Math.max(1, Math.round(Number(n) || 1))}${everyUnit.value}`
  },
})
const everyUnit = computed({
  get: () => (form.value?.notifyRunTime || '').replace('@every ', '').replace(/^\d+/, '') || 'm',
  set: (u) => {
    form.value.notifyRunTime = `@every ${everyNum.value}${u}`
  },
})

// ── tests ────────────────────────────────────────────────────────────────────
// Saved first, because the server tests what it has stored.
const testLoading = ref(false)
const testResult = ref(null)
async function testTgBot() {
  testLoading.value = true
  testResult.value = null
  try {
    if (dirty.value) await save()
    const res = await api.post('/api/settings/notify/test')
    testResult.value = { success: !!res.ok, msg: res.ok ? t('settings.testOk') : res.error }
  } catch (e) {
    testResult.value = { success: false, msg: e.message }
  } finally {
    testLoading.value = false
  }
}
const mailTesting = ref(false)
const mailResult = ref(null)
async function testMail() {
  mailTesting.value = true
  mailResult.value = null
  try {
    if (dirty.value) await save()
    const res = await api.post('/api/settings/mail/test')
    mailResult.value = { success: !!res.ok, msg: res.ok ? t('settings.testOk') : res.error }
  } catch (e) {
    mailResult.value = { success: false, msg: e.message }
  } finally {
    mailTesting.value = false
  }
}

// ── security: credentials ────────────────────────────────────────────────────
const user = ref({ oldUsername: '', oldPassword: '', newUsername: '', newPassword: '' })
const updating = ref(false)
async function updateUser() {
  const u = user.value
  if (!u.oldPassword || (!u.newUsername && !u.newPassword)) {
    notify(t('settings.fillCredentials'), 'error')
    return
  }
  updating.value = true
  try {
    await api.post('/api/auth/password', {
      currentPassword: u.oldPassword,
      newUsername: u.newUsername,
      newPassword: u.newPassword,
    })
    notify(t('set.credentialsChanged'), 'success')
    signOut()
    router.push('/login')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    updating.value = false
  }
}

// ── security: two-factor, in their modal ─────────────────────────────────────
const twoFactor = ref(false)
const tfa = ref(null) // { type: 'set'|'delete', token, uri, qr, code }
async function loadMe() {
  try {
    const me = await api.get('/api/auth/me')
    twoFactor.value = !!me.twoFactor
  } catch {
    /* the page is still usable without it */
  }
}
async function toggleTwoFactor() {
  if (twoFactor.value) {
    tfa.value = { type: 'delete', code: '' }
    return
  }
  try {
    const res = await api.post('/api/auth/totp/start')
    const qr = await makeQR(res.uri)
    tfa.value = { type: 'set', token: res.secret, uri: res.uri, qr, code: '' }
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function confirmTfa() {
  const m = tfa.value
  try {
    if (m.type === 'set') {
      await api.post('/api/auth/totp/confirm', { secret: m.token, code: m.code })
      twoFactor.value = true
      notify(t('settings.twoFactorEnabled'), 'success')
    } else {
      await api.post('/api/auth/totp/disable', { code: m.code, password: m.password || '' })
      twoFactor.value = false
      notify(t('settings.twoFactorDisabled'), 'success')
    }
    tfa.value = null
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('share.copyFailed'), 'error')
  }
}

// ── security: API tokens ─────────────────────────────────────────────────────
const tokens = ref([])
const tokensLoading = ref(false)
const createOpen = ref(false)
const createName = ref('')
const creating = ref(false)
const createdToken = ref(null)
const revoking = ref(null)

async function loadTokens() {
  tokensLoading.value = true
  try {
    tokens.value = await api.get('/api/tokens')
  } catch {
    tokens.value = []
  } finally {
    tokensLoading.value = false
  }
}
async function confirmCreateToken() {
  if (!createName.value.trim()) return
  creating.value = true
  try {
    const res = await api.post('/api/tokens', { name: createName.value.trim() })
    createdToken.value = res
    createOpen.value = false
    createName.value = ''
    await loadTokens()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    creating.value = false
  }
}
async function toggleTokenEnabled(tk) {
  try {
    await api.patch(`/api/tokens/${tk.id}`, { enabled: !!tk.disabled })
    await loadTokens()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function doRevoke() {
  const tk = revoking.value
  revoking.value = null
  try {
    await api.del(`/api/tokens/${tk.id}`)
    notify(t('settings.tokenRevoked', { n: tk.name }), 'success')
    await loadTokens()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function tokenDate(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(store.locale)
}

// ── mTLS trust for this panel as a node ──────────────────────────────────────
const mtlsTrust = ref('')
const mtlsBusy = ref(false)
async function saveMtlsTrust() {
  mtlsBusy.value = true
  try {
    const res = await api.post('/api/nodes/mtls/trust', { caCert: mtlsTrust.value.trim() })
    notify(res?.required ? t('settings.mtlsTrustSaved') : t('settings.mtlsTrustCleared'), 'ok')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    mtlsBusy.value = false
  }
}

// ── the pages outside the menu: backups, engine, logs, system ────────────────
const backups = ref([])
const backupBusy = ref(false)
const restoring = ref(null)
const uploading = ref(false)
const ask = ref(null)

async function loadBackups() {
  try {
    backups.value = await api.get('/api/backups')
  } catch {
    backups.value = []
  }
}
async function makeBackup() {
  backupBusy.value = true
  try {
    await api.post('/api/backups')
    await loadBackups()
    notify(t('settings.backupTaken'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    backupBusy.value = false
  }
}
async function removeBackup(name) {
  try {
    await api.del(`/api/backups/${encodeURIComponent(name)}`)
    await loadBackups()
  } catch (e) {
    notify(e.message, 'error')
  }
}
// The archive holds every key and credential, so it is fetched with the
// session token rather than linked.
async function downloadBackup(name) {
  try {
    const res = await fetch(apiURL(`/api/backups/${encodeURIComponent(name)}`), {
      headers: { Authorization: `Bearer ${getToken()}` },
      credentials: 'same-origin',
    })
    if (!res.ok) throw new Error(await res.text())
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function restoreBackup(name) {
  restoring.value = name
  try {
    const res = await api.post(`/api/backups/${encodeURIComponent(name)}/restore`)
    notify(t('settings.restoreStarted', { n: res?.safetyCopy || '' }), 'success')
    setTimeout(() => window.location.reload(), 6000)
  } catch (e) {
    restoring.value = null
    notify(e.message, 'error')
  }
}
async function uploadBackup(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return
  uploading.value = true
  try {
    const body = new FormData()
    body.append('archive', file)
    const res = await fetch(apiURL('/api/backups/upload'), {
      method: 'POST',
      headers: { Authorization: `Bearer ${getToken()}` },
      credentials: 'same-origin',
      body,
    })
    const data = await res.json().catch(() => null)
    if (!res.ok) throw new Error(data?.error || t('settings.uploadFailed'))
    await loadBackups()
    notify(t('settings.uploaded'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    uploading.value = false
  }
}

const logs = ref([])
const logLevel = ref('')
const logsBusy = ref(false)
async function loadLogs() {
  logsBusy.value = true
  try {
    const res = await api.get(`/api/logs?limit=200&level=${logLevel.value}`)
    logs.value = res.entries || []
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    logsBusy.value = false
  }
}
function logTime(iso) {
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
}
function logFields(e) {
  if (!e.fields) return ''
  return Object.entries(e.fields)
    .map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : v}`)
    .join('  ')
}
function humanBytes(n) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}
const uptime = computed(() => {
  const s = info.value?.uptimeSec ?? 0
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return d > 0 ? `${d}d ${h}h ${m}m` : h > 0 ? `${h}h ${m}m` : `${m}m`
})
</script>

<template>
  <div class="antpage settings-page">
    <!-- Their security-warnings Alert: error, with a bold lead and the list. -->
    <div v-if="warnings.length && alertVisible" class="aalert error with-desc conf-alert">
      <AntIcon name="CloseCircleFilled" />
      <div class="aalert-body">
        <span class="aalert-title">{{ t('set.securityWarnings') }}</span>
        <span class="aalert-desc">
          <b>{{ t('set.panelExposed') }}</b>
          <ul><li v-for="(w, i) in warnings" :key="i">{{ w }}</li></ul>
        </span>
      </div>
      <button type="button" class="aalert-close" :aria-label="t('common.close')" @click="alertVisible = false"><AntIcon name="CloseOutlined" /></button>
    </div>

    <PageSpin v-if="showWait" />
    <div v-else-if="loading" class="empty"></div>
    <ErrorState v-else-if="loadError && !form" :error="loadError" @retry="load" />

    <template v-else-if="form">
      <!-- Their header card: Save, Restart Panel, and the standing note. -->
      <div class="acard">
        <div class="acard-body">
          <div class="header-row">
            <div class="header-actions">
              <div class="aspace">
                <button class="abtn primary" :disabled="!dirty || busy" @click="save">{{ t('set.save') }}</button>
                <button class="abtn primary danger-primary" :disabled="dirty || busy || restarting" @click="askRestart = true">
                  {{ restarting ? t('set.restarting') : t('set.restartPanel') }}
                </button>
              </div>
            </div>
            <div class="header-info">
              <div class="aalert warning">
                <AntIcon name="ExclamationCircleFilled" />
                <div class="aalert-body"><span class="aalert-title">{{ t('set.infoDesc') }}</span></div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="acard">
        <div class="acard-body">
          <!-- ══ General ══ -->
          <template v-if="active === 'general'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.general === '1' }" @click="inner.general = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.panelSettings') }}</span></button>
              <button class="atab" :class="{ active: inner.general === '2' }" @click="inner.general = '2'"><AntIcon name="BellOutlined" /><span>{{ t('set.notifications') }}</span></button>
              <button class="atab" :class="{ active: inner.general === '3' }" @click="inner.general = '3'"><AntIcon name="SafetyCertificateOutlined" /><span>{{ t('set.certs') }}</span></button>
              <button class="atab" :class="{ active: inner.general === '4' }" @click="inner.general = '4'"><AntIcon name="GlobalOutlined" /><span>{{ t('set.externalTraffic') }}</span></button>
              <button class="atab" :class="{ active: inner.general === '5' }" @click="inner.general = '5'"><AntIcon name="ClockCircleOutlined" /><span>{{ t('set.dateAndTime') }}</span></button>
              <button class="atab" :class="{ active: inner.general === '6' }" @click="inner.general = '6'"><AntIcon name="TeamOutlined" /><span>{{ t('set.customerDefaults') }}</span></button>
            </div></div>

            <template v-if="inner.general === '1'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelListeningIP') }}</div><div class="setting-list-description">{{ t('set.panelListeningIPDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.webListen" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelListeningDomain') }}</div><div class="setting-list-description">{{ t('set.panelListeningDomainDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.webDomain" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelPort') }}<span v-if="isDefault('webPort')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.panelPortDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.webPort" type="number" min="1" max="65535" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelUrlPath') }}</div><div class="setting-list-description">{{ t('set.panelUrlPathDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input :value="form.webBasePath" class="ltr" @input="form.webBasePath = sanitizePath($event.target.value)" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.sessionMaxAge') }}<span v-if="isDefault('sessionMaxAge')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.sessionMaxAgeDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.sessionMaxAge" type="number" min="60" max="525600" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.trustedProxyCidrs') }}</div><div class="setting-list-description">{{ t('set.trustedProxyCidrsDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.trustedProxyCIDRs" class="ltr" placeholder="127.0.0.1/32,::1/128" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelOutbound') }}</div><div class="setting-list-description">{{ t('set.panelOutboundDesc') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.panelOutbound">
                  <option value="">{{ t('set.panelOutboundPh') }}</option>
                  <optgroup v-if="balancerTags.length" :label="t('nav.outbounds')"><option v-for="tag in outboundTags" :key="tag" :value="tag">{{ tag }}</option></optgroup>
                  <template v-else><option v-for="tag in outboundTags" :key="tag" :value="tag">{{ tag }}</option></template>
                  <optgroup v-if="balancerTags.length" :label="t('routing.tab.balancers')"><option v-for="tag in balancerTags" :key="tag" :value="tag">{{ tag }}</option></optgroup>
                </select></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.pageSize') }}<span v-if="isDefault('pageSize')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.pageSizeDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.pageSize" type="number" min="0" max="1000" step="5" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.language') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.defaultLocale">
                  <option value="en">🇬🇧&nbsp;&nbsp;English</option>
                  <option value="fa">🇮🇷&nbsp;&nbsp;فارسی</option>
                </select></div></div>
              </div></div>
            </template>

            <template v-else-if="inner.general === '2'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.expireTimeDiff') }}<span v-if="isDefault('expireDiff')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.expireTimeDiffDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.expireDiff" type="number" min="0" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.trafficDiff') }}<span v-if="isDefault('trafficDiff')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.trafficDiffDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.trafficDiff" type="number" min="0" max="100" class="ltr" /></label></div>
              </div></div>
            </template>

            <template v-else-if="inner.general === '3'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.publicKeyPath') }}</div><div class="setting-list-description">{{ t('set.publicKeyPathDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.webCertFile" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.privateKeyPath') }}</div><div class="setting-list-description">{{ t('set.privateKeyPathDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.webKeyFile" class="ltr" /></label></div>
              </div></div>
            </template>

            <template v-else-if="inner.general === '4'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.externalTrafficInformEnable') }}</div><div class="setting-list-description">{{ t('set.externalTrafficInformEnableDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.externalTrafficInformEnable" :label="t('set.externalTrafficInformEnable')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.externalTrafficInformURI') }}</div><div class="setting-list-description">{{ t('set.externalTrafficInformURIDesc') }} {{ t('set.informBody') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.externalTrafficInformURI" class="ltr" placeholder="(http|https)://domain[:port]/path/" /></label></div>
              </div></div>
            </template>

            <template v-else-if="inner.general === '5'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.timeZone') }}</div><div class="setting-list-description">{{ t('set.timeZoneDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.timeLocation" class="ltr" placeholder="Asia/Tehran" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.datepicker') }}</div><div class="setting-list-description">{{ t('set.datepickerDescription') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.datepicker">
                  <option value="gregorian">{{ t('set.calendarGregorian') }}</option>
                  <option value="jalalian">{{ t('set.calendarJalalian') }}</option>
                </select></div></div>
              </div></div>
            </template>

            <template v-else>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defQuota') }}<span v-if="isDefault('defaultQuotaBytes')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defQuotaDesc') }}</div></div></div>
                <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="quotaGB" type="number" min="0" class="ltr" /></label><span class="abtn addon">GB</span></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defExpiry') }}<span v-if="isDefault('defaultExpiryDays')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defExpiryDesc') }}</div></div></div>
                <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="form.defaultExpiryDays" type="number" min="0" class="ltr" /></label><span class="abtn addon">{{ t('settings.days') }}</span></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defDevices') }}<span v-if="isDefault('defaultDeviceLimit')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defDevicesDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.defaultDeviceLimit" type="number" min="1" max="64" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defRate') }}<span v-if="isDefault('defaultRateBitsPerSec')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defRateDesc') }}</div></div></div>
                <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="rateMbit" type="number" min="0" class="ltr" /></label><span class="abtn addon">Mbit/s</span></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defReset') }}<span v-if="isDefault('defaultResetCycle')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defResetDesc') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.defaultResetCycle">
                  <option value="none">{{ t('reset.none') }}</option>
                  <option value="daily">{{ t('reset.daily') }}</option>
                  <option value="weekly">{{ t('reset.weekly') }}</option>
                  <option value="monthly">{{ t('reset.monthly') }}</option>
                </select></div></div>
              </div></div>
            </template>
          </template>

          <!-- ══ Authentication ══ -->
          <template v-else-if="active === 'security'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.security === '1' }" @click="inner.security = '1'"><AntIcon name="UserOutlined" /><span>{{ t('set.security.admin') }}</span></button>
              <button class="atab" :class="{ active: inner.security === '2' }" @click="inner.security = '2'"><AntIcon name="SafetyOutlined" /><span>{{ t('set.security.twoFactor') }}</span></button>
              <button class="atab" :class="{ active: inner.security === '3' }" @click="inner.security = '3'"><AntIcon name="ApiOutlined" /><span>{{ t('set.nodes.apiToken') }}</span></button>
            </div></div>

            <template v-if="inner.security === '1'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.oldUsername') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="user.oldUsername" autocomplete="username" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.currentPassword') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="user.oldPassword" :type="showSecret.old ? 'text' : 'password'" autocomplete="current-password" /><button type="button" class="ainput-clear" @click="showSecret.old = !showSecret.old"><AntIcon :name="showSecret.old ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.newUsername') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="user.newUsername" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.newPassword') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="user.newPassword" :type="showSecret.new ? 'text' : 'password'" autocomplete="new-password" /><button type="button" class="ainput-clear" @click="showSecret.new = !showSecret.new"><AntIcon :name="showSecret.new ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label></div>
              </div></div>
              <div class="security-actions"><div class="aspace" style="padding: 0 20px">
                <button class="abtn primary" :disabled="updating" @click="updateUser"><span v-if="updating" class="spin sm"></span>{{ t('set.confirm') }}</button>
              </div></div>
            </template>

            <template v-else-if="inner.security === '2'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.security.twoFactorEnable') }}</div><div class="setting-list-description">{{ t('set.security.twoFactorEnableDesc') }}</div></div></div>
                <div class="acol"><Toggle :model-value="twoFactor" :label="t('set.security.twoFactorEnable')" @update:model-value="toggleTwoFactor" /></div>
              </div></div>
              <!-- This panel as a node: which panel may drive it. -->
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.mtlsTrust') }}</div><div class="setting-list-description">{{ t('settings.mtlsTrustHint') }}</div></div></div>
                <div class="acol"><div class="aspace-v">
                  <label class="ainput block area"><textarea v-model="mtlsTrust" rows="4" class="ltr mono" placeholder="-----BEGIN CERTIFICATE-----"></textarea></label>
                  <div><button class="abtn" :disabled="mtlsBusy" @click="saveMtlsTrust"><span v-if="mtlsBusy" class="spin sm"></span>{{ t('set.save') }}</button></div>
                </div></div>
              </div></div>
            </template>

            <template v-else>
              <div class="api-token-section">
                <div class="api-token-header">
                  <p class="api-token-hint">{{ t('set.nodes.apiTokenHint') }}</p>
                  <button class="abtn primary sm" @click="createOpen = true">+ {{ t('set.security.apiTokenNew') }}</button>
                </div>
                <div v-if="!tokens.length && !tokensLoading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('set.security.apiTokenEmpty') }}</div>
                <div v-for="tk in tokens" :key="tk.id" class="api-token-row" :class="{ disabled: tk.disabled }">
                  <div class="api-token-row-head">
                    <div class="api-token-name-wrap">
                      <span class="api-token-name">{{ tk.name }}</span>
                      <span class="api-token-created">{{ tokenDate(tk.createdAt) }} · <code class="ltr">{{ tk.prefix }}…</code> · {{ tk.lastUsedAt ? relative(tk.lastUsedAt, store.locale) : t('settings.tokenNeverUsed') }}</span>
                    </div>
                    <div class="api-token-actions">
                      <Toggle :model-value="!tk.disabled" small :label="tk.name" @update:model-value="toggleTokenEnabled(tk)" />
                      <button class="abtn text sm danger-text" @click="revoking = tk">{{ t('set.delete') }}</button>
                    </div>
                  </div>
                </div>
              </div>
            </template>
          </template>

          <!-- ══ Telegram Bot ══ -->
          <template v-else-if="active === 'telegram'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.telegram === '1' }" @click="inner.telegram = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.panelSettings') }}</span></button>
              <button class="atab" :class="{ active: inner.telegram === '2' }" @click="inner.telegram = '2'"><AntIcon name="BellOutlined" /><span>{{ t('set.notifications') }}</span></button>
            </div></div>

            <template v-if="inner.telegram === '1'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramBotEnable') }}</div><div class="setting-list-description">{{ t('set.telegramBotEnableDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.notifyEnabled" :label="t('set.telegramBotEnable')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramToken') }}</div><div class="setting-list-description">{{ secretConfigured('notifyBotToken') ? t('set.telegramTokenConfigured') : t('set.telegramTokenDesc') }}</div></div></div>
                <div class="acol"><div class="acompact">
                  <label class="ainput"><input :value="form.notifyBotToken === PLACEHOLDER ? '' : form.notifyBotToken" :type="showSecret.tg ? 'text' : 'password'" autocomplete="off" spellcheck="false" class="ltr" :placeholder="secretConfigured('notifyBotToken') ? t('set.telegramTokenPlaceholder') : ''" @input="form.notifyBotToken = $event.target.value" /><button type="button" class="ainput-clear" @click="showSecret.tg = !showSecret.tg"><AntIcon :name="showSecret.tg ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label>
                  <button v-if="saved.notifyBotToken === PLACEHOLDER" type="button" class="abtn" :class="{ danger: secretClearArmed('notifyBotToken') }" @click="toggleClear('notifyBotToken')">{{ secretClearArmed('notifyBotToken') ? t('set.secretClearUndo') : t('set.secretClear') }}</button>
                </div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramChatId') }}</div><div class="setting-list-description">{{ t('set.telegramChatIdDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.notifyChatId" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramBotLanguage') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.notifyLang">
                  <option value="en">🇬🇧&nbsp;&nbsp;English</option>
                  <option value="fa">🇮🇷&nbsp;&nbsp;فارسی</option>
                </select></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramAPIServer') }}</div><div class="setting-list-description">{{ t('set.telegramAPIServerDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.notifyAPIServer" class="ltr" placeholder="https://api.example.com" /></label></div>
              </div></div>
              <div class="aspace-v" style="margin-top: 16px">
                <div><button class="abtn primary" :disabled="testLoading" @click="testTgBot"><span v-if="testLoading" class="spin sm"></span><AntIcon v-else name="SendOutlined" /><span>{{ t('set.testTgBot') }}</span></button></div>
                <div v-if="testResult" class="aalert" :class="testResult.success ? 'success' : 'error'">
                  <AntIcon :name="testResult.success ? 'CheckCircleFilled' : 'CloseCircleFilled'" />
                  <div class="aalert-body"><span class="aalert-title">{{ testResult.msg }}</span></div>
                  <button type="button" class="aalert-close" @click="testResult = null"><AntIcon name="CloseOutlined" /></button>
                </div>
              </div>
            </template>

            <template v-else>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramNotifyTime') }}</div><div class="setting-list-description">{{ t('set.telegramNotifyTimeDesc') }}</div></div></div>
                <div class="acol"><div class="aspace-v">
                  <div class="aselect"><select v-model="notifyMode">
                    <option value="every">{{ t('set.notifyTime.every') }}</option>
                    <option value="@hourly">{{ t('set.notifyTime.hourly') }}</option>
                    <option value="@daily">{{ t('set.notifyTime.daily') }}</option>
                    <option value="@weekly">{{ t('set.notifyTime.weekly') }}</option>
                    <option value="@monthly">{{ t('set.notifyTime.monthly') }}</option>
                    <option value="custom">{{ t('set.notifyTime.custom') }}</option>
                  </select></div>
                  <div v-if="notifyMode === 'every'" class="acompact">
                    <label class="ainput number"><input v-model.number="everyNum" type="number" min="1" class="ltr" :aria-label="t('set.notifyTime.interval')" /></label>
                    <div class="aselect"><select v-model="everyUnit" :aria-label="t('set.notifyTime.unit')">
                      <option value="s">{{ t('set.notifyTime.seconds') }}</option>
                      <option value="m">{{ t('set.notifyTime.minutes') }}</option>
                      <option value="h">{{ t('set.notifyTime.hours') }}</option>
                    </select></div>
                  </div>
                  <label v-if="notifyMode === 'custom'" class="ainput block"><input v-model="form.notifyRunTime" class="ltr" placeholder="0 30 8 * * *" /></label>
                </div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.tgNotifyBackup') }}</div><div class="setting-list-description">{{ t('set.tgNotifyBackupDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.notifyBackup" :label="t('set.tgNotifyBackup')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.tgEventBusNotify') }}</div><div class="setting-list-description">{{ t('set.tgEventBusNotifyDesc') }}</div></div></div>
                <div class="acol">
                  <div class="notif-grid">
                    <div v-for="g in eventGroups" :key="g.title" class="acard small notif-card">
                      <div class="acard-head small">
                        <span class="notif-title"><AntIcon :name="g.icon" /> {{ t(g.title) }}</span>
                        <span class="notif-extra"><span class="atag">{{ groupCount('notifyKinds', g) }}/{{ g.events.length }}</span><input type="checkbox" class="acheck" :checked="groupCount('notifyKinds', g) === g.events.length" :indeterminate.prop="groupCount('notifyKinds', g) > 0 && groupCount('notifyKinds', g) < g.events.length" @change="toggleGroup('notifyKinds', g)" /></span>
                      </div>
                      <div class="acard-body"><div class="aspace-v">
                        <div v-for="e in g.events" :key="e.key">
                          <label class="acheckbox"><input type="checkbox" :checked="hasKind('notifyKinds', e.key)" @change="toggleKind('notifyKinds', e.key)" />{{ t(e.label) }}</label>
                          <div v-if="e.setting && hasKind('notifyKinds', e.key)" style="padding-left: 24px; margin-top: 4px">
                            <label class="ainput number sm" style="width: 80px"><input v-model.number="form[e.setting]" type="number" :min="e.min" :max="e.max" class="ltr" /></label>
                          </div>
                        </div>
                      </div></div>
                    </div>
                  </div>
                </div>
              </div></div>
            </template>
          </template>

          <!-- ══ Email ══ -->
          <template v-else-if="active === 'email'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.email === '1' }" @click="inner.email = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.smtpSettings') }}</span></button>
              <button class="atab" :class="{ active: inner.email === '2' }" @click="inner.email = '2'"><AntIcon name="MailOutlined" /><span>{{ t('set.emailNotifications') }}</span></button>
            </div></div>

            <template v-if="inner.email === '1'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpEnable') }}</div><div class="setting-list-description">{{ t('set.smtpEnableDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.mailEnabled" :label="t('set.smtpEnable')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpHost') }}</div><div class="setting-list-description">{{ t('set.smtpHostDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.mailHost" class="ltr" placeholder="smtp.gmail.com" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpPort') }}<span v-if="isDefault('mailPort')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.smtpPortDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.mailPort" type="number" min="1" max="65535" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpUsername') }}</div><div class="setting-list-description">{{ t('set.smtpUsernameDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.mailUsername" class="ltr" placeholder="user@gmail.com" autocomplete="off" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpPassword') }}</div><div class="setting-list-description">{{ secretConfigured('mailPassword') ? t('set.smtpPasswordConfigured') : t('set.smtpPasswordDesc') }}</div></div></div>
                <div class="acol"><div class="acompact">
                  <label class="ainput"><input :value="form.mailPassword === PLACEHOLDER ? '' : form.mailPassword" :type="showSecret.mail ? 'text' : 'password'" autocomplete="off" class="ltr" :placeholder="secretConfigured('mailPassword') ? t('set.smtpPasswordPlaceholder') : ''" @input="form.mailPassword = $event.target.value" /><button type="button" class="ainput-clear" @click="showSecret.mail = !showSecret.mail"><AntIcon :name="showSecret.mail ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label>
                  <button v-if="saved.mailPassword === PLACEHOLDER" type="button" class="abtn" :class="{ danger: secretClearArmed('mailPassword') }" @click="toggleClear('mailPassword')">{{ secretClearArmed('mailPassword') ? t('set.secretClearUndo') : t('set.secretClear') }}</button>
                </div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpFrom') }}</div><div class="setting-list-description">{{ t('set.smtpFromDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.mailFrom" class="ltr" placeholder="user@gmail.com" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpFromName') }}</div><div class="setting-list-description">{{ t('set.smtpFromNameDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.mailFromName" placeholder="W-UI" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpTo') }}</div><div class="setting-list-description">{{ t('set.smtpToDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.mailTo" class="ltr" placeholder="admin@example.com, ops@example.com" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpEncryption') }}</div><div class="setting-list-description">{{ t('set.smtpEncryptionDesc') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.mailEncryption">
                  <option value="none">{{ t('set.smtpEncryptionNone') }}</option>
                  <option value="starttls">{{ t('set.smtpEncryptionStartTLS') }}</option>
                  <option value="tls">{{ t('set.smtpEncryptionTLS') }}</option>
                </select></div></div>
              </div></div>
              <div class="aspace-v" style="margin-top: 16px">
                <div><button class="abtn primary" :disabled="mailTesting" @click="testMail"><span v-if="mailTesting" class="spin sm"></span><AntIcon v-else name="SendOutlined" /><span>{{ t('set.testSmtp') }}</span></button></div>
                <div v-if="mailResult" class="aalert" :class="mailResult.success ? 'success' : 'error'">
                  <AntIcon :name="mailResult.success ? 'CheckCircleFilled' : 'CloseCircleFilled'" />
                  <div class="aalert-body"><span class="aalert-title">{{ mailResult.msg }}</span></div>
                  <button type="button" class="aalert-close" @click="mailResult = null"><AntIcon name="CloseOutlined" /></button>
                </div>
              </div>
            </template>

            <template v-else>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpEventBusNotify') }}</div><div class="setting-list-description">{{ t('set.smtpEventBusNotifyDesc') }}</div></div></div>
                <div class="acol">
                  <div class="notif-grid">
                    <div v-for="g in eventGroups" :key="g.title" class="acard small notif-card">
                      <div class="acard-head small">
                        <span class="notif-title"><AntIcon :name="g.icon" /> {{ t(g.title) }}</span>
                        <span class="notif-extra"><span class="atag">{{ groupCount('mailKinds', g) }}/{{ g.events.length }}</span><input type="checkbox" class="acheck" :checked="groupCount('mailKinds', g) === g.events.length" :indeterminate.prop="groupCount('mailKinds', g) > 0 && groupCount('mailKinds', g) < g.events.length" @change="toggleGroup('mailKinds', g)" /></span>
                      </div>
                      <div class="acard-body"><div class="aspace-v">
                        <div v-for="e in g.events" :key="e.key">
                          <label class="acheckbox"><input type="checkbox" :checked="hasKind('mailKinds', e.key)" @change="toggleKind('mailKinds', e.key)" />{{ t(e.label) }}</label>
                          <div v-if="e.setting && hasKind('mailKinds', e.key)" style="padding-left: 24px; margin-top: 4px">
                            <label class="ainput number sm" style="width: 80px"><input v-model.number="form[e.setting]" type="number" :min="e.min" :max="e.max" class="ltr" /></label>
                          </div>
                        </div>
                      </div></div>
                    </div>
                  </div>
                </div>
              </div></div>
            </template>
          </template>

          <!-- ══ Subscription ══ -->
          <template v-else-if="active === 'subscription' && sub">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.subscription === '1' }" @click="inner.subscription = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.panelSettings') }}</span></button>
              <button class="atab" :class="{ active: inner.subscription === '2' }" @click="inner.subscription = '2'"><AntIcon name="InfoCircleOutlined" /><span>{{ t('set.information') }}</span></button>
              <button class="atab" :class="{ active: inner.subscription === '3' }" @click="inner.subscription = '3'"><AntIcon name="IdcardOutlined" /><span>{{ t('set.profile') }}</span></button>
              <button class="atab" :class="{ active: inner.subscription === '4' }" @click="inner.subscription = '4'"><AntIcon name="SafetyCertificateOutlined" /><span>{{ t('set.certs') }}</span></button>
              <button class="atab" :class="{ active: inner.subscription === '5' }" @click="inner.subscription = '5'"><AntIcon name="PictureOutlined" /><span>{{ t('set.subTemplate') }}</span></button>
            </div></div>

            <template v-if="inner.subscription === '1'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subEnable') }}</div><div class="setting-list-description">{{ t('set.subEnableDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="sub.enabled" :label="t('set.subEnable')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subListen') }}</div><div class="setting-list-description">{{ t('set.subListenDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.listen" class="ltr" /></label><p v-if="subError.listen" class="field-error">{{ subError.listen }}</p></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subDomain') }}</div><div class="setting-list-description">{{ t('set.subDomainDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.host" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subPort') }}<span v-if="!sub.port" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.subPortDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="sub.port" type="number" min="0" max="65535" class="ltr" /></label><p v-if="subError.port" class="field-error">{{ subError.port }}</p></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subPath') }}</div><div class="setting-list-description">{{ t('set.subPathDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input :value="sub.path" class="ltr" placeholder="/sub/" @input="sub.path = sanitizePath($event.target.value)" /></label><p v-if="subError.path" class="field-error">{{ subError.path }}</p></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subURI') }}</div><div class="setting-list-description">{{ t('set.subURIDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.reverseProxyUri" class="ltr" placeholder="(http|https)://domain[:port]/path/" /></label><p v-if="subError.reverseProxyUri" class="field-error">{{ subError.reverseProxyUri }}</p></div>
              </div></div>
            </template>

            <template v-else-if="inner.subscription === '2'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subEncrypt') }}</div><div class="setting-list-description">{{ t('set.subEncryptDescWG') }}</div></div></div>
                <div class="acol"><Toggle v-model="sub.encode" :label="t('set.subEncrypt')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subUpdates') }}<span v-if="sub.updateHours === 12" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.subUpdatesDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="sub.updateHours" type="number" min="1" max="168" class="ltr" /></label><p v-if="subError.updateHours" class="field-error">{{ subError.updateHours }}</p></div>
              </div></div>
            </template>

            <template v-else-if="inner.subscription === '3'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subTitle') }}</div><div class="setting-list-description">{{ t('set.subTitleDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.title" /></label><p v-if="subError.title" class="field-error">{{ subError.title }}</p></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subSupportUrl') }}</div><div class="setting-list-description">{{ t('set.subSupportUrlDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.supportUrl" class="ltr" placeholder="https://example.com" /></label><p v-if="subError.supportUrl" class="field-error">{{ subError.supportUrl }}</p></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subProfileUrl') }}</div><div class="setting-list-description">{{ t('set.subProfileUrlDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.profileUrl" class="ltr" placeholder="https://example.com" /></label><p v-if="subError.profileUrl" class="field-error">{{ subError.profileUrl }}</p></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subAnnounce') }}</div><div class="setting-list-description">{{ t('set.subAnnounceDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block area"><textarea v-model="sub.announce" rows="3"></textarea></label><p v-if="subError.announce" class="field-error">{{ subError.announce }}</p></div>
              </div></div>
            </template>

            <template v-else-if="inner.subscription === '5'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subTemplate') }}</div><div class="setting-list-description">{{ t('set.subTemplateDesc') }}</div></div></div>
                <div class="acol"></div>
              </div></div>
              <div class="tpl-grid">
                <label v-for="tp in SUB_TEMPLATES" :key="tp" class="tpl-card" :class="{ on: sub.template === tp }">
                  <input v-model="sub.template" type="radio" name="sub-template" :value="tp" class="tpl-radio" />
                  <div class="tpl-swatch" :class="tp">
                    <span class="tpl-sw-head"></span>
                    <span class="tpl-sw-row"></span><span class="tpl-sw-row short"></span>
                    <span class="tpl-sw-bar"></span>
                  </div>
                  <div class="tpl-meta">
                    <div class="tpl-name">{{ t(`set.subTpl.${tp}`) }}<span v-if="tp === 'classic'" class="atag">{{ t('set.defaultTag') }}</span></div>
                    <div class="tpl-desc">{{ t(`set.subTpl.${tp}Desc`) }}</div>
                    <button type="button" class="abtn small" @click.prevent="previewTemplate(tp)"><AntIcon name="EyeOutlined" /><span>{{ t('set.subTplPreview') }}</span></button>
                  </div>
                </label>
              </div>
            </template>

            <template v-else>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subCertPath') }}</div><div class="setting-list-description">{{ t('set.subCertPathDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.certFile" class="ltr" /></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subKeyPath') }}</div><div class="setting-list-description">{{ t('set.subKeyPathDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="sub.keyFile" class="ltr" /></label><p v-if="subError.certFile" class="field-error">{{ subError.certFile }}</p></div>
              </div></div>
            </template>
          </template>

          <!-- ══ Backups ══ (reached from Maintenance) -->
          <template v-else-if="active === 'backups'">
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.backupEvery') }}</div><div class="setting-list-description">{{ t('settings.backupEveryDesc') }}</div></div></div>
              <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="form.backupEveryHours" type="number" min="0" max="720" class="ltr" /></label><span class="abtn addon">{{ t('settings.hours') }}</span></div></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.backupKeep') }}</div><div class="setting-list-description">{{ t('settings.backupKeepDesc') }}</div></div></div>
              <div class="acol"><label class="ainput number"><input v-model.number="form.backupKeep" type="number" min="0" max="365" class="ltr" /></label></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.backupsOnDisk') }}</div><div class="setting-list-description">{{ t('settings.backupsWarning') }}</div></div></div>
              <div class="acol"><div class="aspace-v">
                <div><button class="abtn primary" :disabled="backupBusy" @click="makeBackup">{{ backupBusy ? t('settings.backingUp') : t('settings.backupNow') }}</button></div>
                <ul v-if="backups.length" class="backup-list">
                  <li v-for="b in backups" :key="b.name">
                    <span class="backup-name ltr">{{ b.name }}</span>
                    <span class="backup-size ltr">{{ humanBytes(b.size) }}</span>
                    <button class="linkbtn" @click="downloadBackup(b.name)">{{ t('settings.download') }}</button>
                    <button class="linkbtn" :disabled="!!restoring" @click="ask = b.name">{{ t('settings.restore') }}</button>
                    <button class="linkbtn danger" :disabled="!!restoring" @click="removeBackup(b.name)">{{ t('common.delete') }}</button>
                  </li>
                </ul>
                <p v-else class="muted">{{ t('settings.noBackups') }}</p>
                <label class="abtn upload-btn">
                  <Icon name="upload" :size="15" />
                  <span>{{ uploading ? t('settings.uploading') : t('settings.uploadBackup') }}</span>
                  <input type="file" accept=".gz,.tar.gz,application/gzip" :disabled="uploading || !!restoring" @change="uploadBackup" />
                </label>
              </div></div>
            </div></div>
          </template>

          <!-- ══ Engine ══ -->
          <template v-else-if="active === 'engine'">
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.quotaEngine') }}</div><div class="setting-list-description">{{ info?.enforcementActive ? t('settings.quotaEngineOn') : info?.enforcementMessage }}</div></div></div>
              <div class="acol"><span class="atag" :class="info?.enforcementActive ? 'green' : 'red'">{{ info?.enforcementActive ? t('settings.active') : t('settings.inactive') }}</span></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.shaping') }}</div><div class="setting-list-description">{{ info?.shapingActive ? t('settings.shapingOn') : info?.shapingMessage || t('settings.shapingOff') }}</div></div></div>
              <div class="acol"><span class="atag" :class="info?.shapingActive ? 'green' : 'red'">{{ info?.shapingActive ? t('settings.active') : t('settings.inactive') }}</span></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.reconciler') }}</div><div class="setting-list-description">{{ t('settings.reconcilerDesc') }}</div></div></div>
              <div class="acol"><dl class="kv">
                <dt>{{ t('settings.ticks') }}</dt><dd class="ltr">{{ info?.reconciler?.ticks ?? 0 }}</dd>
                <dt>{{ t('settings.lastRun') }}</dt><dd class="ltr">{{ info?.reconciler?.lastDuration || '—' }}</dd>
                <dt>{{ t('settings.counted') }}</dt><dd class="ltr">{{ info?.reconciler?.bytesCounted ?? 0 }}</dd>
              </dl></div>
            </div></div>
          </template>

          <!-- ══ Logs ══ -->
          <template v-else-if="active === 'logs'">
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.recentLog') }}</div><div class="setting-list-description">{{ t('settings.recentLogDesc') }}</div></div></div>
              <div class="acol"><div class="aspace">
                <div class="aselect" style="width: 160px"><select v-model="logLevel" @change="loadLogs">
                  <option value="">{{ t('settings.logAll') }}</option>
                  <option value="INFO">{{ t('settings.logInfo') }}</option>
                  <option value="WARN">{{ t('settings.logWarn') }}</option>
                  <option value="ERROR">{{ t('settings.logError') }}</option>
                </select></div>
                <button class="abtn" :disabled="logsBusy" @click="loadLogs"><AntIcon name="ReloadOutlined" /><span>{{ t('common.refresh') }}</span></button>
              </div></div>
            </div></div>
            <div class="log-view">
              <p v-if="!logs.length" class="muted">{{ t('settings.logEmpty') }}</p>
              <ol v-else class="log-lines">
                <li v-for="(e, i) in logs" :key="i" :class="`lvl-${(e.level || '').toLowerCase()}`">
                  <span class="log-time ltr">{{ logTime(e.time) }}</span>
                  <span class="log-level ltr">{{ e.level }}</span>
                  <span class="log-msg">{{ e.message }}</span>
                  <span v-if="logFields(e)" class="log-fields ltr">{{ logFields(e) }}</span>
                </li>
              </ol>
            </div>
          </template>

          <!-- ══ System ══ -->
          <template v-else>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.version') }}</div><div class="setting-list-description">{{ t('settings.versionDesc') }}</div></div></div>
              <div class="acol"><dl class="kv">
                <dt>W-UI</dt><dd class="ltr">{{ info?.version }}</dd>
                <dt>Go</dt><dd class="ltr">{{ info?.goVersion }}</dd>
                <dt>{{ t('settings.platform') }}</dt><dd class="ltr">{{ info?.platform }}</dd>
                <dt>{{ t('settings.uptime') }}</dt><dd class="ltr">{{ uptime }}</dd>
              </dl></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.storage') }}</div><div class="setting-list-description">{{ t('settings.storageDesc') }}</div></div></div>
              <div class="acol"><dl class="kv">
                <dt>{{ t('settings.driver') }}</dt><dd class="ltr">{{ info?.dbDriver }}</dd>
                <dt>{{ t('nav.interfaces') }}</dt><dd class="ltr">{{ info?.interfaces ?? 0 }}</dd>
                <dt>{{ t('nav.clients') }}</dt><dd class="ltr">{{ info?.clients ?? 0 }}</dd>
                <dt>{{ t('settings.devices') }}</dt><dd class="ltr">{{ info?.accounts ?? 0 }}</dd>
              </dl></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.listen') }}</div><div class="setting-list-description">{{ t('settings.listenDesc') }}</div></div></div>
              <div class="acol"><code class="readonly ltr">{{ info?.listen }}</code></div>
            </div></div>
            <div class="setting-list-item"><div class="arow">
              <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.dataDir') }}</div><div class="setting-list-description">{{ t('settings.dataDirDesc') }}</div></div></div>
              <div class="acol"><code class="readonly ltr">{{ info?.dbSource || '—' }}</code></div>
            </div></div>
          </template>
        </div>
      </div>
    </template>

    <!-- ── modals ── -->
    <ConfirmDialog
      :open="askRestart"
      :title="t('set.restartPanel')"
      :body="t('set.restartConfirm')"
      :confirm-label="t('set.restartPanel')"
      :danger="true"
      @confirm="restartPanel"
      @cancel="askRestart = false"
    />
    <ConfirmDialog
      :open="!!ask"
      :title="t('settings.restoreTitle')"
      :body="t('settings.restoreBody')"
      :confirm-label="t('settings.restoreConfirm')"
      :danger="true"
      :busy="!!restoring"
      @confirm="() => { const n = ask; ask = null; restoreBackup(n) }"
      @cancel="ask = null"
    />
    <ConfirmDialog
      :open="!!revoking"
      :title="t('settings.revokeTitle')"
      :body="t('settings.revokeBody', { n: revoking?.name || '' })"
      :confirm-label="t('set.delete')"
      :danger="true"
      @confirm="doRevoke"
      @cancel="revoking = null"
    />
    <ConfirmDialog
      :open="!!pendingRoute"
      :title="t('settings.leaveTitle')"
      :body="t('settings.leaveBody')"
      :confirm-label="t('settings.saveAndLeave')"
      :danger="false"
      :busy="busy"
      @confirm="saveAndGo"
      @cancel="discardAndGo"
    />

    <!-- New API token: their Modal with one required Name. -->
    <div v-if="createOpen" class="amodal-backdrop" @click.self="createOpen = false">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head"><h2 class="amodal-title">{{ t('set.security.apiTokenNew') }}</h2><button type="button" class="amodal-close" :aria-label="t('common.close')" @click="createOpen = false"><AntIcon name="CloseOutlined" /></button></div>
        <div class="amodal-body">
          <div class="aform-item"><label class="aform-label required" for="tk-name">{{ t('set.security.apiTokenName') }}</label>
            <label class="ainput block"><input id="tk-name" v-model="createName" maxlength="64" :placeholder="t('set.security.apiTokenNamePlaceholder')" @keydown.enter="confirmCreateToken" /></label></div>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="createOpen = false">{{ t('set.cancel') }}</button>
          <button class="abtn primary" :disabled="creating || !createName.trim()" @click="confirmCreateToken"><span v-if="creating" class="spin sm"></span>{{ t('set.confirm') }}</button>
        </div>
      </div>
    </div>

    <!-- The token, once. -->
    <div v-if="createdToken" class="amodal-backdrop" @click.self="createdToken = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head"><h2 class="amodal-title">{{ t('set.security.apiTokenCreatedTitle') }}</h2><button type="button" class="amodal-close" :aria-label="t('common.close')" @click="createdToken = null"><AntIcon name="CloseOutlined" /></button></div>
        <div class="amodal-body">
          <p class="api-token-created-notice">{{ t('set.security.apiTokenCreatedNotice') }}</p>
          <div class="api-token-value-wrap">
            <code class="api-token-value ltr">{{ createdToken.token }}</code>
            <button class="abtn primary sm" @click="copyText(createdToken.token)">{{ t('set.copy') }}</button>
          </div>
        </div>
        <div class="amodal-foot"><button class="abtn primary" @click="createdToken = null">{{ t('set.done') }}</button></div>
      </div>
    </div>

    <!-- Two-factor: their modal, steps and QR and code. -->
    <div v-if="tfa" class="amodal-backdrop" @click.self="tfa = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head"><h2 class="amodal-title">{{ tfa.type === 'set' ? t('set.twoFactorModalSetTitle') : t('set.twoFactorModalDeleteTitle') }}</h2><button type="button" class="amodal-close" :aria-label="t('common.close')" @click="tfa = null"><AntIcon name="CloseOutlined" /></button></div>
        <div class="amodal-body">
          <template v-if="tfa.type === 'set'">
            <p>{{ t('set.twoFactorModalSteps') }}</p>
            <hr class="adivider" />
            <p>{{ t('set.twoFactorModalFirstStep') }}</p>
            <div class="qr-wrap" role="button" tabindex="0" :aria-label="t('set.copy')" @click="copyText(tfa.token)" @keydown.enter="copyText(tfa.token)">
              <img class="qr-code" :src="tfa.qr.dataUrl" width="180" height="180" alt="" />
              <span class="qr-token ltr">{{ tfa.token }}</span>
            </div>
            <hr class="adivider" />
            <p>{{ t('set.twoFactorModalSecondStep') }}</p>
            <label class="ainput block"><input v-model="tfa.code" inputmode="numeric" maxlength="6" class="ltr" :aria-label="t('set.twoFactorCode')" @keydown.enter="confirmTfa" /></label>
          </template>
          <template v-else>
            <p>{{ t('set.twoFactorModalDeleteDesc') }}</p>
            <label class="ainput block"><input v-model="tfa.code" class="ltr" :placeholder="t('set.twoFactorCode')" :aria-label="t('set.twoFactorCode')" /></label>
            <label class="ainput block" style="margin-top: 8px"><input v-model="tfa.password" type="password" autocomplete="current-password" :placeholder="t('set.password')" /></label>
          </template>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="tfa = null">{{ t('set.cancel') }}</button>
          <button class="abtn primary" :disabled="tfa.type === 'set' ? !/^\d{6}$/.test(tfa.code || '') : !(tfa.code || tfa.password)" @click="confirmTfa">{{ t('set.confirm') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Their header row: actions in a 10/24 column, the note in the other 14/24,
   flushed to its end. */
.header-row { display: flex; flex-wrap: wrap; align-items: center; }
.header-actions { flex: 0 0 41.6667%; max-width: 41.6667%; padding: 4px; }
.header-info { flex: 0 0 58.3333%; max-width: 58.3333%; display: flex; justify-content: flex-end; }
@media (max-width: 575px) {
  .header-actions, .header-info { flex: 0 0 100%; max-width: 100%; }
  .header-info { justify-content: flex-start; margin-top: 8px; }
}
.conf-alert { margin-bottom: 10px; }
.abtn.danger-primary { background: var(--bad); border-color: var(--bad); color: #fff; }
.abtn.danger-primary:hover:not(:disabled) { opacity: 0.85; }
.abtn:disabled { color: var(--faint); background: var(--surface-2); border-color: var(--line); box-shadow: none; cursor: not-allowed; }
.abtn.text.danger-text { color: var(--bad); }
.abtn.text.danger-text:hover { background: var(--bad-soft); color: var(--bad); }
.abtn.addon { flex: none; background: var(--surface-2); color: var(--muted); cursor: default; }
.security-actions { padding: 12px 0; display: flex; align-items: center; }

/* Their API token list. */
.api-token-section { padding: 8px 20px 16px; display: flex; flex-direction: column; gap: 12px; }
.api-token-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.api-token-hint { margin: 0; font-size: 12.5px; opacity: 0.7; flex: 1; min-width: 200px; }
.api-token-row { border: 1px solid var(--line-soft); border-radius: 8px; padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; transition: opacity 0.15s; }
.api-token-row.disabled { opacity: 0.55; }
.api-token-row-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
.api-token-name-wrap { display: flex; flex-direction: column; gap: 2px; }
.api-token-name { font-weight: 600; font-size: 13.5px; }
.api-token-created { font-size: 11px; opacity: 0.55; }
.api-token-actions { display: flex; align-items: center; gap: 8px; }
.api-token-value-wrap { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.api-token-value { flex: 1; min-width: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; padding: 4px 8px; background: var(--surface-3); border-radius: 4px; word-break: break-all; }
.api-token-created-notice { margin: 0 0 12px; font-size: 13px; }

/* Their notification cards: a grid of small outlined Cards. */
.notif-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 12px; }
.notif-card { border-width: 1px; }
.notif-card:hover { box-shadow: none; border-color: var(--line-soft); }
.acard-head.small { min-height: 38px; padding: 0 12px; font-size: 14px; font-weight: 600; justify-content: space-between; }
.notif-title { display: inline-flex; align-items: center; gap: 4px; }
.notif-extra { display: inline-flex; align-items: center; gap: 8px; }
.notif-extra .atag { margin: 0; }

/* Their two-factor modal. */
.adivider { margin: 24px 0; border: 0; border-top: 1px solid var(--line-soft); }
.amodal-body p { margin: 0 0 1em; }
.qr-wrap { display: flex; flex-direction: column; align-items: center; gap: 12px; cursor: pointer; }
.qr-code { display: block; border-radius: 4px; background: #fff; }
.qr-token { font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; word-break: break-all; text-align: center; }

.field-error { margin: 4px 0 0; color: var(--bad); font-size: 12px; }
.log-view { padding: 10px 20px; }

/* The template picker: a card per look, its swatch a small drawing of it. */
.tpl-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 12px; padding: 10px 20px; }
.tpl-card { position: relative; display: flex; gap: 12px; padding: 12px; border: 1px solid var(--line-soft); border-radius: 8px; background: var(--surface-2); cursor: pointer; transition: border-color .2s, box-shadow .2s; }
.tpl-card:hover { border-color: var(--accent-hover); }
.tpl-card.on { border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-ring); }
.tpl-radio { position: absolute; opacity: 0; width: 1px; height: 1px; }
.tpl-swatch { position: relative; flex: none; width: 96px; height: 72px; border-radius: 6px; overflow: hidden; background: #0a090a; display: flex; flex-direction: column; gap: 5px; padding: 8px; }
.tpl-sw-head { height: 8px; width: 60%; border-radius: 2px; background: rgba(255,255,255,.35); }
.tpl-sw-row { height: 5px; width: 80%; border-radius: 2px; background: rgba(255,255,255,.14); }
.tpl-sw-row.short { width: 55%; }
.tpl-sw-bar { margin-top: auto; height: 6px; width: 100%; border-radius: 3px; background: linear-gradient(90deg, var(--accent) 70%, rgba(255,255,255,.12) 70%); }
.tpl-swatch.aurora { background: radial-gradient(60px 40px at 15% 10%, #e02e3d99, transparent 70%), radial-gradient(60px 40px at 90% 40%, #3a5bff99, transparent 70%), radial-gradient(60px 40px at 40% 110%, #17b3a699, transparent 70%), #07111f; }
.tpl-swatch.waves { background: linear-gradient(180deg, #0b1220, #12203a); }
.tpl-swatch.waves::after { content: ''; position: absolute; left: -30%; right: -30%; bottom: -18px; height: 34px; border-radius: 45%; background: linear-gradient(90deg, #e02e3d, #2f6df6); opacity: .5; }
.tpl-swatch.network { background-color: #050608; background-image: radial-gradient(circle at 20% 30%, #e9edf3 1px, transparent 2px), radial-gradient(circle at 70% 20%, #e9edf3 1px, transparent 2px), radial-gradient(circle at 55% 75%, #e9edf3 1px, transparent 2px), linear-gradient(35deg, transparent 49%, rgba(224,46,61,.35) 50%, transparent 51%); }
.tpl-swatch.minimal { background: #fff; }
.tpl-swatch.minimal .tpl-sw-head { background: #111; } .tpl-swatch.minimal .tpl-sw-row { background: #ddd; }
.tpl-swatch.midnight { background: radial-gradient(70px 40px at 50% -10%, rgba(224,46,61,.45), transparent 70%), #000; box-shadow: inset 0 0 0 1px rgba(224,46,61,.4); }
.tpl-meta { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.tpl-name { font-size: 14px; font-weight: 500; display: flex; align-items: center; gap: 8px; }
.tpl-desc { font-size: 12px; color: var(--faint); line-height: 1.5; }
.tpl-meta .abtn { align-self: flex-start; margin-top: 4px; }
</style>
