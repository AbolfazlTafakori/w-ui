<script setup>
// The engine pages: what the classic panel keeps under "Xray" -- the core's own
// settings, its balancers, its DNS and the raw template -- laid out the
// same way, for the kernel tunnels this panel runs instead of a core. The
// settings are held here and shared with the tabs; each tab is its own.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { useLeaveGuard } from '../lib/leaveGuard.js'
import { t, notify } from '../lib/store.js'
import { ADV, useTemplateDoc } from '../lib/engine/template.js'
import { provideEngine } from '../components/engine/context.js'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import LeaveDialog from '../components/LeaveDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import BasicTab from '../components/engine/BasicTab.vue'
import BalancersTab from '../components/engine/BalancersTab.vue'
import DnsTab from '../components/engine/DnsTab.vue'

const props = defineProps({ tab: { type: String, default: '' } })

const SECTIONS = ['basic', 'balancer', 'dns', 'advanced']
function known(slug) {
  return SECTIONS.includes(slug) ? slug : ''
}
function fromHash() {
  return known((location.hash || '').replace(/^#/, '')) || 'basic'
}
const active = ref(known(props.tab) || fromHash())
watch(
  () => props.tab,
  (v) => {
    const next = known(v)
    if (next && next !== active.value) active.value = next
  },
)
function onHash() {
  if (!props.tab) active.value = fromHash()
}
const inner = ref({ basic: '1', balancer: 'balancers', dns: '1' })

// ── the settings ──
const loading = ref(true)
const loadError = ref(null)
const saved = ref(null)
const defaults = ref(null)
const form = ref(null)
const dnsListening = ref([])
const busy = ref(false)
const showWait = useDelayed(computed(() => loading.value && !form.value))

const dirty = computed(() => !!form.value && !!saved.value && JSON.stringify(form.value) !== JSON.stringify(saved.value))

function take(res) {
  saved.value = res.settings
  defaults.value = res.defaults
  form.value = JSON.parse(JSON.stringify(res.settings))
  dnsListening.value = res.dnsListening || []
}

async function load() {
  loading.value = true
  try {
    take(await api.get('/api/engine'))
    loadError.value = null
  } catch (e) {
    loadError.value = e
  } finally {
    loading.value = false
  }
}

async function save() {
  busy.value = true
  try {
    take(await api.put('/api/engine', form.value))
    notify(t('eng.saved'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

const askReset = ref(false)
async function resetDefaults() {
  askReset.value = false
  busy.value = true
  try {
    take(await api.post('/api/engine/reset'))
    notify(t('eng.resetDone'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

// Leaving with an unsaved edit asks, as the settings page does.
const { pendingRoute, stay, discard, saveAndGo } = useLeaveGuard({ dirty, save })

function isDefault(key) {
  return !!form.value && !!defaults.value && form.value[key] === defaults.value[key]
}

provideEngine({ form, inner, isDefault, dnsListening })

// ── advanced ──
const tpl = useTemplateDoc({ onApplied: load })
const adv = tpl.adv

// The header's Save: the settings on the first three pages, the document
// on the fourth.
const saveDisabled = computed(() => busy.value || tpl.busy.value || (active.value === 'advanced' ? !tpl.canSave() : !dirty.value))
function onSave() {
  if (active.value === 'advanced') return tpl.save()
  return save()
}

watch(active, (v) => {
  if (v === 'advanced' && !tpl.doc.value) tpl.load()
})

onMounted(() => {
  load()
  if (active.value === 'advanced') tpl.load()
  window.addEventListener('hashchange', onHash)
})
onUnmounted(() => window.removeEventListener('hashchange', onHash))
</script>

<template>
  <div class="antpage engine-page">
    <PageSpin v-if="showWait" />
    <div v-else-if="loading" class="empty"></div>
    <ErrorState v-else-if="loadError && !form" :error="loadError" @retry="load" />

    <template v-else-if="form">
      <!-- Their header card: Save, and the standing note. -->
      <div class="acard">
        <div class="acard-body">
          <div class="header-row wide">
            <div class="header-actions">
              <div class="aspace">
                <button class="abtn primary" :disabled="saveDisabled" @click="onSave">{{ t('eng.save') }}</button>
              </div>
            </div>
            <div class="header-info">
              <div class="aalert warning">
                <AntIcon name="ExclamationCircleFilled" />
                <div class="aalert-body"><span class="aalert-title">{{ t('eng.infoDesc') }}</span></div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="acard">
        <div class="acard-body">
          <BasicTab v-if="active === 'basic'" :busy="busy" @reset="askReset = true" />
          <BalancersTab v-else-if="active === 'balancer'" />
          <DnsTab v-else-if="active === 'dns'" />

          <!-- ══ Advanced ══ -->
          <template v-else>
            <div class="advanced-meta">
              <h4>{{ t('eng.template') }}</h4>
              <p>{{ t('eng.templateDesc') }}</p>
            </div>
            <div class="aradio-group" style="margin: 12px 0">
              <button v-for="k in ADV" :key="k" class="aradio-btn" :class="{ active: adv === k }" @click="adv = k">{{ t(`eng.adv.${k}`) }}</button>
            </div>
            <textarea class="atextarea json" :value="tpl.text.value" :readonly="adv === 'programs'" spellcheck="false" @input="tpl.onInput($event.target.value)"></textarea>
            <p v-if="tpl.error.value" class="json-error ltr">{{ tpl.error.value }}</p>
          </template>
        </div>
      </div>
    </template>

    <ConfirmDialog
      :open="askReset"
      :title="t('eng.resetDefaultConfig')"
      :body="t('eng.resetBody')"
      :confirm-label="t('eng.resetDefaultConfig')"
      :danger="true"
      :busy="busy"
      @confirm="resetDefaults"
      @cancel="askReset = false"
    />
    <LeaveDialog :open="!!pendingRoute" :busy="busy" @stay="stay" @discard="discard" @save="saveAndGo" />
  </div>
</template>

<style>
/* Keyed on the page's root rather than scoped, so the components it is
   made of are styled as when their markup was the page's. */
/* Their header row: actions 14/24 wide, the note 10/24, wrapping. */
.engine-page .header-row { display: flex; flex-wrap: wrap; align-items: center; margin: 0 -8px; }
.engine-page .header-actions { flex: 0 0 58.3333%; max-width: 58.3333%; padding: 4px 8px; }
.engine-page .header-info { flex: 0 0 41.6667%; max-width: 41.6667%; padding: 0 8px; display: flex; justify-content: flex-end; }
@media (max-width: 575px) {
  .engine-page .header-actions, .engine-page .header-info { flex: 0 0 100%; max-width: 100%; }
}
.engine-page .gap-16 { gap: 16px; }
.engine-page .aempty-desc { color: var(--faint); }
.engine-page .ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.engine-page .json-error { margin: 8px 0 0; color: var(--bad); font-size: 12px; }
.engine-page .muted { color: var(--faint); }
.engine-page .muted-break { word-break: break-all; }
.engine-page .atable td.center { text-align: center; }
.engine-page .atable th.center { text-align: center; }
.engine-page .preset-warning { margin-bottom: 12px; }
.engine-page .preset-list { border: 1px solid var(--line-soft); border-radius: 8px; overflow: hidden; }
.engine-page .preset-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 12px 24px; border-bottom: 1px solid var(--line-soft); }
.engine-page .preset-row:last-child { border-bottom: 0; }
.engine-page .preset-name { font-weight: 500; }
.engine-page .atag.orange { background: var(--tag-orange-bg); border-color: var(--tag-orange-line); color: var(--tag-orange-ink); }
</style>
