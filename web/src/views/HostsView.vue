<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import HostForm from '../components/HostForm.vue'

// The addresses customers are handed. An interface listens once; the name a
// customer dials may be several.
const hosts = ref([])
const interfaces = ref([])
const loading = ref(true)
const loadError = ref('')
const formFor = ref(null)
const ask = ref(null)
const busy = ref(false)

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

// Grouped by interface, because a host means nothing without knowing which
// tunnel it fronts.
const byInterface = computed(() => {
  const map = new Map()
  for (const i of interfaces.value) map.set(i.id, { iface: i, rows: [] })
  for (const h of hosts.value) {
    if (!map.has(h.interfaceId)) map.set(h.interfaceId, { iface: null, rows: [] })
    map.get(h.interfaceId).rows.push(h)
  }
  return [...map.values()].filter((g) => g.iface)
})

const reachable = computed(() => hosts.value.filter((h) => h.reachable).length)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const [h, i] = await Promise.all([
      api.get('/api/hosts', { background: quiet }),
      api.get('/api/interfaces', { background: quiet }),
    ])
    hosts.value = quiet ? mergeRows(hosts.value, h, pending.value) : h
    interfaces.value = Array.isArray(i) ? i : i?.interfaces || []
    loadError.value = ''
  } catch (err) {
    loadError.value = err.message
  } finally {
    loading.value = false
  }
}

const showSkeleton = useDelayed(computed(() => loading.value && !hosts.value.length))

onMounted(load)

// Reachability is filled in by the prober rather than by this page, so the
// status column is only ever as current as the last read of it.
useLive(load, { every: 15_000, busy: () => !!formFor.value || !!ask.value })

// Reordering the addresses of one tunnel.
//
// The whole order for that tunnel is sent, because a move is only meaningful
// against the list as it stands and two half-moves race into a third order.
const reordering = ref(false)

function siblings(h) {
  return (hosts.value || [])
    .filter((x) => x.interfaceId === h.interfaceId)
    .slice()
    .sort((a, b) => (a.priority || 0) - (b.priority || 0))
}

const isFirst = (h) => siblings(h)[0]?.id === h.id
const isLast = (h) => siblings(h).slice(-1)[0]?.id === h.id

async function move(h, delta) {
  const list = siblings(h)
  const at = list.findIndex((x) => x.id === h.id)
  const to = at + delta
  if (at < 0 || to < 0 || to >= list.length) return

  const order = list.map((x) => x.id)
  order.splice(to, 0, order.splice(at, 1)[0])

  reordering.value = true
  try {
    await api.post('/api/hosts/reorder', { ids: order })
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    reordering.value = false
  }
}

async function check(h) {
  hold(h.id)
  try {
    const res = await api.post(`/api/hosts/${h.id}/check`)
    h.reachable = res.ok
    h.lastError = res.ok ? '' : res.error
    h.lastCheckAt = new Date().toISOString()
    notify(res.ok ? `${h.name}: ${res.latencyMs} ms` : `${h.name}: ${res.error}`,
      res.ok ? 'success' : 'error')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    release(h.id)
  }
}

async function setEnabled(h, on) {
  const was = h.enabled
  if (was === on) return
  h.enabled = on
  hold(h.id)
  try {
    const updated = await api.patch(`/api/hosts/${h.id}`, {
      name: h.name,
      address: h.address,
      port: h.port,
      enabled: on,
    })
    Object.assign(h, updated)
  } catch (err) {
    h.enabled = was
    notify(err.message, 'error')
  } finally {
    release(h.id)
    load(true)
  }
}

function remove(h) {
  ask.value = {
    title: t('host.removeTitle'),
    subject: h.name,
    body: t('host.removeBody'),
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.delete(`/api/hosts/${h.id}`)
      notify(t('host.removed'), 'success')
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
</script>

<template>
  <section class="view">
    <header class="page-head">
      <div>
        <h1>{{ t('nav.hosts') }}</h1>
        <p class="muted">{{ t('host.lede') }}</p>
      </div>
    </header>

    <div class="statbar">
      <div class="stat">
        <span class="stat-label">{{ t('host.stat.total') }}</span>
        <span class="stat-value">{{ hosts.length }}</span>
      </div>
      <div class="stat">
        <span class="stat-label">{{ t('host.stat.reachable') }}</span>
        <span class="stat-value">{{ reachable }}</span>
      </div>
      <div class="stat">
        <span class="stat-label">{{ t('host.stat.interfaces') }}</span>
        <span class="stat-value">{{ interfaces.length }}</span>
      </div>
    </div>

    <div class="toolbar">
      <button class="btn primary" :disabled="!interfaces.length" @click="formFor = {}">
        <Icon name="plus" :size="16" /> {{ t('host.add') }}
      </button>
    </div>

    <div v-if="loadError" class="empty empty-cta">
      <Icon name="alert" :size="28" />
      <p>{{ loadError }}</p>
      <button class="btn" @click="load()">{{ t('action.retry') }}</button>
    </div>

    <table v-else-if="showSkeleton" class="skeleton" aria-hidden="true">
      <tbody>
        <tr v-for="n in 4" :key="n">
          <td v-for="c in 7" :key="c"><span class="sk"></span></td>
        </tr>
      </tbody>
    </table>
    <div v-else-if="loading" class="empty"></div>

    <!-- Nowhere to put a host yet. Pointing at the page that fixes it rather
         than stating the problem and stopping. -->
    <div v-else-if="!interfaces.length" class="empty empty-cta">
      <Icon name="globe" :size="28" />
      <p>{{ t('host.noInterfaces') }}</p>
      <RouterLink class="btn primary" to="/interfaces">{{ t('nav.interfaces') }}</RouterLink>
    </div>

    <div v-else-if="!hosts.length" class="empty empty-cta">
      <Icon name="globe" :size="28" />
      <p>{{ t('host.emptyHint') }}</p>
      <button class="btn primary" @click="formFor = {}">{{ t('host.add') }}</button>
    </div>

    <template v-else>
      <div v-for="g in byInterface" :key="g.iface.id" class="card group-card">
        <div class="card-head">
          <h2>
            {{ g.iface.name }}
            <span class="tag geekblue">{{ g.iface.protocol }}</span>
          </h2>
          <span class="muted small">{{ t('host.listensOn') }} {{ g.iface.listenPort }}</span>
        </div>

        <div v-if="!g.rows.length" class="empty small-empty">
          <p class="muted">{{ t('host.usingDefault') }} — {{ g.iface.endpointHost }}</p>
        </div>

        <table v-else class="table">
          <thead>
            <tr>
              <th class="w-gact">{{ t('table.actions') }}</th>
              <th>{{ t('table.enabled') }}</th>
              <th>{{ t('host.name') }}</th>
              <th>{{ t('host.address') }}</th>
              <th class="num">{{ t('host.port') }}</th>
              <th class="num">{{ t('host.priority') }}</th>
              <th>{{ t('host.status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="h in g.rows" :key="h.id">
              <td class="w-gact">
                <div class="actions">
                  <button
                    class="act"
                    :title="t('host.check')"
                    :disabled="isPending(h.id)"
                    @click="check(h)"
                  >
                    <span v-if="isPending(h.id)" class="spin sm"></span>
                    <Icon v-else name="zap" :size="16" />
                  </button>
                  <!-- Which address a customer is handed first. Moving one is
                       sending the whole order, not a nudge: two nudges racing
                       each other land in an order neither operator asked for. -->
                  <button class="act" :title="t('host.moveUp')"
                          :disabled="reordering || isFirst(h)" @click="move(h, -1)">
                    <Icon name="chevronDown" :size="16" class="flip" />
                  </button>
                  <button class="act" :title="t('host.moveDown')"
                          :disabled="reordering || isLast(h)" @click="move(h, 1)">
                    <Icon name="chevronDown" :size="16" />
                  </button>
                  <button class="act" :title="t('action.edit')" @click="formFor = { host: h }">
                    <Icon name="edit" :size="16" />
                  </button>
                  <button class="act danger" :title="t('action.delete')" @click="remove(h)">
                    <Icon name="trash" :size="16" />
                  </button>
                </div>
              </td>
              <td>
                <input
                  type="checkbox"
                  :checked="h.enabled"
                  :disabled="isPending(h.id)"
                  :aria-label="h.name"
                  @change="setEnabled(h, $event.target.checked)"
                />
              </td>
              <td>
                <strong>{{ h.name }}</strong>
                <div v-if="h.note" class="muted small">{{ h.note }}</div>
              </td>
              <td class="ltr">{{ h.address }}</td>
              <td class="num ltr">{{ h.effectivePort || h.port || '—' }}</td>
              <td class="num ltr">{{ h.priority }}</td>
              <td>
                <span v-if="h.lastError" class="tag red" :title="h.lastError">
                  {{ t('host.unreachable') }}
                </span>
                <span v-else-if="h.reachable" class="tag green">{{ t('host.ok') }}</span>
                <span v-else class="muted">—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <HostForm
      v-if="formFor"
      :host="formFor.host"
      :interfaces="interfaces"
      @saved="((formFor = null), load())"
      @cancel="formFor = null"
    />

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :subject="ask?.subject || ''"
      :confirm-label="ask?.confirmLabel || ''"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </section>
</template>
