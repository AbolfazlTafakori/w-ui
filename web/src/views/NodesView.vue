<script setup>
import { watch, computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { mergeRows, useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes, bytesToGigabytes, gigabytesToBytes } from '../lib/format.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import AntIcon from '../components/AntIcon.vue'
import { useIsMobile } from '../lib/mobile.js'

const nodes = ref([])
// On a phone the table becomes the classic panel's node cards: the head opens the
// readings, the info glyph opens the figures, and the actions sit in one menu.
const isMobile = useIsMobile()
const expandedIds = ref(new Set())
function toggleExpanded(id) {
  const next = new Set(expandedIds.value)
  next.has(id) ? next.delete(id) : next.add(id)
  expandedIds.value = next
}
const nodeMenu = ref(null)
function openNodeMenu(n, e) {
  e.stopPropagation()
  const r = e.currentTarget.getBoundingClientRect()
  nodeMenu.value = nodeMenu.value?.node?.id === n.id ? null : { node: n, x: Math.max(8, r.right - 180), y: r.bottom + 4 }
}
function nodeAction(key) {
  const n = nodeMenu.value?.node
  nodeMenu.value = null
  if (!n) return
  if (key === 'probe') probe(n)
  else if (key === 'update') askNodeUpdate(n)
  else if (key === 'edit') openEdit(n)
  else if (key === 'delete') remove(n)
}
function closeNodeMenu(e) {
  if (nodeMenu.value && !e.target.closest?.('.rowmenu')) nodeMenu.value = null
}
const loading = ref(true)

// Declared here, after the state it reads: useDelayed watches with `immediate`
// and evaluates its source during setup.
const showSkeleton = useDelayed(computed(() => loading.value && !nodes.value.length))
const loadError = ref(null)
const busy = ref(false)
const dialog = ref(null) // { kind, node }
const form = ref({ name: '', address: '', token: '', note: '' })
const issued = ref(null) // a token, shown once
let timer = null

onMounted(() => {
  load()
  // The panel probes on its own every half minute; this only re-reads what it
  // already found, so it can be frequent without costing a remote request.
  timer = setInterval(() => load(true), 15_000)
})
onBeforeUnmount(() => clearInterval(timer))

async function load(quiet = false) {
  try {
    const fresh = await api.get('/api/nodes', { background: quiet })
    nodes.value = quiet ? mergeRows(nodes.value, fresh, pending.value) : fresh
    loadError.value = null
  } catch (e) {
    loadError.value = e
  } finally {
    loading.value = false
  }
}

const remote = computed(() => nodes.value.filter((n) => n.kind !== 'local'))
const totals = computed(() => ({
  all: nodes.value.length,
  online: nodes.value.filter((n) => n.kind === 'local' || n.reachable).length,
  offline: remote.value.filter((n) => !n.reachable).length,
  // Averaged over the ones that answered: including a node that timed out
  // would report a latency nobody experienced.
  latency: (() => {
    const live = remote.value.filter((n) => n.reachable && n.latencyMs > 0)
    if (!live.length) return null
    return Math.round(live.reduce((a, n) => a + n.latencyMs, 0) / live.length)
  })(),
}))

function openAdd() {
  form.value = {
    name: '', address: '', token: '', note: '',
    usageCoefficient: 1, dataLimitGB: '', resetDay: '',
    tlsMode: 'verify', tlsPin: '', allowPrivateAddress: false,
  }
  dialog.value = { kind: 'add' }
}

function openEdit(n) {
  // The token is deliberately blank: it is stored as given and never sent back,
  // so the field means "replace it" rather than "here is what it is".
  form.value = {
    name: n.name,
    address: n.address,
    token: '',
    note: n.note || '',
    usageCoefficient: n.usageCoefficient || 1,
    tlsMode: n.tlsMode || 'verify',
    allowPrivateAddress: !!n.allowPrivateAddress,
    tlsPin: n.tlsPin || '',
    dataLimitGB: bytesToGigabytes(n.dataLimitBytes),
    resetDay: n.resetDay || '',
  }
  dialog.value = { kind: 'edit', node: n }
}

// Asking a node to update its own panel.
//
// The answer is passed back whole: "this build carries no release-signing key"
// is something an operator can act on, and is a different problem from the node
// not answering at all.
async function askNodeUpdate(node) {
  ask.value = {
    title: t('update.askNodeTitle'),
    subject: node.name,
    body: t('update.askNodeBody'),
    confirmLabel: t('update.install'),
    run: async () => {
      const res = await api.post(`/api/nodes/${node.id}/update`)
      if (res?.updated) {
        notify(t('update.nodeUpdated').replace('{v}', res.to || ''), 'ok')
      } else {
        notify(res?.notice || t('update.upToDate'), 'ok')
      }
      await load()
    },
  }
}

const pinBusy = ref(false)

// This panel's own authority, which an operator copies once into each node.
//
// Only ever read here: the key behind it stays in this panel, which is the
// whole reason a certificate is worth more than a token that travels.
const authority = ref('')
const authorityBusy = ref(false)

async function loadAuthority() {
  authorityBusy.value = true
  try {
    const res = await api.get('/api/nodes/mtls/authority')
    authority.value = res?.caCert || ''
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    authorityBusy.value = false
  }
}

async function copyAuthority() {
  try {
    await navigator.clipboard.writeText(authority.value)
    notify(t('node.authorityCopied'), 'ok')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

// Fetched as soon as the mode is chosen, because the value is the point of
// choosing it and a button nobody presses leaves an empty box.
watch(
  () => form.value.tlsMode,
  (mode) => {
    if (mode === 'mtls' && !authority.value) loadAuthority()
  },
)

// Read the fingerprint the address is presenting, so nobody has to run openssl
// and copy a hash by hand — which is how certificate checking ends up switched
// off instead. Nothing is verified while reading it, and the hint under the
// field says so.
async function fetchPin() {
  const address = (form.value.address || '').trim()
  if (!address) {
    notify(t('node.addressFirst'), 'error')
    return
  }
  pinBusy.value = true
  try {
    const res = await api.post('/api/nodes/fetch-pin', {
      address,
      allowPrivateAddress: !!form.value.allowPrivateAddress,
    })
    form.value.tlsPin = res?.tlsPin || ''
    notify(t('node.pinFetched'), 'ok')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    pinBusy.value = false
  }
}

async function submit() {
  busy.value = true
  try {
    const body = {
      ...form.value,
      usageCoefficient: Number(form.value.usageCoefficient) || 1,
      tlsMode: form.value.tlsMode || 'verify',
      allowPrivateAddress: !!form.value.allowPrivateAddress,
      tlsPin: (form.value.tlsPin || '').trim(),
      dataLimitBytes: gigabytesToBytes(form.value.dataLimitGB),
      resetDay: Number(form.value.resetDay) || 0,
      dataLimitGB: undefined,
    }
    if (dialog.value.kind === 'add') await api.post('/api/nodes', body)
    else await api.patch(`/api/nodes/${dialog.value.node.id}`, body)
    dialog.value = null
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

// Which rows are mid-request, by node id.
//
// A probe crosses the network to another server: it can take seconds, and it
// can time out. Marking the whole page busy meant the operator could not probe
// a second node while the first was still answering, and the row they had just
// clicked looked exactly like the ones they had not.
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

async function probe(n) {
  hold(n.id)
  try {
    await api.post(`/api/nodes/${n.id}/probe`)
    // Quiet: the row's own spinner already says something is happening, and a
    // second indicator in the header for the same click reads as two things
    // going on at once.
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    release(n.id)
  }
}

// The box ticks now; the panel catches up.
async function toggle(n, enabled) {
  const was = n.enabled
  if (was === enabled) return
  n.enabled = enabled
  hold(n.id)
  try {
    const updated = await api.patch(`/api/nodes/${n.id}`, {
      name: n.name,
      address: n.address,
      enabled,
    })
    Object.assign(n, updated)
  } catch (e) {
    // Put the box back where it was rather than leaving it showing a state the
    // panel never reached.
    n.enabled = was
    notify(e.message, 'error')
  } finally {
    release(n.id)
    load(true)
  }
}

const ask = ref(null)

function remove(n) {
  ask.value = {
    title: t('node.confirmDeleteTitle'),
    body: t('node.confirmDeleteBody'),
    subject: n.name,
    // Worth saying plainly: this is a change to this panel's list, not to the
    // server at the other end. Its customers keep working.
    consequences: [t('node.consequenceRemote'), t('node.consequenceToken')],
    confirmLabel: t('action.delete'),
    run: async () => {
      try {
        await api.del(`/api/nodes/${n.id}`)
        await load()
      } catch (e) {
        notify(e.message, 'error')
      }
    },
  }
}

async function runConfirmed() {
  const spec = ask.value
  if (!spec) return
  busy.value = true
  try {
    await spec.run()
  } finally {
    busy.value = false
    ask.value = null
  }
}

function openIssue() {
  form.value = { name: '', address: '', token: '', note: '' }
  dialog.value = { kind: 'token' }
}

async function issueToken() {
  const name = (form.value.name || '').trim()
  if (!name) return
  busy.value = true
  try {
    issued.value = await api.post('/api/tokens', { name })
    dialog.value = null
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(issued.value.token)
    notify(t('node.tokenCopied'), 'ok')
  } catch {
    notify(t('api.copyFailed'), 'error')
  }
}

function ago(iso) {
  if (!iso) return t('node.never')
  const secs = Math.round((Date.now() - new Date(iso)) / 1000)
  if (secs < 60) return t('node.justNow')
  if (secs < 3600) return tn('node.minutesAgo', Math.round(secs / 60))
  if (secs < 86400) return tn('node.hoursAgo', Math.round(secs / 3600))
  return tn('node.daysAgo', Math.round(secs / 86400))
}

function uptime(secs) {
  if (!secs) return '—'
  const d = Math.floor(secs / 86400)
  const h = Math.floor((secs % 86400) / 3600)
  return d > 0 ? `${d}d ${h}h` : `${h}h ${Math.floor((secs % 3600) / 60)}m`
}

// Latency is coloured the way a person judges it: usable, sluggish, painful.
function latencyTone(ms) {
  if (!ms) return 'grey'
  if (ms < 120) return 'green'
  if (ms < 400) return 'orange'
  return 'red'
}
</script>

<template>
  <!-- No page heading. the classic panel opens this page straight on the figures, and
       both controls live in the table's card, where the rows they act on are.
       Add node takes the primary spot on the left; the token button sits at
       the far end the way their Node mTLS does -- both are how a node proves
       itself to this panel, and that is the position they gave that. -->
  <div class="strip card">
    <div class="strip-item">
      <span class="strip-label"><Icon name="server" :size="14" />{{ t('node.total') }}</span>
      <span class="strip-value num">{{ totals.all }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="check" :size="14" />{{ t('node.online') }}</span>
      <span class="strip-value num" style="color: var(--ok)">{{ totals.online }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="alert" :size="14" />{{ t('node.offline') }}</span>
      <span class="strip-value num" :style="totals.offline ? 'color: var(--bad)' : ''">
        {{ totals.offline }}
      </span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="clock" :size="14" />{{ t('node.avgLatency') }}</span>
      <span class="strip-value num ltr">{{ totals.latency === null ? '—' : totals.latency + ' ms' }}</span>
    </div>
  </div>

  <div class="card">
    <div class="card-toolbar spread">
      <button class="btn primary" @click="openAdd">
        <Icon name="plus" :size="14" />
        <span v-if="!isMobile">{{ t('node.add') }}</span>
      </button>
      <button class="btn" @click="openIssue">
        <Icon name="key" :size="14" />
        <span>{{ t('node.issueToken') }}</span>
      </button>
    </div>

    <PageSpin v-if="showSkeleton" />
    <div v-else-if="loading" class="empty"></div>
    <ErrorState v-else-if="loadError" :error="loadError" @retry="load" />

    <!-- Their column order. Enabled is the second column, a switch beside
         the actions; the readings run left to right from status to heartbeat.
         The header row stays when there is nothing under it. -->
    <div v-else-if="isMobile" class="node-cards" @click="closeNodeMenu">
      <div v-if="!nodes.length" class="card-empty">
        <AntIcon name="ClusterOutlined" :size="28" style="opacity: 0.5" />
        <div>{{ t('common.nothingYet') }}</div>
      </div>
      <div v-for="n in nodes" :key="n.id" class="node-card">
        <div class="card-head" @click="toggleExpanded(n.id)">
          <AntIcon name="RightOutlined" class="card-expand" :class="{ 'is-expanded': expandedIds.has(n.id) }" />
          <i class="abadge-dot" :class="n.kind === 'local' || n.reachable ? 'green' : !n.enabled ? '' : 'red'"></i>
          <span class="node-name">{{ n.name }}</span>
          <span v-if="n.kind === 'local'" class="tag grey">{{ t('node.thisPanel') }}</span>
          <div class="card-actions" @click.stop>
            <input type="checkbox" class="acheck" :checked="n.enabled" :disabled="n.kind === 'local' || isPending(n.id)" :aria-label="n.name" @change="toggle(n, $event.target.checked)" />
            <button v-if="n.kind !== 'local'" type="button" class="row-action-trigger" :aria-label="t('action.more')" :aria-expanded="nodeMenu?.node?.id === n.id" @click="openNodeMenu(n, $event)"><AntIcon name="MoreOutlined" /></button>
          </div>
        </div>
        <div v-if="expandedIds.has(n.id)" class="card-history card-stats">
          <div class="stat-row"><span class="stat-label">{{ t('node.address') }}</span><span class="ltr">{{ n.address || '—' }}</span></div>
          <div class="stat-row"><span class="stat-label">{{ t('node.status') }}</span>
            <span v-if="n.kind === 'local'" class="tag green">{{ t('node.running') }}</span>
            <span v-else-if="!n.enabled" class="tag grey">{{ t('status.disabled') }}</span>
            <span v-else-if="n.reachable" class="tag green">{{ t('node.online') }}</span>
            <span v-else class="tag red" :title="n.lastError">{{ t('node.offline') }}</span>
          </div>
          <div class="stat-row"><span class="stat-label">CPU / RAM</span><span class="ltr">{{ n.cpuPercent ? n.cpuPercent.toFixed(0) + '%' : '—' }} / {{ n.memPercent ? n.memPercent.toFixed(0) + '%' : '—' }}</span></div>
          <div class="stat-row"><span class="stat-label">{{ t('node.version') }}</span><span class="ltr">{{ n.version || '—' }}</span></div>
          <div class="stat-row"><span class="stat-label">{{ t('node.uptime') }}</span><span class="ltr">{{ uptime(n.uptimeSec) }}</span></div>
          <div class="stat-row"><span class="stat-label">{{ t('client.traffic') }}</span><span class="ltr">{{ bytes(n.usedBytes || 0, store.locale) }}<template v-if="n.dataLimitBytes"> / {{ bytes(n.dataLimitBytes, store.locale) }}</template></span></div>
          <div class="stat-row"><span class="stat-label">{{ t('node.latency') }}</span><span class="tag num ltr" :class="latencyTone(n.latencyMs)">{{ n.latencyMs || 0 }} ms</span></div>
          <div class="stat-row"><span class="stat-label">{{ t('node.lastSeen') }}</span><span>{{ n.kind === 'local' ? t('node.justNow') : ago(n.lastSeenAt) }}</span></div>
        </div>
      </div>
      <Teleport to="body">
        <div v-if="nodeMenu" class="rowmenu" role="menu" :style="{ top: nodeMenu.y + 'px', left: nodeMenu.x + 'px' }">
          <button class="menu-item" role="menuitem" @click="nodeAction('probe')"><AntIcon name="ThunderboltOutlined" />{{ t('node.probe') }}</button>
          <button class="menu-item" role="menuitem" @click="nodeAction('update')"><AntIcon name="DownloadOutlined" />{{ t('update.askNode') }}</button>
          <button class="menu-item" role="menuitem" @click="nodeAction('edit')"><AntIcon name="EditOutlined" />{{ t('action.edit') }}</button>
          <button class="menu-item danger" role="menuitem" @click="nodeAction('delete')"><AntIcon name="DeleteOutlined" />{{ t('action.delete') }}</button>
        </div>
      </Teleport>
    </div>

    <div v-else class="table-wrap">
    <table>
      <thead>
        <tr>
          <th class="w-gact">{{ t('table.actions') }}</th>
          <th class="w-sm">{{ t('table.enabled') }}</th>
          <th>{{ t('node.name') }}</th>
          <th>{{ t('node.address') }}</th>
          <th class="w-md">{{ t('node.status') }}</th>
          <th class="w-sm">{{ t('node.cpu') }}</th>
          <th class="w-sm">{{ t('node.mem') }}</th>
          <th class="w-md">{{ t('node.version') }}</th>
          <th class="w-md">{{ t('node.uptime') }}</th>
          <th class="w-md">{{ t('node.transfer') }}</th>
          <th class="w-sm">{{ t('node.latency') }}</th>
          <th class="w-md">{{ t('node.lastSeen') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!nodes.length" class="empty-row">
          <td colspan="12">
            <div class="card-empty">
              <Icon name="hdd" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="n in nodes" :key="n.id">
          <td class="w-gact">
            <div class="actions">
              <button
                class="act"
                :title="t('node.probe')"
                :disabled="n.kind === 'local' || isPending(n.id)"
                @click="probe(n)"
              >
                <span v-if="isPending(n.id)" class="spin sm"></span>
                <Icon v-else name="refresh" :size="16" />
              </button>
              <!-- No binary travels from here. The node fetches the release
                   itself and checks the signature, so taking this panel does
                   not mean running code of your choosing on every node. -->
              <button
                class="act"
                :title="t('update.askNode')"
                :disabled="n.kind === 'local' || isPending(n.id)"
                @click="askNodeUpdate(n)"
              >
                <Icon name="download" :size="16" />
              </button>
              <button
                class="act"
                :title="t('action.edit')"
                :disabled="n.kind === 'local'"
                @click="openEdit(n)"
              >
                <Icon name="edit" :size="16" />
              </button>
              <button
                class="act danger"
                :title="t('action.delete')"
                :disabled="n.kind === 'local'"
                @click="remove(n)"
              >
                <Icon name="trash" :size="16" />
              </button>
            </div>
          </td>

          <td>
            <input
              type="checkbox"
              :checked="n.enabled"
              :disabled="n.kind === 'local' || isPending(n.id)"
              :aria-label="n.name"
              @change="toggle(n, $event.target.checked)"
            />
          </td>

          <td>
            <span class="nodename">{{ n.name }}</span>
            <!-- The panel's own row is marked, because everything else on it
                 behaves differently and an operator should not wonder why. -->
            <span v-if="n.kind === 'local'" class="tag grey">{{ t('node.thisPanel') }}</span>
            <!-- Shown only when it is not 1. An operator looking at a customer
                 whose usage climbed faster than their traffic should be able to
                 see the reason here rather than open every node in turn. -->
            <span v-if="n.usageCoefficient && n.usageCoefficient !== 1" class="tag num ltr"
                  :title="t('node.coefficientHint')">x{{ n.usageCoefficient }}</span>
            <!-- Said plainly, because an operator whose customers have quietly
                 moved off a server deserves to know it was this and not a
                 fault. -->
            <!-- Said on the row, because a node nobody is checking the identity
                 of is a decision somebody made once and forgot. -->
            <span v-if="n.kind !== 'local' && n.tlsMode === 'skip'" class="tag red"
                  :title="t('node.tlsSkipWarning')">{{ t('node.tlsUnchecked') }}</span>
            <span v-else-if="n.kind !== 'local' && n.tlsMode === 'mtls'" class="tag green"
                  :title="t('node.tlsMutualHint')">{{ t('node.tlsMutualShort') }}</span>
            <span v-else-if="n.kind !== 'local' && n.tlsMode === 'pin'" class="tag grey"
                  :title="t('node.tlsPinnedHint')">{{ t('node.tlsPinned') }}</span>
            <span v-if="n.overAllowance" class="tag red" :title="t('node.spentHint')">
              {{ t('node.spent') }}
            </span>
            <div v-if="n.note" class="sub muted small">{{ n.note }}</div>
          </td>

          <td class="muted small ltr">{{ n.address || '—' }}</td>

          <td>
            <span v-if="n.kind === 'local'" class="tag green"><i class="dot"></i>{{ t('node.running') }}</span>
            <span v-else-if="!n.enabled" class="tag grey">{{ t('status.disabled') }}</span>
            <span v-else-if="n.reachable" class="tag green"><i class="dot"></i>{{ t('node.online') }}</span>
            <!-- The reason, not just the colour: "refused" and "no route" send
                 an operator to entirely different places. -->
            <span v-else class="tag red" :title="n.lastError">{{ t('node.offline') }}</span>
          </td>

          <td class="num ltr">{{ n.cpuPercent ? n.cpuPercent.toFixed(0) + '%' : '—' }}</td>
          <td class="num ltr">{{ n.memPercent ? n.memPercent.toFixed(0) + '%' : '—' }}</td>
          <td class="muted small ltr">{{ n.version || '—' }}</td>
          <td class="muted small ltr">{{ uptime(n.uptimeSec) }}</td>
          <td class="muted small ltr">
            <template v-if="n.dataLimitBytes">
              {{ bytes(n.usedBytes || 0, store.locale) }} / {{ bytes(n.dataLimitBytes, store.locale) }}
            </template>
            <template v-else>{{ bytes(n.usedBytes || 0, store.locale) }}</template>
          </td>

          <td>
            <span v-if="n.kind === 'local'" class="muted">—</span>
            <span v-else class="tag num ltr" :class="latencyTone(n.latencyMs)">{{ n.latencyMs || 0 }} ms</span>
          </td>

          <td class="muted small">{{ n.kind === 'local' ? t('node.justNow') : ago(n.lastSeenAt) }}</td>
        </tr>
      </tbody>
    </table>
    </div>
  </div>

  <!-- Add / edit: the same 760px horizontal form the other dialogs use,
       labels at the end of a third of the row and the control beside them,
       in three groups -- the server, how it is trusted, what it costs. -->
  <div v-if="dialog" class="amodal-backdrop" @click.self="dialog = null">
    <div class="amodal" :class="dialog.kind === 'token' ? 'w520' : 'w760'" role="dialog" aria-modal="true" aria-labelledby="n-title">
      <div class="amodal-head">
        <h2 id="n-title" class="amodal-title">
          {{ dialog.kind === 'token' ? t('node.issueToken')
            : dialog.kind === 'add' ? t('node.add') : t('node.edit') }}
        </h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="dialog = null"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body nf-body">
        <form id="n-form" class="hform" @submit.prevent="dialog.kind === 'token' ? issueToken() : submit()">
          <template v-if="dialog.kind === 'token'">
            <div class="hrow">
              <label class="req" for="tk-name">{{ t('node.tokenName') }}</label>
              <div class="hctl">
                <label class="ainput block"><input id="tk-name" v-model="form.name" required autofocus maxlength="64" :placeholder="t('node.tokenNamePlaceholder')" /></label>
                <p class="hint">{{ t('node.tokenNameHint') }}</p>
              </div>
            </div>
          </template>

          <template v-else>
            <div class="nf-section">{{ t('node.sectionServer') }}</div>
            <div class="hrow">
              <label class="req" for="n-name">{{ t('node.name') }}</label>
              <div class="hctl"><label class="ainput block"><input id="n-name" v-model="form.name" required autofocus maxlength="64" /></label></div>
            </div>
            <div class="hrow">
              <label class="req" for="n-addr">{{ t('node.address') }}</label>
              <div class="hctl">
                <label class="ainput block"><input id="n-addr" v-model="form.address" required placeholder="https://vpn2.example.com:2096" class="ltr" /></label>
                <p class="hint">{{ t('node.addressHint') }}</p>
                <label class="acheckbox nf-check"><input v-model="form.allowPrivateAddress" type="checkbox" /><span>{{ t('node.allowPrivate') }}</span></label>
                <p v-if="form.allowPrivateAddress" class="hint">{{ t('node.allowPrivateHint') }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="n-note">{{ t('group.note') }}</label>
              <div class="hctl"><label class="ainput block"><input id="n-note" v-model="form.note" maxlength="256" /></label></div>
            </div>

            <div class="nf-section">{{ t('node.sectionTrust') }}</div>
            <div class="hrow">
              <label :class="{ req: dialog.kind === 'add' }" for="n-token">{{ t('node.token') }}</label>
              <div class="hctl">
                <label class="ainput block"><input id="n-token" v-model="form.token" type="password" class="ltr" autocomplete="off"
                       :required="dialog.kind === 'add'" :placeholder="dialog.kind === 'edit' ? t('node.tokenKeep') : 'wui_…'" /></label>
                <p class="hint">{{ t('node.tokenHint') }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="n-tls">{{ t('node.tlsMode') }}</label>
              <div class="hctl">
                <label class="aselect"><select id="n-tls" v-model="form.tlsMode">
                  <option value="verify">{{ t('node.tlsVerify') }}</option>
                  <option value="pin">{{ t('node.tlsPin') }}</option>
                  <option value="mtls">{{ t('node.tlsMutual') }}</option>
                  <option value="skip">{{ t('node.tlsSkip') }}</option>
                </select></label>
                <p v-if="form.tlsMode === 'verify'" class="hint">{{ t('node.tlsModeHint') }}</p>
                <p v-else-if="form.tlsMode === 'pin'" class="hint">{{ t('node.tlsPinnedHint') }}</p>
                <p v-else-if="form.tlsMode === 'mtls'" class="hint">{{ t('node.tlsMutualHint') }}</p>
                <p v-else class="hint warn-text">{{ t('node.tlsSkipWarning') }}</p>
              </div>
            </div>
            <div v-if="form.tlsMode === 'pin'" class="hrow">
              <label for="n-pin">{{ t('node.pin') }}</label>
              <div class="hctl">
                <div class="nf-inline">
                  <label class="ainput block"><input id="n-pin" v-model="form.tlsPin" class="ltr mono" placeholder="sha256/…" /></label>
                  <button type="button" class="abtn" :disabled="pinBusy || !form.address" @click="fetchPin">
                    <span v-if="pinBusy" class="spin sm"></span>
                    <template v-else><AntIcon name="DownloadOutlined" /><span>{{ t('node.fetchPin') }}</span></template>
                  </button>
                </div>
                <p class="hint">{{ t('node.fetchPinHint') }}</p>
              </div>
            </div>
            <div v-if="form.tlsMode === 'mtls'" class="hrow">
              <label>{{ t('node.authority') }}</label>
              <div class="hctl">
                <textarea v-model="authority" rows="4" readonly class="atextarea ltr mono" :placeholder="t('node.authorityLoading')"></textarea>
                <div class="nf-inline nf-actions">
                  <button type="button" class="abtn" :disabled="authorityBusy" @click="loadAuthority">
                    <span v-if="authorityBusy" class="spin sm"></span>
                    <template v-else><AntIcon name="SafetyCertificateOutlined" /><span>{{ t('node.authorityLoad') }}</span></template>
                  </button>
                  <button type="button" class="abtn" :disabled="!authority" @click="copyAuthority"><AntIcon name="CopyOutlined" /><span>{{ t('action.copy') }}</span></button>
                </div>
                <p class="hint">{{ t('node.authorityHint') }}</p>
              </div>
            </div>

            <div class="nf-section">{{ t('node.sectionBilling') }}</div>
            <div class="hrow">
              <label for="n-coef">{{ t('node.coefficient') }}</label>
              <div class="hctl">
                <label class="ainput number" style="width: 140px"><input id="n-coef" v-model="form.usageCoefficient" type="number" step="0.1" min="0.1" max="100" class="ltr" /><span class="ainput-suffix">×</span></label>
                <p class="hint">{{ t('node.coefficientHint') }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="n-limit">{{ t('node.dataLimit') }}</label>
              <div class="hctl">
                <div class="nf-inline">
                  <label class="ainput number" style="width: 140px"><input id="n-limit" v-model="form.dataLimitGB" type="number" min="0" step="1" placeholder="∞" class="ltr" /><span class="ainput-suffix">GB</span></label>
                  <span class="nf-sep">{{ t('node.resetDay') }}</span>
                  <label class="ainput number" style="width: 110px"><input id="n-resetday" v-model="form.resetDay" type="number" min="0" max="28" step="1" placeholder="0" class="ltr" /></label>
                </div>
                <p class="hint">{{ t('node.dataLimitHint') }}</p>
              </div>
            </div>
          </template>
        </form>
      </div>

      <div class="amodal-foot">
        <button type="button" class="abtn" @click="dialog = null">{{ t('action.cancel') }}</button>
        <button type="submit" form="n-form" class="abtn primary" :disabled="busy">
          <span v-if="busy" class="spin sm"></span>
          <template v-else>{{ dialog.kind === 'token' ? t('node.issueToken') : t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>

  <!-- A freshly issued token, shown once. -->
  <div v-if="issued" class="amodal-backdrop" @click.self="issued = null">
    <div class="amodal w520" role="dialog" aria-modal="true">
      <div class="amodal-head"><h2 class="amodal-title">{{ t('node.tokenIssued') }}</h2></div>
      <div class="amodal-body">
        <div class="nf-notice"><AntIcon name="ExclamationCircleOutlined" /><span>{{ t('node.tokenOnce') }}</span></div>
        <pre class="api-code ltr nf-token"><code>{{ issued.token }}</code></pre>
      </div>
      <div class="amodal-foot">
        <button class="abtn" @click="issued = null">{{ t('common.close') }}</button>
        <button class="abtn primary" @click="copyToken"><AntIcon name="CopyOutlined" /><span>{{ t('api.copy') }}</span></button>
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
</template>

<style scoped>
.nodename {
  font-weight: 600;
  margin-inline-end: 6px;
}
.nf-body { max-height: 72vh; overflow-y: auto; overflow-x: hidden; }
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.hint.warn-text { color: var(--warn); }
/* A group heading: small caps over a rule, the way a settings page breaks
   its rows up, so a nine-row form reads as three short ones. */
.nf-section {
  margin: 4px 0 16px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--line-soft);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.4px;
  text-transform: uppercase;
  color: var(--muted);
}
.nf-section:not(:first-child) { margin-top: 8px; }
.nf-check { display: inline-flex; margin-top: 8px; }
.nf-inline { display: flex; align-items: center; gap: 8px; }
.nf-inline .ainput.block { flex: 1; }
.nf-actions { margin-top: 8px; }
.nf-sep { color: var(--muted); font-size: 13px; white-space: nowrap; }
.ainput-suffix { color: var(--faint); font-size: 12px; padding-inline-end: 10px; }
.atextarea {
  width: 100%;
  min-height: 96px;
  padding: 6px 11px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface);
  color: var(--ink);
  font-size: 12px;
  line-height: 1.5;
  resize: vertical;
}
.atextarea:focus { outline: none; border-color: var(--accent-hover); box-shadow: 0 0 0 2px var(--accent-ring); }
.nf-notice {
  display: flex; align-items: flex-start; gap: 10px;
  padding: 10px 12px; margin-bottom: 12px;
  border: 1px solid var(--warn-line, var(--line)); border-radius: 6px;
  background: var(--warn-bg, var(--surface-2)); color: var(--ink); font-size: 13px; line-height: 1.5;
}
.nf-notice .anticon { color: var(--warn); margin-top: 2px; }
.nf-token { margin: 0; user-select: all; }
@media (max-width: 640px) {
  .hrow { grid-template-columns: 1fr; }
  .hrow > label { text-align: start; padding-top: 0; }
}
</style>
