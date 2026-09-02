<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, loadMessages, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'

const router = useRouter()

const info = ref(null)
const loading = ref(true)
const loadError = ref(null)

// `saved` is what the server last confirmed; `form` is what is on screen. The
// save button is enabled by the difference between them, so an operator can
// always tell whether there is anything outstanding.
const saved = ref(null)
const defaults = ref(null)
const form = ref(null)
const busy = ref(false)

const pw = ref({ current: '', next: '', confirm: '' })
const pwBusy = ref(false)
const pwError = ref('')

const tabs = [
  { key: 'general', icon: 'settings', label: 'settings.tab.general' },
  { key: 'clients', icon: 'users', label: 'settings.tab.clients' },
  { key: 'security', icon: 'lock', label: 'settings.tab.security' },
  { key: 'notify', icon: 'alert', label: 'settings.tab.notify' },
  { key: 'backups', icon: 'database', label: 'settings.tab.backups' },
  { key: 'engine', icon: 'shield', label: 'settings.tab.engine' },
  { key: 'logs', icon: 'info', label: 'settings.tab.logs' },
  { key: 'system', icon: 'server', label: 'settings.tab.system' },
]
// The section can be named two ways: as a path, which is what the menu links
// to, or as a hash, which is what older links and bookmarks carry. Both are
// honoured so neither form breaks.
const props = defineProps({ tab: { type: String, default: '' } })

const active = ref(known(props.tab) || tabFromHash())

function known(slug) {
  return tabs.some((x) => x.key === slug) ? slug : ''
}
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

onMounted(load)

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
    const res = await fetch(`/api/backups/${encodeURIComponent(name)}`, {
      headers: { Authorization: `Bearer ${localStorage.getItem('wui.token')}` },
    })
    if (!res.ok) throw new Error(await res.text())
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = name
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    notify(e.message, 'error')
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
  const set = new Set(form.value.notifyKinds || [])
  if (on) set.add(kind)
  else set.delete(kind)
  form.value.notifyKinds = [...set]
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

// Concrete risks, not a lecture. Each one names something an operator can act on.
const warnings = computed(() => {
  const out = []
  if (!info.value) return out
  if (location.protocol !== 'https:') out.push(t('settings.warn.http'))
  if (info.value.listen?.startsWith('0.0.0.0') && location.protocol !== 'https:') {
    out.push(t('settings.warn.exposed'))
  }
  if (!info.value.enforcementActive) out.push(t('settings.warn.enforcement'))
  if (info.value.shapingActive === false) out.push(t('settings.warn.shaping'))
  return out
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

  <p v-if="loading" class="muted">{{ t('common.loading') }}</p>

  <ErrorState v-else-if="loadError && !form" :error="loadError" @retry="load" />

  <template v-else-if="form">
    <!-- Named risks first, because they are the reason to open this page. -->
    <div v-if="warnings.length" class="banner warn stack" role="status">
      <div class="banner-head">
        <Icon name="alert" :size="15" />
        <strong>{{ t('settings.warn.title') }}</strong>
      </div>
      <ul>
        <li v-for="(w, i) in warnings" :key="i">{{ w }}</li>
      </ul>
    </div>

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

    <nav class="cat-tabs" role="tablist">
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
      </template>

      <!-- ── New customer defaults ── -->
      <template v-else-if="active === 'clients'">
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

      <!-- ── Security ── -->
      <template v-else-if="active === 'security'">
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
                <button class="linkbtn danger" @click="removeBackup(b.name)">
                  {{ t('common.delete') }}
                </button>
              </li>
            </ul>
            <p v-else class="muted">{{ t('settings.noBackups') }}</p>
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
