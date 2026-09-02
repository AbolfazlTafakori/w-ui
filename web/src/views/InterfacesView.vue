<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import InterfaceForm from '../components/InterfaceForm.vue'
import InterfaceDetail from '../components/InterfaceDetail.vue'
import Toggle from '../components/Toggle.vue'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const router = useRouter()

const interfaces = ref([])
const busy = ref(false)
const loading = ref(true)
const formFor = ref(null) // null = closed, {} = create, { iface } = edit
const detailFor = ref(null)
const selected = ref(new Set())

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load() {
  loading.value = true
  try {
    interfaces.value = await api.interfaces()
    const visible = new Set(interfaces.value.map((i) => i.id))
    selected.value = new Set([...selected.value].filter((id) => visible.has(id)))
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(load)

// The strip 3x-ui puts above its inbound table: what the whole set is carrying.
const totals = computed(() => {
  const list = interfaces.value
  return {
    used: list.reduce((a, i) => a + (i.usedBytes || 0), 0),
    clients: list.reduce((a, i) => a + (i.clients || 0), 0),
    devices: list.reduce((a, i) => a + (i.devices || 0), 0),
    allocated: list.reduce((a, i) => a + (i.allocated || 0), 0),
    capacity: list.reduce((a, i) => a + (i.capacity || 0), 0),
    count: list.length,
  }
})

const allSelected = computed(
  () => !!interfaces.value.length && selected.value.size === interfaces.value.length,
)

function toggleAll(checked) {
  selected.value = checked ? new Set(interfaces.value.map((i) => i.id)) : new Set()
}
function toggleOne(id, checked) {
  const next = new Set(selected.value)
  checked ? next.add(id) : next.delete(id)
  selected.value = next
}

function poolPercent(i) {
  return i.capacity ? (i.allocated / i.capacity) * 100 : 0
}

async function guard(fn, successKey) {
  try {
    await fn()
    if (successKey) notify(t(successKey), 'success')
    await load()
  } catch (err) {
    notify(err.message, 'error')
  }
}

const setEnabled = (iface, on) =>
  guard(() => api.updateInterface(iface.id, { enabled: on }), 'interface.updated')

const ask = ref(null)

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

// Deleting a tunnel is the most expensive click in the panel: it takes every
// customer on it and every key they hold, and there is no undo. The dialog says
// the count, and the name has to be typed.
const removeOne = (iface) => {
  ask.value = {
    title: t('interface.confirmDeleteTitle'),
    body: t('interface.confirmDeleteBody'),
    subject: iface.name,
    consequences: [
      tn('interface.consequenceClients', iface.clients ?? 0),
      tn('interface.consequenceDevices', iface.devices ?? 0),
      t('interface.consequenceKeys'),
    ],
    confirmLabel: t('action.delete'),
    requireText: (iface.clients ?? 0) > 0 ? iface.name : '',
    run: () => guard(() => api.deleteInterface(iface.id), 'interface.deleted'),
  }
}

const bulkDelete = () => {
  const ids = [...selected.value]
  if (!ids.length) return
  const chosen = interfaces.value.filter((i) => selected.value.has(i.id))
  const clients = chosen.reduce((a, i) => a + (i.clients ?? 0), 0)

  ask.value = {
    title: t('interface.confirmDeleteManyTitle'),
    body: t('interface.confirmDeleteBody'),
    subject: tn('interface.nTunnels', ids.length),
    consequences: [
      tn('interface.consequenceClients', clients),
      t('interface.consequenceKeys'),
    ],
    confirmLabel: t('action.delete'),
    requireText: clients > 0 ? String(clients) : '',
    run: () =>
      guard(async () => {
        for (const id of ids) await api.deleteInterface(id)
        selected.value = new Set()
      }, 'interface.deleted'),
  }
}

async function submitForm(input) {
  try {
    if (formFor.value?.iface) {
      await api.updateInterface(formFor.value.iface.id, {
        enabled: input.enabled,
        endpointHost: input.endpointHost,
        mtu: Number(input.mtu),
        dns: input.dns,
        natInterface: input.natInterface,
      })
      notify(t('interface.updated'), 'success')
    } else {
      await api.createInterface({
        ...input,
        listenPort: Number(input.listenPort),
        mtu: Number(input.mtu),
      })
      notify(t('interface.created'), 'success')
    }
    formFor.value = null
    await load()
  } catch (err) {
    notify(err.message, 'error')
    throw err
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>{{ t('nav.interfaces') }}</h1>
      <p>{{ t('interface.subtitle') }}</p>
    </div>
  </div>

  <div class="strip card">
    <div class="strip-item">
      <span class="strip-label"><Icon name="swap" :size="14" />{{ t('interface.totalTraffic') }}</span>
      <span class="strip-value num ltr">{{ bytes(totals.used, store.locale) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="users" :size="14" />{{ t('nav.clients') }}</span>
      <span class="strip-value num ltr">{{ nf(totals.clients) }} · {{ nf(totals.devices) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="server" :size="14" />{{ t('interface.total') }}</span>
      <span class="strip-value num">{{ nf(totals.count) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="globe" :size="14" />{{ t('interface.capacity') }}</span>
      <span class="strip-value num ltr">{{ nf(totals.allocated) }} / {{ nf(totals.capacity) }}</span>
    </div>
  </div>

  <div class="actionbar">
    <button class="btn primary" @click="formFor = {}">
      <Icon name="plus" :size="15" />{{ t('interface.create') }}
    </button>
    <template v-if="selected.size">
      <span class="selcount small">{{ t('action.selected') }}: {{ nf(selected.size) }}</span>
      <button class="btn sm danger" @click="bulkDelete">{{ t('action.delete') }}</button>
    </template>
  </div>

  <div class="card">
    <div v-if="loading" class="empty"><span class="spin"></span></div>

    <div v-else-if="!interfaces.length" class="empty">
      <p>{{ t('interface.noneYet') }}</p>
      <p class="small">{{ t('interface.noneYetHint') }}</p>
    </div>

    <div v-else class="table-wrap">
      <table>
        <thead>
          <tr>
            <th class="tick">
              <input
                type="checkbox"
                :checked="allSelected"
                :aria-label="t('action.selectAll')"
                @change="toggleAll($event.target.checked)"
              />
            </th>
            <!-- Name first, as on every other table here. The row's own id is
                 a database detail and reads as noise in front of it. -->
            <th>{{ t('interface.name') }}</th>
            <th>{{ t('interface.port') }}</th>
            <th>{{ t('client.protocol') }}</th>
            <th>{{ t('nav.clients') }}</th>
            <th>{{ t('client.traffic') }}</th>
            <th style="min-width: 190px">{{ t('interface.capacity') }}</th>
            <th>{{ t('table.enabled') }}</th>
            <th class="right">{{ t('table.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="i in interfaces" :key="i.id" :class="{ picked: selected.has(i.id) }">
            <td class="tick">
              <input
                type="checkbox"
                :checked="selected.has(i.id)"
                :aria-label="i.name"
                @change="toggleOne(i.id, $event.target.checked)"
              />
            </td>

            <td class="mono name" :title="`#${i.id}`">{{ i.name }}</td>
            <td class="mono num">{{ i.listenPort }}</td>

            <td>
              <div class="tags">
                <span class="tag proto">{{ i.protocol }}</span>
                <span v-if="i.mode === 'amnezia'" class="tag active">
                  <Icon name="shield" :size="11" />AmneziaWG
                </span>
              </div>
            </td>

            <td>
              <button class="linkish" @click="router.push('/clients')">
                <Icon name="users" :size="14" />
                <span class="num ltr">{{ nf(i.clients) }} · {{ nf(i.devices) }}</span>
              </button>
            </td>

            <td>
              <span class="tag disabled num ltr">
                {{ bytes(i.usedBytes, store.locale) }} / ∞
              </span>
            </td>

            <td>
              <div class="meter" style="margin-bottom: 6px">
                <span
                  :class="poolPercent(i) > 90 ? 'bad' : poolPercent(i) > 75 ? 'warn' : ''"
                  :style="{ width: Math.max(poolPercent(i), 1) + '%' }"
                ></span>
              </div>
              <span class="muted small num ltr">
                {{ nf(i.allocated) }} / {{ nf(i.capacity) }}
              </span>
            </td>

            <td>
              <Toggle
                :model-value="i.enabled"
                :label="i.name"
                @update:model-value="(v) => setEnabled(i, v)"
              />
            </td>

            <td class="right">
              <div class="actions">
                <button class="act" :title="t('action.edit')" @click="formFor = { iface: i }">
                  <Icon name="edit" :size="16" />
                </button>
                <button class="act" :title="t('action.details')" @click="detailFor = i">
                  <Icon name="info" :size="16" />
                </button>
                <button class="act danger" :title="t('action.delete')" @click="removeOne(i)">
                  <Icon name="trash" :size="16" />
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <InterfaceForm
    v-if="formFor"
    :iface="formFor.iface"
    @close="formFor = null"
    @submit="submitForm"
  />

  <InterfaceDetail v-if="detailFor" :iface="detailFor" @close="detailFor = null" />

  <ConfirmDialog
    :open="!!ask"
    :title="ask?.title || ''"
    :body="ask?.body || ''"
    :subject="ask?.subject || ''"
    :consequences="ask?.consequences || []"
    :confirm-label="ask?.confirmLabel || ''"
    :require-text="ask?.requireText || ''"
    :busy="busy"
    @confirm="runConfirmed"
    @cancel="ask = null"
  />
</template>

<style scoped>
.strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1px;
  background: var(--line-soft);
  margin-bottom: 16px;
  overflow: hidden;
}
.strip-item {
  background: var(--surface);
  padding: 14px 18px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.strip-label {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: var(--t-xs);
  color: var(--muted);
}
.strip-value {
  font-size: var(--t-lg);
  font-weight: 600;
  line-height: 1.15;
}

.actionbar {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.selcount {
  color: var(--muted);
  margin-inline-start: 8px;
}

th.tick,
td.tick {
  width: 42px;
  padding-inline-end: 0;
}
input[type='checkbox'] {
  width: 16px;
  height: 16px;
  min-height: 0;
  padding: 0;
  accent-color: var(--accent);
  cursor: pointer;
}

.actions {
  display: flex;
  gap: 2px;
}
.act {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
}
.act:hover {
  background: var(--surface-3);
  color: var(--ink);
}
.act.danger:hover {
  background: var(--bad-soft);
  color: var(--bad);
}

tr.picked {
  background: var(--accent-soft);
}
.name {
  color: var(--ink);
  font-weight: 600;
}
.tags {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}
.linkish {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: none;
  background: transparent;
  color: var(--ink-2);
  font: inherit;
  font-size: var(--t-sm);
  cursor: pointer;
  padding: 0;
}
.linkish:hover {
  color: var(--accent-hover);
}
</style>
