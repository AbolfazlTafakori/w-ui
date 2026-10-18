<script setup>
// Where traffic leaves. Two rows always exist and cannot be removed, so a
// routing rule always has somewhere to point. How a row reads is
// lib/outbounds/rows.js; the table, the cards and the import and export
// dialog are their own; this holds the list and what is mid-request.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { mergeRows, useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import { MODES, modeLabel } from '../lib/outbounds/rows.js'
import Icon from '../components/Icon.vue'
import AntIcon from '../components/AntIcon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MoreMenu from '../components/MoreMenu.vue'
import OutboundForm from '../components/OutboundForm.vue'
import OutboundSubsDialog from '../components/OutboundSubsDialog.vue'
import WarpDialog from '../components/WarpDialog.vue'
import NordDialog from '../components/NordDialog.vue'
import PiaDialog from '../components/PiaDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import OutboundsTable from '../components/outbounds/OutboundsTable.vue'
import OutboundCards from '../components/outbounds/OutboundCards.vue'
import ImportExportDialog from '../components/outbounds/ImportExportDialog.vue'
import { useIsMobile } from '../lib/mobile.js'

const outbounds = ref([])
const loading = ref(true)
const loadError = ref('')
const formFor = ref(null) // null = closed, {} = create, { outbound } = edit
const ask = ref(null)
const busy = ref(false)
const mode = ref('tcp')
const checkingAll = ref(false)
// On a phone the table becomes the classic panel's OutboundCardList.
const isMobile = useIsMobile()

// The Save bar theirs has over every Xray page. Here a change is written the
// moment it is made and the panel pushes it into the kernel on its next tick;
// Save does that push now, and is only enabled once something has changed
// since the last one.
const dirty = ref(false)
const saving = ref(false)
async function saveAll() {
  saving.value = true
  try {
    await api.post('/api/routing/apply')
    dirty.value = false
    notify(t('outbound.saved'), 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    saving.value = false
  }
}

// Which dialog is open: 'subs', 'warp', 'nord', 'pia', 'import', 'export',
// or null.
const dialog = ref(null)
function onDialogChanged() {
  dirty.value = true
  load(true)
}
async function onImported() {
  dirty.value = true
  await load()
}

// Rows mid-request, so probing one hop does not freeze the controls on another.
const pending = ref(new Set())
function hold(id) {
  pending.value = new Set(pending.value).add(id)
}
function release(id) {
  const next = new Set(pending.value)
  next.delete(id)
  pending.value = next
}

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const fresh = await api.get('/api/outbounds', { background: quiet })
    // Merged rather than replaced: a second switch flipped while the first is
    // still settling would otherwise be overwritten by the first one's refresh.
    outbounds.value = quiet ? mergeRows(outbounds.value, fresh, pending.value) : fresh
    loadError.value = ''
  } catch (err) {
    loadError.value = err.message
  } finally {
    loading.value = false
  }
}
const showSkeleton = useDelayed(computed(() => loading.value && !outbounds.value.length))
onMounted(load)

async function check(o) {
  hold(o.id)
  try {
    const res = await api.post(`/api/outbounds/${o.id}/check?mode=${mode.value}`)
    Object.assign(o, {
      latencyMs: res.latencyMs,
      lastError: res.ok ? '' : res.error,
      lastCheckAt: new Date().toISOString(),
    })
    if (res.egress) {
      o.egressIp = res.egress.ipv4 || ''
      o.egressCountry = res.egress.country || ''
    }
    if (!res.ok) notify(`${o.tag}: ${res.error}`, 'error')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    release(o.id)
  }
}

async function checkAll() {
  checkingAll.value = true
  outbounds.value.forEach((o) => hold(o.id))
  try {
    await api.post(`/api/outbounds/check?mode=${mode.value}`)
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    // Released before the refresh: a quiet reload keeps rows that are still
    // pending as they were, which would throw the fresh figures away.
    outbounds.value.forEach((o) => release(o.id))
    checkingAll.value = false
  }
  await load(true)
}

async function setEnabled(o, on) {
  const was = o.enabled
  if (was === on) return
  o.enabled = on
  hold(o.id)
  try {
    const updated = await api.patch(`/api/outbounds/${o.id}`, {
      tag: o.tag,
      kind: o.kind,
      address: o.address,
      enabled: on,
    })
    Object.assign(o, updated)
    dirty.value = true
  } catch (err) {
    o.enabled = was
    notify(err.message, 'error')
  } finally {
    release(o.id)
    load(true)
  }
}

// A row menu choice: the same menu theirs keeps behind the "more" circle.
function pick(o, idx, key) {
  switch (key) {
    case 'edit':
      formFor.value = { outbound: o }
      return
    case 'top':
      return reorder(idx, 0)
    case 'up':
      return reorder(idx, idx - 1)
    case 'down':
      return reorder(idx, idx + 1)
    case 'reset':
      return resetTraffic(o)
    case 'toggle':
      return setEnabled(o, !o.enabled)
    case 'del':
      return remove(o)
  }
}

async function reorder(from, to) {
  const ids = outbounds.value.map((o) => o.id)
  if (to < 0 || to >= ids.length || from === to) return
  const [id] = ids.splice(from, 1)
  ids.splice(to, 0, id)
  try {
    outbounds.value = await api.post('/api/outbounds/order', { ids })
    dirty.value = true
  } catch (err) {
    notify(err.message, 'error')
  }
}

function resetTraffic(o) {
  ask.value = {
    title: t('outbound.resetTraffic'),
    body: t('outbound.resetOneContent'),
    subject: o.tag,
    confirmLabel: t('outbound.reset'),
    run: async () => {
      await api.post(`/api/outbounds/${o.id}/reset`)
      await load(true)
    },
  }
}
function resetAllTraffic() {
  ask.value = {
    title: t('outbound.resetTraffic'),
    body: t('outbound.resetAllContent'),
    confirmLabel: t('outbound.reset'),
    run: async () => {
      await api.post('/api/outbounds/reset')
      await load(true)
    },
  }
}

function remove(o) {
  ask.value = {
    title: t('outbound.removeTitle'),
    body: t('outbound.removeBody'),
    subject: o.tag,
    consequences: [t('outbound.removeConsequence')],
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.del(`/api/outbounds/${o.id}`)
      notify(t('outbound.removed'), 'success')
      dirty.value = true
      await load()
    },
  }
}

async function runConfirmed() {
  if (!ask.value) return
  busy.value = true
  try {
    await ask.value.run()
    ask.value = null
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function onSaved() {
  formFor.value = null
  dirty.value = true
  await load()
}

// The "more" menu: the providers, then import and export, the way theirs
// offers them. Nothing to export until there is an outbound of one's own.
const moreItems = computed(() => [
  { key: 'warp', label: 'WARP', icon: 'CloudOutlined' },
  { key: 'nord', label: 'NordVPN', icon: 'ApiOutlined' },
  { key: 'pia', label: t('outbound.pia.menu'), icon: 'ApiOutlined' },
  { divider: true },
  { key: 'import', label: t('outbound.import'), icon: 'ImportOutlined' },
  { key: 'export', label: t('outbound.export'), icon: 'ExportOutlined', disabled: !outbounds.value.some((o) => !o.builtin) },
])
</script>

<template>
  <section class="view">
    <!-- Their page: the Save bar every Xray page has, then a card with a
         toolbar row -- space-between -- and the table. No heading, no figures. -->
    <div class="card save-bar">
      <div class="save-left">
        <button class="btn primary" :disabled="!dirty || saving" @click="saveAll">
          <span v-if="saving" class="spin sm"></span>
          <span>{{ t('action.save') }}</span>
        </button>
      </div>
      <div class="save-right">
        <div class="alert warning" role="status">
          <Icon name="alert" :size="14" />
          <span>{{ t('outbound.saveHint') }}</span>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-toolbar spread wrap">
        <div class="toolbar-group">
          <button class="btn primary" @click="formFor = {}">
            <AntIcon name="PlusOutlined" />
            <span v-if="!isMobile">{{ t('nav.outbounds') }}</span>
          </button>
          <button class="btn" @click="dialog = 'subs'">
            <AntIcon name="CloudOutlined" />
            <span>{{ t('outbound.sub.manage') }}</span>
          </button>
          <MoreMenu :items="moreItems" button-class="btn more-btn" @pick="(key) => (dialog = key)" />
        </div>

        <div class="toolbar-group">
          <!-- Their Radio.Group buttonStyle="solid" size="small", with the
               same three choices and the same tooltip on it. -->
          <div class="seg sm" role="group" :title="t('outbound.testModeTooltip')">
            <button
              v-for="m in MODES"
              :key="m.value"
              type="button"
              class="seg-btn"
              :class="{ on: mode === m.value }"
              :aria-pressed="mode === m.value"
              @click="mode = m.value"
            >
              {{ modeLabel(m.value) }}
            </button>
          </div>

          <button class="btn primary" :disabled="checkingAll" @click="checkAll">
            <span v-if="checkingAll" class="spin sm"></span>
            <AntIcon v-else name="PlayCircleOutlined" />
            <span v-if="!isMobile">{{ t('outbound.testAll') }}</span>
          </button>

          <button class="btn icon" :aria-label="t('outbound.resetTraffic')" :title="t('outbound.resetTraffic')" @click="resetAllTraffic">
            <AntIcon name="RetweetOutlined" />
          </button>
        </div>
      </div>

      <div v-if="loadError" class="empty empty-cta">
        <Icon name="alert" :size="28" />
        <p>{{ loadError }}</p>
        <button class="btn" @click="load()">{{ t('action.retry') }}</button>
      </div>

      <PageSpin v-else-if="showSkeleton" />
      <div v-else-if="loading" class="empty"></div>
      <OutboundCards v-else-if="isMobile" :outbounds="outbounds" :pending="pending" @pick="pick" @check="check" />
      <OutboundsTable v-else :outbounds="outbounds" :pending="pending" :mode="mode" @edit="(o) => (formFor = { outbound: o })" @pick="pick" @check="check" />
    </div>

    <OutboundSubsDialog v-if="dialog === 'subs'" @close="dialog = null" @changed="onDialogChanged" />
    <WarpDialog v-if="dialog === 'warp'" @close="dialog = null" @changed="onDialogChanged" />
    <NordDialog v-if="dialog === 'nord'" @close="dialog = null" @changed="onDialogChanged" />
    <PiaDialog v-if="dialog === 'pia'" @close="dialog = null" @changed="onDialogChanged" />
    <ImportExportDialog
      v-if="dialog === 'import' || dialog === 'export'"
      :kind="dialog"
      :outbounds="outbounds"
      @close="dialog = null"
      @imported="onImported"
    />

    <OutboundForm
      v-if="formFor"
      :outbound="formFor.outbound"
      :existing-tags="outbounds.map((o) => o.tag)"
      @saved="onSaved"
      @cancel="formFor = null"
    />

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :subject="ask?.subject || ''"
      :consequences="ask?.consequences || []"
      :confirm-label="ask?.confirmLabel || ''"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </section>
</template>
