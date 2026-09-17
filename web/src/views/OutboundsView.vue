<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { mergeRows, useDelayed } from '../lib/live.js'
import { t, tn, notify, store } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Icon from '../components/Icon.vue'
import AntIcon from '../components/AntIcon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import OutboundForm from '../components/OutboundForm.vue'
import OutboundSubsDialog from '../components/OutboundSubsDialog.vue'
import WarpDialog from '../components/WarpDialog.vue'
import NordDialog from '../components/NordDialog.vue'
import PiaDialog from '../components/PiaDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import { useIsMobile } from '../lib/mobile.js'

// Where traffic leaves. Two rows always exist and cannot be removed, so a
// routing rule always has somewhere to point.
const outbounds = ref([])
const loading = ref(true)
const loadError = ref('')
const formFor = ref(null) // null = closed, {} = create, { outbound } = edit
const ask = ref(null)
const busy = ref(false)
const mode = ref('tcp')
const checkingAll = ref(false)
const showEgressIp = ref(false)

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

// Which dialog is open: 'subs', 'warp', 'nord', 'pia', or null.
const dialog = ref(null)
function onDialogChanged() {
  dirty.value = true
  load(true)
}

// Rows mid-request, so probing one hop does not freeze the controls on another.
const pending = ref(new Set())
const isPending = (id) => pending.value.has(id)
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

function kindLabel(o) {
  return t(`outbound.kind.${o.kind}`)
}

const MODES = [
  { value: 'tcp', label: 'TCP' },
  { value: 'http', label: 'HTTP' },
  { value: 'real', label: () => t('outbound.modeRealDelay') },
]
function modeLabel(m) {
  const found = MODES.find((x) => x.value === m)
  return typeof found?.label === 'function' ? found.label() : found?.label || m.toUpperCase()
}

// A latency figure is only meaningful next to when it was taken. One from an
// hour ago next to a hop that has since died reads as proof it is fine.
function checkedAgo(o) {
  if (!o.lastCheckAt) return ''
  const secs = Math.max(0, (Date.now() - new Date(o.lastCheckAt).getTime()) / 1000)
  if (secs < 60) return t('time.justNow')
  if (secs < 3600) return tn('time.minutesAgo', Math.floor(secs / 60))
  return tn('time.hoursAgo', Math.floor(secs / 3600))
}

// The country as a flag and a name in the operator's own language, the way
// theirs shows it. Two regional-indicator code points make the flag.
function countryFlag(code) {
  const c = (code || '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(c)) return ''
  return String.fromCodePoint(...[...c].map((ch) => 0x1f1e6 + ch.charCodeAt(0) - 65))
}
function countryName(code) {
  const c = (code || '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(c)) return ''
  try {
    return new Intl.DisplayNames([store.locale || 'en'], { type: 'region' }).of(c) || c
  } catch {
    return c
  }
}

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

// ── the row menu, the same one theirs keeps behind the "more" circle ──
const menu = ref(null) // { outbound, idx, x, y }
// On a phone the table becomes the classic panel's OutboundCardList.
const isMobile = useIsMobile()
const cardEgressShown = ref(new Set())
function toggleCardEgress(id) {
  const next = new Set(cardEgressShown.value)
  next.has(id) ? next.delete(id) : next.add(id)
  cardEgressShown.value = next
}

function openMenuFor(o, idx, event) {
  if (menu.value?.outbound?.id === o.id) {
    menu.value = null
    return
  }
  const r = event.currentTarget.getBoundingClientRect()
  const height = 6 * 34 + 10
  const up = r.bottom + height > window.innerHeight && r.top > height
  menu.value = { outbound: o, idx, x: r.left, y: up ? r.top - height - 4 : r.bottom + 4 }
}
function closeMenu() {
  menu.value = null
}
function onDocClick(e) {
  if (!menu.value && !moreOpen.value) return
  if (e.target.closest?.('.rowmenu') || e.target.closest?.('.act') || e.target.closest?.('.more-btn')) return
  closeMenu()
  moreOpen.value = false
}
function onKey(e) {
  if (e.key === 'Escape') {
    closeMenu()
    moreOpen.value = false
  }
}
onMounted(() => {
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('resize', closeMenu)
  window.addEventListener('scroll', closeMenu, true)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', closeMenu)
  window.removeEventListener('scroll', closeMenu, true)
})

// Their items, in their order: Move to top (only past the first row), Move
// up, Move down, Reset Traffic, Delete. Enable is ours; theirs has no switch
// on an outbound, so it sits here rather than in a column they do not have.
function menuFor(o, idx) {
  const last = outbounds.value.length - 1
  const items = []
  if (idx > 0) items.push({ key: 'top', icon: 'VerticalAlignTopOutlined', label: t('outbound.moveToTop') })
  items.push(
    { key: 'up', icon: 'ArrowUpOutlined', label: t('outbound.moveUp'), disabled: idx === 0 },
    { key: 'down', icon: 'ArrowDownOutlined', label: t('outbound.moveDown'), disabled: idx === last },
    { key: 'reset', icon: 'RetweetOutlined', label: t('outbound.resetTraffic') },
    {
      key: 'toggle',
      icon: o.enabled ? 'StopOutlined' : 'PlayCircleOutlined',
      label: o.enabled ? t('action.disable') : t('action.enable'),
      disabled: o.builtin,
    },
    { divider: true },
    { key: 'del', icon: 'DeleteOutlined', label: t('action.delete'), danger: true, disabled: o.builtin },
  )
  return items
}

async function pick(o, idx, key) {
  closeMenu()
  switch (key) {
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

// ── the "more" menu: import and export, the way theirs offers them ──
const moreOpen = ref(false)
const importOpen = ref(false)
const importText = ref('')
const exportOpen = ref(false)

// What leaves is what can be re-created: the two built-ins are skipped
// because every panel already has them, and secrets are not in the listing
// to begin with. Their export is the same shape -- an array of outbounds.
const exportText = computed(() =>
  JSON.stringify(
    outbounds.value
      .filter((o) => !o.builtin)
      .map((o) => ({
        tag: o.tag,
        kind: o.kind,
        enabled: o.enabled,
        address: o.address,
        username: o.username || undefined,
        peerPubKey: o.peerPubKey || undefined,
        hopAddress: o.hopAddress || undefined,
        hopDns: o.hopDns || undefined,
        hopMtu: o.hopMtu || undefined,
        note: o.note || undefined,
      })),
    null,
    2,
  ),
)

async function copyExport() {
  try {
    await navigator.clipboard.writeText(exportText.value)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

async function runImport() {
  let parsed
  try {
    parsed = JSON.parse(importText.value)
  } catch {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  // An array, or an object carrying one under "outbounds" -- both forms
  // theirs accepts.
  const list = Array.isArray(parsed) ? parsed : Array.isArray(parsed?.outbounds) ? parsed.outbounds : null
  if (!list) {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  busy.value = true
  let added = 0
  try {
    for (const item of list) {
      if (!item || typeof item !== 'object') continue
      await api.post('/api/outbounds', item)
      added++
    }
    notify(tn('outbound.imported', added), 'success')
    importOpen.value = false
    importText.value = ''
    dirty.value = true
    await load()
  } catch (err) {
    notify(err.message, 'error')
    if (added) await load()
  } finally {
    busy.value = false
  }
}
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
          <div class="more-wrap">
            <button class="btn more-btn" :aria-expanded="moreOpen" @click="moreOpen = !moreOpen">
              <AntIcon name="MoreOutlined" />
              <span>{{ t('outbound.more') }}</span>
            </button>
            <div v-if="moreOpen" class="rowmenu below" role="menu">
              <button class="menu-item" role="menuitem" @click="moreOpen = false; dialog = 'warp'">
                <AntIcon name="CloudOutlined" />WARP
              </button>
              <button class="menu-item" role="menuitem" @click="moreOpen = false; dialog = 'nord'">
                <AntIcon name="ApiOutlined" />NordVPN
              </button>
              <button class="menu-item" role="menuitem" @click="moreOpen = false; dialog = 'pia'">
                <AntIcon name="ApiOutlined" />{{ t('outbound.pia.menu') }}
              </button>
              <hr class="menu-divider" />
              <button class="menu-item" role="menuitem" @click="moreOpen = false; importOpen = true">
                <AntIcon name="ImportOutlined" />{{ t('outbound.import') }}
              </button>
              <button
                class="menu-item"
                role="menuitem"
                :disabled="!outbounds.some((o) => !o.builtin)"
                @click="moreOpen = false; exportOpen = true"
              >
                <AntIcon name="ExportOutlined" />{{ t('outbound.export') }}
              </button>
            </div>
          </div>
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

      <!-- Their columns, in their order and with their widths: #, Tag,
           Address, Egress (with the eye that hides the address), Country,
           Traffic, Latency, Check. -->
      <div v-else-if="isMobile" class="outbound-cards">
        <div v-if="!outbounds.length" class="card-empty">
          <AntIcon name="ExportOutlined" :size="32" />
          <div>{{ t('common.nothingYet') }}</div>
        </div>
        <div v-for="(o, idx) in outbounds" :key="o.id" class="outbound-card" :class="{ off: !o.enabled }">
          <div class="card-head">
            <div class="card-identity">
              <span class="card-num">{{ idx + 1 }}</span>
              <span class="tag-name" :title="o.tag">{{ o.tag }}</span>
              <span class="tag green">{{ kindLabel(o) }}</span>
            </div>
            <button
              class="act round sm"
              :aria-label="t('action.more')"
              :aria-expanded="menu?.outbound?.id === o.id"
              @click="openMenuFor(o, idx, $event)"
            >
              <AntIcon name="MoreOutlined" />
            </button>
          </div>
          <div v-if="o.address" class="address-list">
            <span class="address-pill" :title="o.address">{{ o.address }}</span>
          </div>
          <div v-if="o.egressIp || o.egressCountry" class="card-egress">
            <div class="card-egress-row">
              <span>{{ t('outbound.egress') }}:</span>
              <button type="button" class="ip-toggle" :aria-label="t('outbound.toggleIpVisibility')" @click="toggleCardEgress(o.id)">
                <AntIcon :name="cardEgressShown.has(o.id) ? 'EyeOutlined' : 'EyeInvisibleOutlined'" />
              </button>
              <span v-if="o.egressCountry" class="country-pill">
                <span>{{ countryFlag(o.egressCountry) }}</span>
                <span>{{ countryName(o.egressCountry) || o.egressCountry }}</span>
              </span>
            </div>
            <div v-if="o.egressIp" class="card-egress-row" :title="o.egressIp">
              <span class="egress-family">{{ o.egressIp.includes(':') ? 'v6' : 'v4' }}:</span>
              <span class="egress-ip" :class="cardEgressShown.has(o.id) ? 'address-visible' : 'address-hidden'">{{ o.egressIp }}</span>
            </div>
          </div>
          <div class="card-foot">
            <span class="traffic-up">↑ {{ bytes(o.txBytes || 0) }}</span>
            <span class="traffic-sep"></span>
            <span class="traffic-down">↓ {{ bytes(o.rxBytes || 0) }}</span>
            <span class="card-test">
              <span v-if="o.lastError" class="tag red" :title="o.lastError">{{ t('outbound.failed') }}</span>
              <span v-else-if="o.latencyMs" class="tag green">{{ o.latencyMs }} ms</span>
              <AntIcon v-else-if="isPending(o.id)" name="LoadingOutlined" class="anticon-spin" />
              <button
                class="act round sm primary"
                :aria-label="t('outbound.check')"
                :disabled="isPending(o.id) || o.kind === 'block'"
                @click="check(o)"
              >
                <AntIcon v-if="isPending(o.id)" name="LoadingOutlined" class="anticon-spin" />
                <AntIcon v-else name="ThunderboltOutlined" />
              </button>
            </span>
          </div>
        </div>
      </div>

      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th class="w-num center">#</th>
              <th>{{ t('outbound.tag') }}</th>
              <th>{{ t('outbound.address') }}</th>
              <th class="w-egress">
                <span class="egress-header">
                  {{ t('outbound.egress') }}
                  <button
                    type="button"
                    class="ip-toggle"
                    :aria-label="t('outbound.toggleIpVisibility')"
                    :title="t('outbound.toggleIpVisibility')"
                    @click="showEgressIp = !showEgressIp"
                  >
                    <AntIcon :name="showEgressIp ? 'EyeOutlined' : 'EyeInvisibleOutlined'" />
                  </button>
                </span>
              </th>
              <th class="w-country">{{ t('outbound.country') }}</th>
              <th class="w-traffic">{{ t('outbound.traffic') }}</th>
              <th class="w-latency">{{ t('outbound.latency') }}</th>
              <th class="w-check center">{{ t('outbound.check') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="!outbounds.length" class="empty-row">
              <td colspan="8">
                <div class="card-empty">
                  <Icon name="upload" :size="32" />
                  <div>{{ t('common.nothingYet') }}</div>
                </div>
              </td>
            </tr>
            <tr v-for="(o, idx) in outbounds" :key="o.id" :class="{ off: !o.enabled }">
              <!-- The number with the row's two circles beside it: edit, and
                   the menu with everything else. -->
              <td class="w-num">
                <div class="rownum">
                  <span class="num row-index">{{ idx + 1 }}</span>
                  <div class="action-buttons">
                    <button
                      class="act round sm"
                      :aria-label="t('action.edit')"
                      :title="t('action.edit')"
                      :disabled="isPending(o.id)"
                      @click="formFor = { outbound: o }"
                    >
                      <AntIcon name="EditOutlined" />
                    </button>
                    <button
                      class="act round sm"
                      :aria-label="t('action.more')"
                      :title="t('action.more')"
                      :aria-expanded="menu?.outbound?.id === o.id"
                      @click="openMenuFor(o, idx, $event)"
                    >
                      <AntIcon name="MoreOutlined" />
                    </button>
                  </div>
                </div>
              </td>

              <td>
                <div class="identity-cell">
                  <span class="tag-name" :title="o.tag">{{ o.tag }}</span>
                  <div class="protocol-line">
                    <span class="tag green">{{ kindLabel(o) }}</span>
                  </div>
                </div>
                <div v-if="o.note" class="muted small">{{ o.note }}</div>
              </td>

              <td class="ltr">
                <div class="address-list">
                  <span v-if="o.address" class="address-pill" :title="o.address">{{ o.address }}</span>
                  <span v-else class="muted">—</span>
                </div>
              </td>

              <td class="ltr">
                <div v-if="o.egressIp" class="egress-stack">
                  <span class="egress-address" :title="o.egressIp">
                    <span class="egress-family">{{ o.egressIp.includes(':') ? 'v6' : 'v4' }}</span>
                    <span class="egress-ip" :class="showEgressIp ? 'address-visible' : 'address-hidden'">{{ o.egressIp }}</span>
                  </span>
                </div>
                <span v-else class="muted" :title="t('outbound.egressHint')">—</span>
              </td>

              <td>
                <span v-if="o.egressCountry" class="country-pill" title="Cloudflare trace">
                  <span>{{ countryFlag(o.egressCountry) }}</span>
                  <span>{{ countryName(o.egressCountry) || o.egressCountry }}</span>
                </span>
                <span v-else class="muted" :title="t('outbound.egressHint')">—</span>
              </td>

              <td class="num ltr">
                <span class="traffic">
                  <span class="up">↑ {{ bytes(o.txBytes || 0) }}</span>
                  <span class="down">↓ {{ bytes(o.rxBytes || 0) }}</span>
                </span>
              </td>

              <td class="num ltr">
                <AntIcon v-if="isPending(o.id)" name="LoadingOutlined" class="anticon-spin" />
                <template v-else-if="o.lastError">
                  <span class="tag red" :title="o.lastError">{{ t('outbound.failed') }}</span>
                </template>
                <template v-else-if="o.latencyMs">
                  <span class="tag green">{{ o.latencyMs }} ms</span>
                  <div class="muted small">{{ checkedAgo(o) }}</div>
                </template>
                <span v-else class="muted">—</span>
              </td>

              <td class="center">
                <button
                  class="act round primary check-btn"
                  :aria-label="t('outbound.check')"
                  :title="`${t('outbound.check')} (${modeLabel(mode)})`"
                  :disabled="isPending(o.id) || o.kind === 'block'"
                  @click="check(o)"
                >
                  <AntIcon v-if="isPending(o.id)" name="LoadingOutlined" class="anticon-spin" />
                  <AntIcon v-else name="ThunderboltOutlined" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Teleported: the view keeps a transform from its entrance animation,
         which would make "fixed" relative to it rather than the window. -->
    <Teleport to="body">
    <div
      v-if="menu"
      v-fit
      class="rowmenu"
      role="menu"
      :style="{ top: menu.y + 'px', left: menu.x + 'px' }"
    >
      <template v-for="(m, i) in menuFor(menu.outbound, menu.idx)" :key="m.key || `d${i}`">
        <hr v-if="m.divider" class="menu-divider" />
        <button
          v-else
          class="menu-item"
          :class="{ danger: m.danger }"
          :disabled="m.disabled"
          role="menuitem"
          @click="pick(menu.outbound, menu.idx, m.key)"
        >
          <AntIcon :name="m.icon" />{{ m.label }}
        </button>
      </template>
    </div>
    </Teleport>

    <OutboundSubsDialog v-if="dialog === 'subs'" @close="dialog = null" @changed="onDialogChanged" />
    <WarpDialog v-if="dialog === 'warp'" @close="dialog = null" @changed="onDialogChanged" />
    <NordDialog v-if="dialog === 'nord'" @close="dialog = null" @changed="onDialogChanged" />
    <PiaDialog v-if="dialog === 'pia'" @close="dialog = null" @changed="onDialogChanged" />

    <OutboundForm
      v-if="formFor"
      :outbound="formFor.outbound"
      :existing-tags="outbounds.map((o) => o.tag)"
      @saved="onSaved"
      @cancel="formFor = null"
    />

    <!-- Import: paste, the way theirs takes it. -->
    <div v-if="importOpen" class="modal-backdrop" @click.self="importOpen = false">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ob-import-title">
        <div class="card-head">
          <h2 id="ob-import-title">{{ t('outbound.import') }}</h2>
          <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="importOpen = false">
            <Icon name="close" :size="15" />
          </button>
        </div>
        <div class="card-body">
          <div class="field">
            <textarea v-model="importText" class="ltr mono" rows="12" spellcheck="false" placeholder="[ { ... } ]"></textarea>
          </div>
        </div>
        <div class="modal-foot">
          <button type="button" class="btn ghost" @click="importOpen = false">{{ t('action.cancel') }}</button>
          <button class="btn primary" :disabled="busy || !importText.trim()" @click="runImport">
            <span v-if="busy" class="spin"></span>
            <template v-else>{{ t('outbound.import') }}</template>
          </button>
        </div>
      </div>
    </div>

    <div v-if="exportOpen" class="modal-backdrop" @click.self="exportOpen = false">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ob-export-title">
        <div class="card-head">
          <h2 id="ob-export-title">{{ t('outbound.export') }}</h2>
          <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="exportOpen = false">
            <Icon name="close" :size="15" />
          </button>
        </div>
        <div class="card-body">
          <div class="field">
            <textarea class="ltr mono" rows="12" readonly spellcheck="false" :value="exportText"></textarea>
          </div>
        </div>
        <div class="modal-foot">
          <button type="button" class="btn ghost" @click="exportOpen = false">{{ t('common.close') }}</button>
          <button class="btn primary" @click="copyExport">
            <Icon name="copy" :size="14" />
            <span>{{ t('action.copy') }}</span>
          </button>
        </div>
      </div>
    </div>

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
