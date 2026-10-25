<script setup>
// The settings page, laid out the way the classic panel lays its own out: a card with
// Save and Restart Panel and the standing warning, then a card holding the
// category the sidebar chose -- General, Authentication, Telegram Bot, Email,
// Subscription -- each a row of tabs over lists of setting rows.
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { waitForRestart } from '../lib/restart.js'
import { useDelayed } from '../lib/live.js'
import { useLeaveGuard } from '../lib/leaveGuard.js'
import { store, t, loadMessages, loadPanelSettings, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import LeaveDialog from '../components/LeaveDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import SecuritySettings from '../components/settings/SecuritySettings.vue'
import BackupArchives from '../components/settings/BackupArchives.vue'
import LogsPanel from '../components/settings/LogsPanel.vue'
import GeneralTab from '../components/settings/GeneralTab.vue'
import TelegramTab from '../components/settings/TelegramTab.vue'
import EmailTab from '../components/settings/EmailTab.vue'
import SubscriptionTab from '../components/settings/SubscriptionTab.vue'
import { provideSettings } from '../components/settings/context.js'

// ── which page ──────────────────────────────────────────────────────────────
const categories = ['general', 'security', 'telegram', 'bale', 'email', 'subscription']
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
    }
  },
)
window.addEventListener('hashchange', () => {
  if (!props.tab) active.value = tabFromHash()
})

// The tab inside each category, as theirs: each remembers its own.
const inner = ref({ general: '1', security: '1', telegram: '1', bale: '1', email: '1', subscription: '1' })

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

const outboundTags = ref([])
const balancerTags = ref([])

onMounted(() => {
  load()
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
  // Wait for it to go away and come back, then reload onto it.
  if (!(await waitForRestart())) {
    restarting.value = false
    notify(t('overview.restoreNotBack'), 'error')
  }
}

// Leaving with an unsaved edit asks first.
const { pendingRoute, stay, discard, saveAndGo } = useLeaveGuard({ dirty, save })

// A value equal to what the panel ships as gets their "Default" tag.
function isDefault(key) {
  if (!form.value || !defaults.value) return false
  return form.value[key] === defaults.value[key]
}

// The tabs edit the same form through this; their tests save first, since
// the server tests what it has stored.
async function saveIfDirty() {
  if (dirty.value) await save()
}
provideSettings({ form, saved, defaults, info, inner, sub, subError, isDefault, outboundTags, balancerTags, saveIfDirty })

// ── security warnings ────────────────────────────────────────────────────────
const alertVisible = ref(true)
const warnings = ref([])
onMounted(async () => {
  try {
    const res = await api.get('/api/security/warnings', { background: true })
    // Each warning is named by its id, so it reads in the panel's language;
    // a number or a path in the English title is carried over into the
    // translation. One without a translation shows as the server wrote it.
    warnings.value = (res.warnings || []).map((w) => {
      const key = 'secwarn.' + (w.id || '')
      const text = t(key, { n: (String(w.title || '').match(/\d+/) || [''])[0], path: (String(w.title || '').match(/"([^"]+)"/) || ['', ''])[1] })
      return text === key ? w.title || w.detail || String(w) : text
    })
  } catch {
    warnings.value = []
  }
})

// ── the pages outside the menu: backups, engine, logs, system ────────────────
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
            <GeneralTab />
          </template>

          <!-- ══ Authentication ══ -->
          <template v-else-if="active === 'security'">
            <SecuritySettings />
          </template>

          <!-- ══ Telegram Bot ══ -->
          <template v-else-if="active === 'telegram'">
            <TelegramTab key="telegram" />
          </template>

          <!-- ══ Bale Bot: the same bot, on Bale ══ -->
          <template v-else-if="active === 'bale'">
            <TelegramTab key="bale" platform="bale" />
          </template>

          <!-- ══ Email ══ -->
          <template v-else-if="active === 'email'">
            <EmailTab />
          </template>

          <!-- ══ Subscription ══ -->
          <template v-else-if="active === 'subscription' && sub">
            <SubscriptionTab />
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
            <BackupArchives />
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
            <LogsPanel />
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
    <LeaveDialog :open="!!pendingRoute" :busy="busy" @stay="stay" @discard="discard" @save="saveAndGo" />

  </div>
</template>

<style>
/* Unscoped, keyed on the page's root, so its tab components are styled too. */
/* Their header row: actions in a 10/24 column, the note in the other 14/24,
   flushed to its end. */
.settings-page .header-row { display: flex; flex-wrap: wrap; align-items: center; }
.settings-page .header-actions { flex: 0 0 41.6667%; max-width: 41.6667%; padding: 4px; }
.settings-page .header-info { flex: 0 0 58.3333%; max-width: 58.3333%; display: flex; justify-content: flex-end; }
@media (max-width: 575px) {
  .settings-page .header-actions, .settings-page .header-info { flex: 0 0 100%; max-width: 100%; }
  .settings-page .header-info { justify-content: flex-start; margin-top: 8px; }
}
.settings-page .conf-alert { margin-bottom: 10px; }
.settings-page .abtn.danger-primary { background: var(--bad-fill, var(--bad)); border-color: var(--bad-fill, var(--bad)); color: #fff; }
.settings-page .abtn.danger-primary:hover:not(:disabled) { opacity: 0.85; }
.settings-page .abtn:disabled { color: var(--faint); background: var(--surface-2); border-color: var(--line); box-shadow: none; cursor: not-allowed; }
.settings-page .abtn.text.danger-text { color: var(--bad); }
.settings-page .abtn.text.danger-text:hover { background: var(--bad-soft); color: var(--bad); }
.settings-page .abtn.addon { flex: none; background: var(--surface-2); color: var(--muted); cursor: default; }

/* Their notification cards: a grid of small outlined Cards. */
.settings-page .notif-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 12px; }
.settings-page .notif-card { border-width: 1px; }
.settings-page .notif-card:hover { box-shadow: none; border-color: var(--line-soft); }
.settings-page .acard-head.small { min-height: 38px; padding: 0 12px; font-size: 14px; font-weight: 600; justify-content: space-between; }
.settings-page .notif-title { display: inline-flex; align-items: center; gap: 4px; }
.settings-page .notif-extra { display: inline-flex; align-items: center; gap: 8px; }
.settings-page .notif-extra .atag { margin: 0; }

.settings-page .field-error { margin: 4px 0 0; color: var(--bad); font-size: 12px; }

/* The template picker: a card per look, its swatch a small drawing of it. */
.settings-page .sub-former-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.settings-page .sub-former-row .atag { white-space: nowrap; }
@media (max-width: 600px) { .settings-page .sub-former-row { flex-wrap: wrap; } }
.settings-page .sub-former-row .ainput.number { width: 96px; flex: none; }
.settings-page .sub-former-row .ainput.block { flex: 1; min-width: 0; }
.settings-page .tpl-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 12px; padding: 10px 20px; }
.settings-page .tpl-card { position: relative; display: flex; gap: 12px; padding: 12px; border: 1px solid var(--line-soft); border-radius: 8px; background: var(--surface-2); cursor: pointer; transition: border-color .2s, box-shadow .2s; }
.settings-page .tpl-card:hover { border-color: var(--accent-hover); }
.settings-page .tpl-card.on { border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-ring); }
.settings-page .tpl-radio { position: absolute; opacity: 0; width: 1px; height: 1px; }
.settings-page .tpl-swatch { position: relative; flex: none; width: 96px; height: 72px; border-radius: 6px; overflow: hidden; background: #0a090a; display: flex; flex-direction: column; gap: 5px; padding: 8px; }
.settings-page .tpl-sw-head { height: 8px; width: 60%; border-radius: 2px; background: rgba(255,255,255,.35); }
.settings-page .tpl-sw-row { height: 5px; width: 80%; border-radius: 2px; background: rgba(255,255,255,.14); }
.settings-page .tpl-sw-row.short { width: 55%; }
.settings-page .tpl-sw-bar { margin-top: auto; height: 6px; width: 100%; border-radius: 3px; background: linear-gradient(90deg, var(--accent) 70%, rgba(255,255,255,.12) 70%); }
.settings-page .tpl-swatch.aurora { background: radial-gradient(60px 40px at 15% 10%, #e02e3d99, transparent 70%), radial-gradient(60px 40px at 90% 40%, #3a5bff99, transparent 70%), radial-gradient(60px 40px at 40% 110%, #17b3a699, transparent 70%), #07111f; }
.settings-page .tpl-swatch.waves { background: linear-gradient(180deg, #0b1220, #12203a); }
.settings-page .tpl-swatch.waves::after { content: ''; position: absolute; left: -30%; right: -30%; bottom: -18px; height: 34px; border-radius: 45%; background: linear-gradient(90deg, #e02e3d, #2f6df6); opacity: .5; }
.settings-page .tpl-swatch.network { background-color: #050608; background-image: radial-gradient(circle at 20% 30%, #e9edf3 1px, transparent 2px), radial-gradient(circle at 70% 20%, #e9edf3 1px, transparent 2px), radial-gradient(circle at 55% 75%, #e9edf3 1px, transparent 2px), linear-gradient(35deg, transparent 49%, rgba(224,46,61,.35) 50%, transparent 51%); }
.settings-page .tpl-swatch.minimal { background: #fff; }
.settings-page .tpl-swatch.minimal .tpl-sw-head { background: #111; } .settings-page .tpl-swatch.minimal .tpl-sw-row { background: #ddd; }
.settings-page .tpl-swatch.midnight { background: radial-gradient(70px 40px at 50% -10%, rgba(224,46,61,.45), transparent 70%), #000; box-shadow: inset 0 0 0 1px rgba(224,46,61,.4); }
.settings-page .tpl-swatch.ember { background: radial-gradient(80px 50px at 50% 110%, rgba(255,106,61,.7), rgba(224,46,61,.25) 50%, transparent 75%), #0f0b0a; }
.settings-page .tpl-swatch.ocean { background: linear-gradient(180deg, #06202f, #04121c); }
.settings-page .tpl-swatch.ocean::after { content: ''; position: absolute; left: -30%; right: -30%; top: -22px; height: 34px; border-radius: 42%; background: linear-gradient(90deg, #19b8e6, #0b5f8a); opacity: .5; }
.settings-page .tpl-swatch.forest { background: radial-gradient(60px 40px at 90% -10%, rgba(46,204,113,.55), transparent 70%), radial-gradient(60px 40px at 0% 110%, rgba(23,179,166,.4), transparent 70%), #08120c; }
.settings-page .tpl-swatch.sunset { background: linear-gradient(180deg, #2b1449, #6a2a5f 45%, #c8553d 80%, #ff8c42); }
.settings-page .tpl-swatch.neon { background: #050008; box-shadow: inset 0 0 0 1px #ff2bd6, inset 0 0 14px -4px #ff2bd6; }
.settings-page .tpl-swatch.neon .tpl-sw-bar { background: #00e5ff; }
.settings-page .tpl-swatch.glass { background: radial-gradient(50px 40px at 10% 10%, #8b5cf6aa, transparent 70%), radial-gradient(50px 40px at 90% 50%, #22d3eeaa, transparent 70%), radial-gradient(50px 40px at 40% 110%, #f472b6aa, transparent 70%), #dfe6f3; }
.settings-page .tpl-swatch.glass .tpl-sw-head { background: rgba(20,26,38,.7); } .settings-page .tpl-swatch.glass .tpl-sw-row { background: rgba(20,26,38,.2); }
.settings-page .tpl-swatch.paper { background: #fbf8f2; box-shadow: inset 0 0 0 1px #d9d0be; }
.settings-page .tpl-swatch.paper .tpl-sw-head { background: #2b241c; } .settings-page .tpl-swatch.paper .tpl-sw-row { background: #ddd3c0; } .settings-page .tpl-swatch.paper .tpl-sw-bar { background: #9c2f2f; }
.settings-page .tpl-swatch.terminal { background: #020402; box-shadow: inset 0 0 0 1px #1d3d24; }
.settings-page .tpl-swatch.terminal .tpl-sw-head, .settings-page .tpl-swatch.terminal .tpl-sw-bar { background: #37ff6e; } .settings-page .tpl-swatch.terminal .tpl-sw-row { background: rgba(55,255,110,.35); }
.settings-page .tpl-swatch.carbon { background-color: #101113; background-image: linear-gradient(27deg, #151618 3px, transparent 3px), linear-gradient(207deg, #151618 3px, transparent 3px), linear-gradient(90deg, #131416 6px, transparent 6px); background-size: 12px 12px; }
.settings-page .tpl-swatch.carbon .tpl-sw-bar { background: #c9ced4; }
.settings-page .tpl-swatch.royal { background: radial-gradient(80px 50px at 50% -20%, rgba(212,175,55,.35), transparent 60%), #0d1330; box-shadow: inset 0 0 0 1px rgba(212,175,55,.6); }
.settings-page .tpl-swatch.royal .tpl-sw-bar { background: #d4af37; }
.settings-page .tpl-swatch.sakura { background: radial-gradient(60px 40px at 100% 0%, rgba(232,97,140,.3), transparent 60%), #fff6f8; box-shadow: inset 0 0 0 1px #f5cfd9; }
.settings-page .tpl-swatch.sakura .tpl-sw-head { background: #3a2530; } .settings-page .tpl-swatch.sakura .tpl-sw-row { background: #f5cfd9; } .settings-page .tpl-swatch.sakura .tpl-sw-bar { background: #e8618c; }
.settings-page .tpl-swatch.frost { background: #e9f2fb; box-shadow: inset 0 0 0 1px #cfe0f1; }
.settings-page .tpl-swatch.frost .tpl-sw-head { background: #10233a; } .settings-page .tpl-swatch.frost .tpl-sw-row { background: #cfe0f1; } .settings-page .tpl-swatch.frost .tpl-sw-bar { background: #1f6fe0; }
.settings-page .tpl-swatch.graphite { background: #292c30; border-radius: 12px; }
.settings-page .tpl-swatch.graphite .tpl-sw-bar { background: #3b82f6; }
.settings-page .tpl-swatch.mesh { background: radial-gradient(60px 50px at 0% 0%, #a855f7cc, transparent 70%), radial-gradient(60px 50px at 100% 30%, #ec4899cc, transparent 70%), radial-gradient(60px 50px at 50% 120%, #06b6d4cc, transparent 70%), #0b0b14; }
.settings-page .tpl-swatch.retro { background: linear-gradient(180deg, #12041f 0%, #3a0a5c 50%, #12041f 51%); }
.settings-page .tpl-swatch.retro::after { content: ''; position: absolute; left: 50%; top: 30%; width: 28px; height: 28px; transform: translateX(-50%); border-radius: 50%; background: linear-gradient(180deg, #ffd166, #ff0099); }
.settings-page .tpl-swatch.retro .tpl-sw-bar { background: #ff0099; }
.settings-page .tpl-meta { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.settings-page .tpl-name { font-size: 14px; font-weight: 500; display: flex; align-items: center; gap: 8px; }
.settings-page .tpl-desc { font-size: 12px; color: var(--faint); line-height: 1.5; }
.settings-page .tpl-meta .abtn { align-self: flex-start; margin-top: 4px; }
</style>
