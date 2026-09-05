<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { api, apiURL, getToken } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { relative } from '../lib/format.js'
import { store, t, loadMessages, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import SecurityWarnings from '../components/SecurityWarnings.vue'
import Toggle from '../components/Toggle.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const router = useRouter()

// The subscription service is stored separately from the panel settings, so it
// loads and saves on its own rather than riding along with a form that has
// nothing to do with it.
const sub = ref({
  enabled: false,
  path: '/subscribe/',
  host: '',
  title: '',
  updateHours: 12,
  reverseProxyUri: '',
})
const subBusy = ref(false)
const subError = ref({})

async function loadSub() {
  try {
    sub.value = await api.get('/api/subscription', { background: true })
  } catch {
    // Leaves the defaults in place. The page is still usable, and saving will
    // report anything genuinely wrong.
  }
}

async function saveSub() {
  subBusy.value = true
  subError.value = {}
  try {
    sub.value = await api.put('/api/subscription', sub.value)
    notify(t('sub.saved'), 'success')
  } catch (err) {
    if (err.field) subError.value = { [err.field]: err.message }
    else notify(err.message, 'error')
  } finally {
    subBusy.value = false
  }
}

const info = ref(null)
const loading = ref(true)
const loadError = ref(null)

// `saved` is what the server last confirmed; `form` is what is on screen. The
// save button is enabled by the difference between them, so an operator can
// always tell whether there is anything outstanding.
const saved = ref(null)
const defaults = ref(null)
const form = ref(null)
const showWait = useDelayed(computed(() => loading.value && !form.value))
const busy = ref(false)

const pw = ref({ current: '', next: '', confirm: '' })
const pwBusy = ref(false)
const pwError = ref('')

// The same five as the menu, in the same order. The tabs along the top and the
// links down the side are two ways to the same pages: an operator who learns
// one order must not meet another.
const tabs = [
  { key: 'general', icon: 'settings', label: 'settings.tab.general' },
  { key: 'security', icon: 'lock', label: 'settings.tab.security' },
  { key: 'notify', icon: 'send', label: 'settings.tab.notify' },
  { key: 'email', icon: 'mail', label: 'settings.tab.email' },
  { key: 'subscription', icon: 'link', label: 'settings.tab.subscription' },
]

// Pages this view still renders that are not part of that menu. They are
// reached from Maintenance, and showing the settings tabs above them would
// offer a row where nothing is selected.
const asideTabs = ['backups', 'engine', 'logs', 'system']
// The section can be named two ways: as a path, which is what the menu links
// to, or as a hash, which is what older links and bookmarks carry. Both are
// honoured so neither form breaks.
const props = defineProps({ tab: { type: String, default: '' } })

const active = ref(known(props.tab) || tabFromHash())

function known(slug) {
  return tabs.some((x) => x.key === slug) || asideTabs.includes(slug) ? slug : ''
}

// Whether the row of settings tabs belongs above this page.
const inSettingsMenu = computed(() => tabs.some((x) => x.key === active.value))
function tabFromHash() {
  return known((location.hash || '').replace(/^#/, '')) || 'general'
}

// Arriving from the menu while already on this page changes the parameter
// rather than remounting, so the tab has to follow it.
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

function selectTab(key) {
  active.value = key
  if (key === 'logs') loadLogs()
  // Kept in the address bar so a section can be linked to, and so a reload does
  // not throw the operator back to the first tab.
  if (props.tab) router.replace(`/settings/${key}`)
  else history.replaceState(null, '', `#${key}`)
}

onMounted(() => {
  load()
  loadSub()
  loadTokens()
})

async function load() {
  loading.value = true
  try {
    const [sys, cfg] = await Promise.all([api.get('/api/system'), api.get('/api/settings')])
    info.value = sys
    saved.value = cfg.settings
    defaults.value = cfg.defaults
    form.value = { ...cfg.settings, notifyKinds: [...(cfg.settings.notifyKinds || [])] }
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

const dirty = computed(() => {
  if (!form.value || !saved.value) return false
  return JSON.stringify(form.value) !== JSON.stringify(saved.value)
})

async function save() {
  busy.value = true
  try {
    const res = await api.put('/api/settings', {
      ...form.value,
      sessionHours: Number(form.value.sessionHours) || 12,
      defaultQuotaBytes: Number(form.value.defaultQuotaBytes) || 0,
      defaultExpiryDays: Number(form.value.defaultExpiryDays) || 0,
      defaultDeviceLimit: Number(form.value.defaultDeviceLimit) || 1,
      defaultRateBitsPerSec: Number(form.value.defaultRateBitsPerSec) || 0,
      backupEveryHours: Number(form.value.backupEveryHours) || 0,
      backupKeep: Number(form.value.backupKeep) || 0,
    })
    saved.value = res.settings
    defaults.value = res.defaults
    form.value = { ...res.settings, notifyKinds: [...(res.settings.notifyKinds || [])] }
    notify(t('settings.saved'), 'success')
    if (res.settings.defaultLocale !== store.locale) await loadMessages(res.settings.defaultLocale)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

const pendingRoute = ref(null)
// Set while we are deliberately going, so the guard does not stop the very
// navigation it just authorised.
const leaving = ref(false)

// Settings are a form you fill in and then leave, and clicking a link in the
// sidebar is how you leave. Without this, an edit made and not saved is gone
// with no word: measured before adding it, a value changed from 0 to 5 was 0
// again on returning, and nothing had asked.
//
// A dialog rather than the browser's own confirm: the panel does not use that
// anywhere else, and this one can name what is unsaved.
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
  // Only leave if the save actually took: a validation failure should keep the
  // operator on the page with their edit and the message, not send them away
  // having lost both.
  if (!dirty.value) await discardAndGo()
  else pendingRoute.value = null
}

function revert() {
  form.value = { ...saved.value }
}

// A value equal to what the panel ships as is marked, so an operator can tell
// what they chose from what merely came that way.
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

const NOTIFY_KINDS = ['exhausted', 'expired', 'expiring', 'sharing', 'login', 'panel', 'backup']

const backups = ref([])
const backupBusy = ref(false)
const testResult = ref(null)

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
    await loadMe()
  } catch (e) {
    notify(e.message, 'error')
  }
}

// The archive holds every key and credential, so it is fetched with the
// session token rather than linked: a plain link carries no Authorization
// header and would either fail or force the file to be served unauthenticated.
async function downloadBackup(name) {
  try {
    const res = await fetch(apiURL(`/api/backups/${encodeURIComponent(name)}`), {
      headers: { Authorization: `Bearer ${getToken()}` },
      // Half the session is an HttpOnly cookie the server sets at sign-in, and
      // the token alone is refused without it.
      credentials: 'same-origin',
    })
    if (!res.ok) throw new Error(await res.text())
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = name
    // Attached to the document, and the URL released on the next turn of the
    // loop. A detached anchor's click does nothing in Firefox, and revoking
    // immediately can cancel a download that has not started.
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
  } catch (e) {
    notify(e.message, 'error')
  }
}

// Restoring, which is the half that makes the rest worth having.
//
// Asked for twice: once as a dialog explaining what goes and what is kept, and
// once by making the operator watch the panel go away and come back. A backup
// restored by a stray click is worse than no restore button.
const restoring = ref(null)
const uploading = ref(false)
// Which archive the confirmation is about, or null.
const ask = ref(null)

// Machine access to this panel.
//
// A token here can do everything an administrator can, so the list is on the
// page about ways in rather than only on the page where one happens to be
// issued. Revoking is immediate and cannot be undone, which is why it asks.
const tokens = ref([])
const revoking = ref(null)

async function loadTokens() {
  try {
    tokens.value = await api.get('/api/tokens')
  } catch {
    // Not worth a message on a settings page that is showing other things
    // successfully; the empty line says as much as an error would.
    tokens.value = []
  }
}

function revokeToken(tk) {
  revoking.value = tk
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

async function restoreBackup(name) {
  restoring.value = name
  try {
    const res = await api.post(`/api/backups/${encodeURIComponent(name)}/restore`)
    notify(t('settings.restoreStarted', { n: res?.safetyCopy || '' }), 'success')
    // The panel is on its way out. Waiting and then reloading is what turns
    // "the page stopped working" into "it came back with the old data".
    setTimeout(() => window.location.reload(), 6000)
  } catch (e) {
    restoring.value = null
    notify(e.message, 'error')
  }
}

// Taking one in from another server, which is how a panel moves house.
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

// Saved first, because the server tests what it has stored rather than what is
// on screen — otherwise an operator would test a token they had not saved.
async function testNotification() {
  testResult.value = null
  if (dirty.value) await save()
  try {
    const res = await api.post('/api/settings/notify/test')
    testResult.value = res.ok ? { ok: true } : { ok: false, error: res.error }
  } catch (e) {
    testResult.value = { ok: false, error: e.message }
  }
}

function toggleKind(kind, on) {
  form.value.notifyKinds = withKind(form.value.notifyKinds, kind, on)
}

function toggleMailKind(kind, on) {
  form.value.mailKinds = withKind(form.value.mailKinds, kind, on)
}

function withKind(list, kind, on) {
  const set = new Set(list || [])
  if (on) set.add(kind)
  else set.delete(kind)
  return [...set]
}

// The mail test is its own button and its own result.
//
// An operator with both channels set up needs to know which one is broken, and
// one button reporting "failed" would not tell them. It saves first, because
// the server tests what is stored rather than what is on screen — testing an
// unsaved server address would report on the old one.
const mailTesting = ref(false)
const mailResult = ref(null)

async function testMail() {
  mailResult.value = null
  mailTesting.value = true
  try {
    if (dirty.value) await save()
    const res = await api.post('/api/settings/mail/test')
    mailResult.value = res.ok ? { ok: true } : { ok: false, error: res.error }
  } catch (e) {
    mailResult.value = { ok: false, error: e.message }
  } finally {
    mailTesting.value = false
  }
}

// Two-factor enrolment. The secret is held here only until it is confirmed:
// it is stored on the server after the operator's app has produced a correct
// code, never before, so a scan their app silently rejected cannot lock them
// out of their own panel.
const twoFactor = ref(false)
const enrol = ref(null)
const enrolCode = ref('')
const enrolError = ref('')
const disablePw = ref('')
const disabling = ref(false)

// The authority of the panel that manages this one, when this panel is being
// used as a node. Empty means the token alone, which is what an operator who
// has not set this up is relying on.
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

async function loadMe() {
  try {
    const me = await api.get('/api/auth/me')
    twoFactor.value = !!me.twoFactor
  } catch {
    /* the page is still usable without it */
  }
}

async function startEnrol() {
  enrolError.value = ''
  enrolCode.value = ''
  try {
    enrol.value = await api.post('/api/auth/totp/start')
  } catch (e) {
    notify(e.message, 'error')
  }
}

async function confirmEnrol() {
  enrolError.value = ''
  try {
    await api.post('/api/auth/totp/confirm', { secret: enrol.value.secret, code: enrolCode.value })
    enrol.value = null
    twoFactor.value = true
    notify(t('settings.twoFactorEnabled'), 'success')
  } catch (e) {
    enrolError.value = e.message
  }
}

async function disableTwoFactor() {
  enrolError.value = ''
  try {
    await api.post('/api/auth/totp/disable', { password: disablePw.value })
    disablePw.value = ''
    disabling.value = false
    twoFactor.value = false
    notify(t('settings.twoFactorDisabled'), 'success')
  } catch (e) {
    enrolError.value = e.message
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
  return d.toLocaleTimeString(undefined, { hour12: false }) + '.' +
    String(d.getMilliseconds()).padStart(3, '0')
}

// Fields are shown inline rather than hidden behind a toggle: the field is
// usually the answer — which interface, which customer, which error.
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

async function changePassword() {
  pwError.value = ''
  if (pw.value.next.length < 8) {
    pwError.value = t('settings.passwordTooShort')
    return
  }
  if (pw.value.next !== pw.value.confirm) {
    pwError.value = t('settings.passwordMismatch')
    return
  }
  pwBusy.value = true
  try {
    await api.post('/api/auth/password', {
      currentPassword: pw.value.current,
      newPassword: pw.value.next,
    })
    pw.value = { current: '', next: '', confirm: '' }
    notify(t('settings.passwordChanged'), 'success')
  } catch (e) {
    pwError.value = e.message
  } finally {
    pwBusy.value = false
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>{{ t('nav.settings') }}</h1>
      <p class="lede">{{ t('settings.lede') }}</p>
    </div>
  </div>


  <!-- Settings rows are a label with a description on the left and a control
       on the right, and that is what stands in for them. -->
  <section v-if="showWait" class="card sk-rows" aria-hidden="true">
    <div v-for="n in 6" :key="n" class="sk-row">
      <div class="sk-row-meta">
        <span class="sk" :style="{ width: 34 + ((n * 7) % 22) + '%' }"></span>
        <span class="sk" :style="{ width: 62 + ((n * 5) % 26) + '%' }"></span>
      </div>
      <span class="sk sk-lg sk-row-control"></span>
    </div>
  </section>
  <div v-else-if="loading" class="empty"></div>

  <ErrorState v-else-if="loadError && !form" :error="loadError" @retry="load" />

  <template v-else-if="form">
    <!-- Everything worth saying about this server, in one place. Two stacked
         warning boxes read as two unrelated problems and get skimmed. -->
    <SecurityWarnings :listen="info?.listen" />

    <!-- Restoring replaces the database, every key and every certificate, so
         it is asked for rather than done. The safety copy is named in the body
         because knowing it exists is what makes the answer an easy one. -->
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

    <!-- Revoking is immediate: whatever was using that token stops working the
         moment this is confirmed, and there is no undoing it. -->
    <ConfirmDialog
      :open="!!revoking"
      :title="t('settings.revokeTitle')"
      :body="t('settings.revokeBody', { n: revoking?.name || '' })"
      :confirm-label="t('action.revoke')"
      :danger="true"
      @confirm="doRevoke"
      @cancel="revoking = null"
    />

    <!-- Raised when a link is clicked with an unsaved edit on the page. -->
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

    <!-- The save bar sits above the tabs: it applies to all of them, and a
         change made on one tab must not look lost when another is opened. -->
    <section class="card savebar">
      <div class="savebar-actions">
        <button class="btn primary"
          :disabled="!dirty || busy"
          :title="!dirty ? t('settings.nothingToSave') : ''"
          @click="save">
          <span v-if="busy">{{ t('common.saving') }}</span>
          <span v-else>{{ t('common.save') }}</span>
        </button>
        <button class="btn ghost"
          :disabled="!dirty || busy"
          :title="!dirty ? t('settings.nothingToRevert') : ''"
          @click="revert">
          {{ t('common.revert') }}
        </button>
      </div>
      <p class="savebar-note">
        <Icon name="info" :size="14" />
        <span>{{ dirty ? t('settings.unsaved') : t('settings.note') }}</span>
      </p>
    </section>

    <nav v-if="inSettingsMenu" class="cat-tabs" role="tablist">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        class="cat-tab"
        role="tab"
        :class="{ active: active === tab.key }"
        :aria-selected="active === tab.key"
        :title="t(tab.label)"
        @click="selectTab(tab.key)"
      >
        <Icon :name="tab.icon" :size="16" />
        <span class="cat-tab-text">{{ t(tab.label) }}</span>
      </button>
    </nav>

    <section class="card settings-list">
      <!-- ── General ── -->
      <template v-if="active === 'general'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.language') }}
              <span v-if="isDefault('defaultLocale')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.languageDesc') }}</p>
          </div>
          <div class="setting-control">
            <select v-model="form.defaultLocale">
              <option value="en">English</option>
              <option value="fa">فارسی</option>
            </select>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.listen') }}</div>
            <p class="setting-desc">{{ t('settings.listenDesc') }}</p>
          </div>
          <div class="setting-control">
            <code class="readonly ltr">{{ info?.listen }}</code>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.dataDir') }}</div>
            <p class="setting-desc">{{ t('settings.dataDirDesc') }}</p>
          </div>
          <div class="setting-control">
            <code class="readonly ltr">{{ info?.dbSource || '—' }}</code>
          </div>
        </div>

        <!-- What a new customer starts with. Its own page before, which made
             the settings menu longer than it needed to be for three fields
             that are read once and rarely changed. -->
        <h3 class="setting-group">{{ t('settings.tab.clients') }}</h3>
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.defQuota') }}
              <span v-if="isDefault('defaultQuotaBytes')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.defQuotaDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="unit-field">
              <input v-model.number="quotaGB" type="number" min="0" step="1" />
              <span class="unit">GB</span>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.defExpiry') }}
              <span v-if="isDefault('defaultExpiryDays')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.defExpiryDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="unit-field">
              <input v-model.number="form.defaultExpiryDays" type="number" min="0" step="1" />
              <span class="unit">{{ t('settings.days') }}</span>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.defDevices') }}
              <span v-if="isDefault('defaultDeviceLimit')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.defDevicesDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model.number="form.defaultDeviceLimit" type="number" min="1" max="64" step="1" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.defRate') }}
              <span v-if="isDefault('defaultRateBitsPerSec')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.defRateDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="unit-field">
              <input v-model.number="rateMbit" type="number" min="0" step="1" />
              <span class="unit">Mbit/s</span>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.defReset') }}
              <span v-if="isDefault('defaultResetCycle')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.defResetDesc') }}</p>
          </div>
          <div class="setting-control">
            <select v-model="form.defaultResetCycle">
              <option value="none">{{ t('reset.none') }}</option>
              <option value="daily">{{ t('reset.daily') }}</option>
              <option value="weekly">{{ t('reset.weekly') }}</option>
              <option value="monthly">{{ t('reset.monthly') }}</option>
            </select>
          </div>
        </div>
      </template>



      <!-- ── Subscription ── -->
      <template v-else-if="active === 'subscription'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('sub.enabled') }}</div>
            <p class="setting-desc">{{ t('sub.enabledDesc') }}</p>
          </div>
          <div class="setting-control">
            <Toggle v-model="sub.enabled" :label="t('sub.enabled')" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('sub.path') }}</div>
            <p class="setting-desc">{{ t('sub.pathDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="sub.path" class="ltr" autocomplete="off" placeholder="/subscribe/" />
            <p v-if="subError.path" class="field-error">{{ subError.path }}</p>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('sub.title') }}</div>
            <p class="setting-desc">{{ t('sub.titleDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="sub.title" autocomplete="off" />
            <p v-if="subError.title" class="field-error">{{ subError.title }}</p>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('sub.host') }}</div>
            <p class="setting-desc">{{ t('sub.hostDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="sub.host" class="ltr" autocomplete="off" :placeholder="t('sub.hostPlaceholder')" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('sub.interval') }}</div>
            <p class="setting-desc">{{ t('sub.intervalDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="unit-field">
              <input v-model.number="sub.updateHours" type="number" min="1" max="168" />
              <span class="unit">{{ t('settings.hours') }}</span>
            </div>
            <p v-if="subError.updateHours" class="field-error">{{ subError.updateHours }}</p>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('sub.proxy') }}</div>
            <p class="setting-desc">{{ t('sub.proxyDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="sub.reverseProxyUri" class="ltr" autocomplete="off" placeholder="https://vpn.example.com" />
            <p v-if="subError.reverseProxyUri" class="field-error">{{ subError.reverseProxyUri }}</p>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta"></div>
          <div class="setting-control">
            <button class="btn primary" :disabled="subBusy" @click="saveSub">
              <span v-if="subBusy" class="spin"></span>
              <template v-else>{{ t('action.save') }}</template>
            </button>
          </div>
        </div>
      </template>

      <!-- ── Security ── -->
      <template v-else-if="active === 'security'">
        <!-- Machine access to this panel. It was only on the Nodes page, where
             it was reachable while adding a node and nowhere else: a credential
             that can do everything an administrator can belongs with the other
             ways in, so it can be looked at and revoked without a reason. -->
        <div class="setting-row block">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.apiTokens') }}</div>
            <p class="setting-desc">{{ t('settings.apiTokensDesc') }}</p>
          </div>
          <div class="setting-control wide">
            <table v-if="tokens.length" class="mini-table">
              <thead>
                <tr>
                  <th>{{ t('node.tokenName') }}</th>
                  <th class="ltr">{{ t('settings.tokenPrefix') }}</th>
                  <th>{{ t('settings.tokenLastUsed') }}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="tk in tokens" :key="tk.id">
                  <td>{{ tk.name }}</td>
                  <td><code class="ltr">{{ tk.prefix }}…</code></td>
                  <!-- A token that has never been used is worth seeing: it is
                       either not wired up yet, or it was forgotten. -->
                  <td class="muted small">
                    {{ tk.lastUsedAt ? relative(tk.lastUsedAt) : t('settings.tokenNeverUsed') }}
                  </td>
                  <td class="right">
                    <button class="btn sm ghost danger" @click="revokeToken(tk)">
                      {{ t('action.revoke') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
            <p v-else class="muted small">{{ t('settings.noTokens') }}</p>
          </div>
        </div>
        <!-- This panel as a node: which panel is allowed to manage it. The
             token says the caller knows a secret and travels in every request;
             a certificate says which machine it is and its key never moves. -->
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mtlsTrust') }}</div>
            <p class="setting-desc">{{ t('settings.mtlsTrustHint') }}</p>
          </div>
          <div class="setting-control wide">
            <textarea v-model="mtlsTrust" rows="4" class="ltr mono"
                      placeholder="-----BEGIN CERTIFICATE-----"></textarea>
            <button class="btn sm" :disabled="mtlsBusy" @click="saveMtlsTrust">
              <span v-if="mtlsBusy" class="spin sm"></span>
              <span v-else>{{ t('action.save') }}</span>
            </button>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.session') }}
              <span v-if="isDefault('sessionHours')" class="tag grey">{{ t('settings.default') }}</span>
            </div>
            <p class="setting-desc">{{ t('settings.sessionDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="unit-field">
              <input v-model.number="form.sessionHours" type="number" min="1" max="720" step="1" />
              <span class="unit">{{ t('settings.hours') }}</span>
            </div>
          </div>
        </div>

        <div class="setting-row block">
          <div class="setting-meta">
            <div class="setting-title">
              {{ t('settings.twoFactor') }}
              <span class="tag" :class="twoFactor ? 'green' : 'grey'">
                {{ twoFactor ? t('settings.twoFactorOn') : t('settings.twoFactorOff') }}
              </span>
            </div>
            <p class="setting-desc">{{ t('settings.twoFactorDesc') }}</p>
          </div>
          <div class="setting-control stack-end">
            <!-- Off, and not being set up. -->
            <button v-if="!twoFactor && !enrol" class="btn primary" @click="startEnrol">
              {{ t('settings.enableTwoFactor') }}
            </button>

            <!-- Mid-enrolment: the key is shown once, here. -->
            <div v-else-if="enrol" class="enrol">
              <p class="setting-desc">{{ t('settings.scanThis') }}</p>
              <div class="enrol-uri ltr">{{ enrol.uri }}</div>
              <p class="setting-desc">{{ t('settings.orEnterKey') }}</p>
              <code class="readonly ltr">{{ enrol.secret }}</code>
              <div class="enrol-confirm">
                <input
                  v-model="enrolCode"
                  inputmode="numeric"
                  maxlength="6"
                  placeholder="000000"
                  class="ltr code-input"
                />
                <button class="btn primary" @click="confirmEnrol">{{ t('settings.confirmCode') }}</button>
                <button class="btn ghost" @click="enrol = null">{{ t('common.cancel') }}</button>
              </div>
              <p v-if="enrolError" class="test-result bad">{{ enrolError }}</p>
            </div>

            <!-- On. Turning it off asks for the password again, because a
                 borrowed open session is exactly what it exists to survive. -->
            <div v-else class="enrol">
              <template v-if="disabling">
                <p class="setting-desc">{{ t('settings.confirmPasswordToDisable') }}</p>
                <div class="enrol-confirm">
                  <input v-model="disablePw" type="password" autocomplete="current-password" />
                  <button class="btn danger" @click="disableTwoFactor">
                    {{ t('settings.disableTwoFactor') }}
                  </button>
                  <button class="btn ghost" @click="disabling = false">{{ t('common.cancel') }}</button>
                </div>
                <p v-if="enrolError" class="test-result bad">{{ enrolError }}</p>
              </template>
              <button v-else class="btn ghost" @click="disabling = true">
                {{ t('settings.disableTwoFactor') }}
              </button>
            </div>
          </div>
        </div>

        <div class="setting-row block">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.changePassword') }}</div>
            <p class="setting-desc">{{ t('settings.changePasswordDesc') }}</p>
          </div>
          <div class="setting-control">
            <form class="pw-form" @submit.prevent="changePassword">
              <label>
                <span>{{ t('settings.currentPassword') }}</span>
                <input v-model="pw.current" type="password" autocomplete="current-password" required />
              </label>
              <label>
                <span>{{ t('settings.newPassword') }}</span>
                <input v-model="pw.next" type="password" autocomplete="new-password" required />
              </label>
              <label>
                <span>{{ t('settings.confirmPassword') }}</span>
                <input v-model="pw.confirm" type="password" autocomplete="new-password" required />
              </label>
              <p v-if="pwError" class="field-error">{{ pwError }}</p>
              <button class="btn primary" type="submit" :disabled="pwBusy">
                <span v-if="pwBusy">{{ t('common.saving') }}</span>
                <span v-else>{{ t('settings.updatePassword') }}</span>
              </button>
            </form>
          </div>
        </div>
      </template>

      <!-- ── Notifications ── -->
      <template v-else-if="active === 'notify'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.notifyEnabled') }}</div>
            <p class="setting-desc">{{ t('settings.notifyEnabledDesc') }}</p>
          </div>
          <div class="setting-control">
            <label class="switch">
              <input v-model="form.notifyEnabled" type="checkbox" />
              <span>{{ form.notifyEnabled ? t('settings.on') : t('settings.off') }}</span>
            </label>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.botToken') }}</div>
            <p class="setting-desc">{{ t('settings.botTokenDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.notifyBotToken" type="password" autocomplete="off" spellcheck="false" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.chatId') }}</div>
            <p class="setting-desc">{{ t('settings.chatIdDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.notifyChatId" type="text" inputmode="numeric" class="ltr" />
          </div>
        </div>

        <div class="setting-row block">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.notifyKinds') }}</div>
            <p class="setting-desc">{{ t('settings.notifyKindsDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="check-list">
              <label v-for="k in NOTIFY_KINDS" :key="k" class="check">
                <input
                  type="checkbox"
                  :checked="(form.notifyKinds || []).includes(k)"
                  @change="toggleKind(k, $event.target.checked)"
                />
                <span>{{ t(`settings.kind.${k}`) }}</span>
              </label>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.testNotify') }}</div>
            <p class="setting-desc">{{ t('settings.testNotifyDesc') }}</p>
          </div>
          <div class="setting-control stack-end">
            <button class="btn ghost" @click="testNotification">{{ t('settings.sendTest') }}</button>
            <p v-if="testResult" class="test-result" :class="testResult.ok ? 'ok' : 'bad'">
              {{ testResult.ok ? t('settings.testOk') : testResult.error }}
            </p>
          </div>
        </div>
      </template>

      <!-- ── Email ── -->
      <template v-else-if="active === 'email'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailEnabled') }}</div>
            <p class="setting-desc">{{ t('settings.mailEnabledDesc') }}</p>
          </div>
          <div class="setting-control">
            <label class="switch">
              <input v-model="form.mailEnabled" type="checkbox" />
              <span>{{ form.mailEnabled ? t('settings.on') : t('settings.off') }}</span>
            </label>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailHost') }}</div>
            <p class="setting-desc">{{ t('settings.mailHostDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.mailHost" type="text" class="ltr" spellcheck="false" placeholder="smtp.example.com" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailPort') }}</div>
            <p class="setting-desc">{{ t('settings.mailPortDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model.number="form.mailPort" type="number" min="1" max="65535" step="1" class="ltr" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailEncryption') }}</div>
            <p class="setting-desc">{{ t('settings.mailEncryptionDesc') }}</p>
          </div>
          <div class="setting-control">
            <select v-model="form.mailEncryption">
              <option value="starttls">{{ t('settings.mailStartTLS') }}</option>
              <option value="tls">{{ t('settings.mailTLS') }}</option>
              <option value="none">{{ t('settings.mailNoTLS') }}</option>
            </select>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailUsername') }}</div>
            <p class="setting-desc">{{ t('settings.mailUsernameDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.mailUsername" type="text" class="ltr" autocomplete="off" spellcheck="false" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailPassword') }}</div>
            <p class="setting-desc">{{ t('settings.mailPasswordDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.mailPassword" type="password" autocomplete="off" spellcheck="false" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailFrom') }}</div>
            <p class="setting-desc">{{ t('settings.mailFromDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.mailFrom" type="email" class="ltr" autocomplete="off" spellcheck="false" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailFromName') }}</div>
            <p class="setting-desc">{{ t('settings.mailFromNameDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.mailFromName" type="text" placeholder="W-UI" />
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailTo') }}</div>
            <p class="setting-desc">{{ t('settings.mailToDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model="form.mailTo" type="text" class="ltr" autocomplete="off" spellcheck="false" />
          </div>
        </div>

        <div class="setting-row block">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.mailKinds') }}</div>
            <p class="setting-desc">{{ t('settings.mailKindsDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="check-list">
              <label v-for="k in NOTIFY_KINDS" :key="k" class="check">
                <input
                  type="checkbox"
                  :checked="(form.mailKinds || []).includes(k)"
                  @change="toggleMailKind(k, $event.target.checked)"
                />
                <span>{{ t(`settings.kind.${k}`) }}</span>
              </label>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.testMail') }}</div>
            <p class="setting-desc">{{ t('settings.testMailDesc') }}</p>
          </div>
          <div class="setting-control stack-end">
            <button class="btn ghost" :disabled="mailTesting" @click="testMail">
              <span v-if="mailTesting" class="spin sm"></span>{{ t('settings.sendTest') }}
            </button>
            <p v-if="mailResult" class="test-result" :class="mailResult.ok ? 'ok' : 'bad'">
              {{ mailResult.ok ? t('settings.testOk') : mailResult.error }}
            </p>
          </div>
        </div>
      </template>

      <!-- ── Backups ── -->
      <template v-else-if="active === 'backups'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.backupEvery') }}</div>
            <p class="setting-desc">{{ t('settings.backupEveryDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="unit-field">
              <input v-model.number="form.backupEveryHours" type="number" min="0" max="720" step="1" />
              <span class="unit">{{ t('settings.hours') }}</span>
            </div>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.backupKeep') }}</div>
            <p class="setting-desc">{{ t('settings.backupKeepDesc') }}</p>
          </div>
          <div class="setting-control">
            <input v-model.number="form.backupKeep" type="number" min="0" max="365" step="1" />
          </div>
        </div>

        <div class="setting-row block">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.backupsOnDisk') }}</div>
            <p class="setting-desc">{{ t('settings.backupsWarning') }}</p>
          </div>
          <div class="setting-control stack-end">
            <button class="btn primary" :disabled="backupBusy" @click="makeBackup">
              <span v-if="backupBusy">{{ t('settings.backingUp') }}</span>
              <span v-else>{{ t('settings.backupNow') }}</span>
            </button>

            <ul v-if="backups.length" class="backup-list">
              <li v-for="b in backups" :key="b.name">
                <span class="backup-name ltr">{{ b.name }}</span>
                <span class="backup-size ltr">{{ humanBytes(b.size) }}</span>
                <button class="linkbtn" @click="downloadBackup(b.name)">
                  {{ t('settings.download') }}
                </button>
                <button class="linkbtn" :disabled="!!restoring" @click="ask = b.name">
                  {{ t('settings.restore') }}
                </button>
                <button class="linkbtn danger" :disabled="!!restoring" @click="removeBackup(b.name)">
                  {{ t('common.delete') }}
                </button>
              </li>
            </ul>
            <p v-else class="muted">{{ t('settings.noBackups') }}</p>

            <!-- How a panel moves to another server: take the archive off the
                 old one, put it on the new one, restore it. -->
            <label class="btn ghost upload-btn">
              <Icon name="upload" :size="15" />
              <span>{{ uploading ? t('settings.uploading') : t('settings.uploadBackup') }}</span>
              <input type="file" accept=".gz,.tar.gz,application/gzip"
                     :disabled="uploading || !!restoring" @change="uploadBackup" />
            </label>
          </div>
        </div>
      </template>

      <!-- ── Engine ── -->
      <template v-else-if="active === 'engine'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.quotaEngine') }}</div>
            <p class="setting-desc">
              {{ info?.enforcementActive ? t('settings.quotaEngineOn') : info?.enforcementMessage }}
            </p>
          </div>
          <div class="setting-control">
            <span class="tag" :class="info?.enforcementActive ? 'green' : 'red'">
              {{ info?.enforcementActive ? t('settings.active') : t('settings.inactive') }}
            </span>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.shaping') }}</div>
            <p class="setting-desc">
              {{ info?.shapingActive ? t('settings.shapingOn') : info?.shapingMessage || t('settings.shapingOff') }}
            </p>
          </div>
          <div class="setting-control">
            <span class="tag" :class="info?.shapingActive ? 'green' : 'red'">
              {{ info?.shapingActive ? t('settings.active') : t('settings.inactive') }}
            </span>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.reconciler') }}</div>
            <p class="setting-desc">{{ t('settings.reconcilerDesc') }}</p>
          </div>
          <div class="setting-control">
            <dl class="kv">
              <dt>{{ t('settings.ticks') }}</dt>
              <dd class="ltr">{{ info?.reconciler?.ticks ?? 0 }}</dd>
              <dt>{{ t('settings.lastRun') }}</dt>
              <dd class="ltr">{{ info?.reconciler?.lastDuration || '—' }}</dd>
              <dt>{{ t('settings.counted') }}</dt>
              <dd class="ltr">{{ info?.reconciler?.bytesCounted ?? 0 }}</dd>
            </dl>
          </div>
        </div>
      </template>

      <!-- ── Logs ── -->
      <template v-else-if="active === 'logs'">
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.recentLog') }}</div>
            <p class="setting-desc">{{ t('settings.recentLogDesc') }}</p>
          </div>
          <div class="setting-control">
            <div class="log-controls">
              <select v-model="logLevel" @change="loadLogs">
                <option value="">{{ t('settings.logAll') }}</option>
                <option value="INFO">{{ t('settings.logInfo') }}</option>
                <option value="WARN">{{ t('settings.logWarn') }}</option>
                <option value="ERROR">{{ t('settings.logError') }}</option>
              </select>
              <button class="btn ghost" :disabled="logsBusy" @click="loadLogs">
                <Icon name="refresh" :size="15" />
                <span>{{ t('common.refresh') }}</span>
              </button>
            </div>
          </div>
        </div>

        <div class="setting-row block">
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
        </div>
      </template>

      <!-- ── System ── -->
      <template v-else>
        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.version') }}</div>
            <p class="setting-desc">{{ t('settings.versionDesc') }}</p>
          </div>
          <div class="setting-control">
            <dl class="kv">
              <dt>W-UI</dt>
              <dd class="ltr">{{ info?.version }}</dd>
              <dt>Go</dt>
              <dd class="ltr">{{ info?.goVersion }}</dd>
              <dt>{{ t('settings.platform') }}</dt>
              <dd class="ltr">{{ info?.platform }}</dd>
              <dt>{{ t('settings.uptime') }}</dt>
              <dd class="ltr">{{ uptime }}</dd>
            </dl>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.storage') }}</div>
            <p class="setting-desc">{{ t('settings.storageDesc') }}</p>
          </div>
          <div class="setting-control">
            <dl class="kv">
              <dt>{{ t('settings.driver') }}</dt>
              <dd class="ltr">{{ info?.dbDriver }}</dd>
              <dt>{{ t('nav.interfaces') }}</dt>
              <dd class="ltr">{{ info?.interfaces ?? 0 }}</dd>
              <dt>{{ t('nav.clients') }}</dt>
              <dd class="ltr">{{ info?.clients ?? 0 }}</dd>
              <dt>{{ t('settings.devices') }}</dt>
              <dd class="ltr">{{ info?.accounts ?? 0 }}</dd>
            </dl>
          </div>
        </div>

        <div class="setting-row">
          <div class="setting-meta">
            <div class="setting-title">{{ t('settings.processConfig') }}</div>
            <p class="setting-desc">{{ t('settings.processConfigDesc') }}</p>
          </div>
          <div class="setting-control">
            <code class="readonly ltr">w-ui</code>
          </div>
        </div>
      </template>
    </section>
  </template>
</template>
