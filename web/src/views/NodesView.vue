<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { mergeRows, useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const nodes = ref([])
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
  form.value = { name: '', address: '', token: '', note: '', usageCoefficient: 1 }
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
  }
  dialog.value = { kind: 'edit', node: n }
}

async function submit() {
  busy.value = true
  try {
    const body = { ...form.value, usageCoefficient: Number(form.value.usageCoefficient) || 1 }
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
  <div class="page-head">
    <div>
      <h1>{{ t('nav.nodes') }}</h1>
      <p class="lede">{{ t('node.lede') }}</p>
    </div>
    <div class="page-actions">
      <button class="btn ghost" @click="openIssue">
        <Icon name="key" :size="15" />
        <span>{{ t('node.issueToken') }}</span>
      </button>
      <button class="btn" @click="openAdd">
        <Icon name="plus" :size="15" />
        <span>{{ t('node.add') }}</span>
      </button>
    </div>
  </div>

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

  <table v-if="showSkeleton" class="skeleton card" aria-hidden="true">
    <tbody>
      <tr v-for="n in 3" :key="n">
        <td v-for="c in 8" :key="c"><span class="sk"></span></td>
      </tr>
    </tbody>
  </table>
  <p v-else-if="loading" class="muted"></p>
  <ErrorState v-else-if="loadError" :error="loadError" @retry="load" />

  <div v-else class="card table-wrap">
    <table>
      <thead>
        <tr>
          <th class="w-gact">{{ t('table.actions') }}</th>
          <th>{{ t('node.name') }}</th>
          <th>{{ t('node.address') }}</th>
          <th class="w-md">{{ t('node.status') }}</th>
          <th class="w-sm">{{ t('node.latency') }}</th>
          <th class="w-sm">{{ t('node.cpu') }}</th>
          <th class="w-sm">{{ t('node.mem') }}</th>
          <th class="w-md">{{ t('node.uptime') }}</th>
          <th class="w-md">{{ t('node.lastSeen') }}</th>
          <th class="w-md">{{ t('node.version') }}</th>
          <th class="w-sm">{{ t('table.enabled') }}</th>
        </tr>
      </thead>
      <tbody>
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
            <span class="nodename">{{ n.name }}</span>
            <!-- The panel's own row is marked, because everything else on it
                 behaves differently and an operator should not wonder why. -->
            <span v-if="n.kind === 'local'" class="tag grey">{{ t('node.thisPanel') }}</span>
            <!-- Shown only when it is not 1. An operator looking at a customer
                 whose usage climbed faster than their traffic should be able to
                 see the reason here rather than open every node in turn. -->
            <span v-if="n.usageCoefficient && n.usageCoefficient !== 1" class="tag num ltr"
                  :title="t('node.coefficientHint')">x{{ n.usageCoefficient }}</span>
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

          <td>
            <span v-if="n.kind === 'local'" class="muted">—</span>
            <span v-else class="tag num ltr" :class="latencyTone(n.latencyMs)">{{ n.latencyMs || 0 }} ms</span>
          </td>

          <td class="num ltr">{{ n.cpuPercent ? n.cpuPercent.toFixed(0) + '%' : '—' }}</td>
          <td class="num ltr">{{ n.memPercent ? n.memPercent.toFixed(0) + '%' : '—' }}</td>
          <td class="muted small ltr">{{ uptime(n.uptimeSec) }}</td>
          <td class="muted small">{{ n.kind === 'local' ? t('node.justNow') : ago(n.lastSeenAt) }}</td>
          <td class="muted small ltr">{{ n.version || '—' }}</td>

          <td>
            <input
              type="checkbox"
              :checked="n.enabled"
              :disabled="n.kind === 'local' || isPending(n.id)"
              :aria-label="n.name"
              @change="toggle(n, $event.target.checked)"
            />
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="!remote.length" class="empty empty-cta">
      <p>{{ t('node.none') }}</p>
      <p class="small muted">{{ t('node.noneHint') }}</p>
      <button class="btn" @click="openAdd">
        <Icon name="plus" :size="15" />
        <span>{{ t('node.add') }}</span>
      </button>
    </div>
  </div>

  <!-- Add / edit -->
  <div v-if="dialog" class="modal-backdrop" @click.self="dialog = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="n-title">
      <div class="card-head">
        <h2 id="n-title">
          {{ dialog.kind === 'token' ? t('node.issueToken')
            : dialog.kind === 'add' ? t('node.add') : t('node.edit') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="dialog = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form
        id="n-form"
        class="card-body"
        @submit.prevent="dialog.kind === 'token' ? issueToken() : submit()"
      >
        <template v-if="dialog.kind === 'token'">
          <div class="field">
            <label for="tk-name">{{ t('node.tokenName') }}</label>
            <input id="tk-name" v-model="form.name" required autofocus maxlength="64"
                   :placeholder="t('node.tokenNamePlaceholder')" />
            <span class="hint">{{ t('node.tokenNameHint') }}</span>
          </div>
        </template>

        <template v-else>
        <div class="field">
          <label for="n-name">{{ t('node.name') }}</label>
          <input id="n-name" v-model="form.name" required autofocus maxlength="64" />
        </div>

        <div class="field">
          <label for="n-addr">{{ t('node.address') }}</label>
          <input id="n-addr" v-model="form.address" required placeholder="https://vpn2.example.com:2096" class="ltr" />
          <span class="hint">{{ t('node.addressHint') }}</span>
        </div>

        <div class="field">
          <label for="n-token">{{ t('node.token') }}</label>
          <input id="n-token" v-model="form.token" type="password" class="ltr"
                 :placeholder="dialog.kind === 'edit' ? t('node.tokenKeep') : 'wui_…'" />
          <span class="hint">{{ t('node.tokenHint') }}</span>
        </div>

        <div class="field">
          <label for="n-note">{{ t('group.note') }}</label>
          <input id="n-note" v-model="form.note" maxlength="256" />
        </div>

        <!-- What a gigabyte through this server costs a customer. The nearest a
             reseller gets to charging more for an expensive server without
             selling a second plan. -->
        <div class="field">
          <label for="n-coef">{{ t('node.coefficient') }}</label>
          <input id="n-coef" v-model="form.usageCoefficient" type="number"
                 step="0.1" min="0.1" max="100" class="ltr" />
          <span class="hint">{{ t('node.coefficientHint') }}</span>
        </div>
        </template>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="dialog = null">{{ t('action.cancel') }}</button>
        <button type="submit" form="n-form" class="btn primary" :disabled="busy">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>

  <!-- A freshly issued token, shown once. -->
  <div v-if="issued" class="modal-backdrop" @click.self="issued = null">
    <div class="modal narrow" role="dialog" aria-modal="true">
      <div class="card-head">
        <h2>{{ t('node.tokenIssued') }}</h2>
      </div>
      <div class="card-body">
        <p class="muted small">{{ t('node.tokenOnce') }}</p>
        <pre class="api-code ltr"><code>{{ issued.token }}</code></pre>
      </div>
      <div class="modal-foot">
        <button class="btn ghost" @click="issued = null">{{ t('common.close') }}</button>
        <button class="btn primary" @click="copyToken">
          <Icon name="copy" :size="15" />
          <span>{{ t('api.copy') }}</span>
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
</template>

<style scoped>
.nodename {
  font-weight: 600;
  margin-inline-end: 6px;
}
</style>
