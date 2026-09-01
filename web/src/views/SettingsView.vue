<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { store, t, loadMessages, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'

const info = ref(null)
const loading = ref(true)

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
  { key: 'engine', icon: 'shield', label: 'settings.tab.engine' },
  { key: 'system', icon: 'server', label: 'settings.tab.system' },
]
const active = ref(tabFromHash())

function tabFromHash() {
  const slug = (location.hash || '').replace(/^#/, '')
  return tabs.some((x) => x.key === slug) ? slug : 'general'
}

function selectTab(key) {
  active.value = key
  // Kept in the address bar so a particular section can be linked to, and so a
  // reload does not throw the operator back to the first tab.
  history.replaceState(null, '', `#${key}`)
}

onMounted(load)

async function load() {
  loading.value = true
  try {
    const [sys, cfg] = await Promise.all([api.get('/api/system'), api.get('/api/settings')])
    info.value = sys
    saved.value = cfg.settings
    defaults.value = cfg.defaults
    form.value = { ...cfg.settings }
  } catch (e) {
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
    })
    saved.value = res.settings
    defaults.value = res.defaults
    form.value = { ...res.settings }
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
        <button class="btn primary" :disabled="!dirty || busy" @click="save">
          <span v-if="busy">{{ t('common.saving') }}</span>
          <span v-else>{{ t('common.save') }}</span>
        </button>
        <button class="btn ghost" :disabled="!dirty || busy" @click="revert">
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
