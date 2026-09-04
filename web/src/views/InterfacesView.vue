<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, apiURL, getToken } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import ErrorState from '../components/ErrorState.vue'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import InterfaceForm from '../components/InterfaceForm.vue'
import InterfaceDetail from '../components/InterfaceDetail.vue'
import Toggle from '../components/Toggle.vue'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const router = useRouter()

const interfaces = ref([])
// The servers a tunnel can be put on. An install that never added one has
// only its own, and the form then says so instead of offering a choice of one.
const nodeList = ref([])
const busy = ref(false)
// A failed load and an empty list are not the same thing, and until now this
// page told an operator whose connection had dropped that they had no
// interfaces -- and offered to create one. On a page whose rows are live
// customer tunnels, that is an invitation to build a duplicate.
const loadError = ref(null)
const formRef = ref(null)
const loading = ref(true)
const formFor = ref(null) // null = closed, {} = create, { iface } = edit
const detailFor = ref(null)
const selected = ref(new Set())

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const fresh = await api.interfaces({ background: quiet })
    loadError.value = null
    interfaces.value = quiet
      ? mergeRows(interfaces.value, fresh, pending.value)
      : fresh
    const visible = new Set(interfaces.value.map((i) => i.id))
    selected.value = new Set([...selected.value].filter((id) => visible.has(id)))
  } catch (err) {
    // A poll that fails leaves the rows that are already on screen alone; a
    // first load that fails has nothing to leave, so it says so.
    if (!quiet) {
      if (!interfaces.value.length) loadError.value = err
      else notify(err.message, 'error')
    }
  } finally {
    loading.value = false
  }
}

onMounted(load)
onMounted(async () => {
  try {
    nodeList.value = await api.get('/api/nodes', { background: true })
  } catch {
    // Not worth a message: the form falls back to this server, which is
    // where every tunnel goes on a panel with no nodes anyway.
    nodeList.value = []
  }
})

// What these rows carry -- the traffic, the speed, how much of the address pool
// is spoken for -- changes constantly. An interface that has just been created
// also takes a moment to come up, and this is what shows it happening.
const showSkeleton = useDelayed(computed(() => loading.value && !interfaces.value.length))

useLive(load, {
  every: 5000,
  busy: () => !!formFor.value || !!detailFor.value || !!ask.value,
})

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

// Rows that are mid-request. Per-row rather than one flag for the page: taking
// one tunnel down should not freeze the controls on the other five.
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

// The switch moves on click and the knob spins until the server agrees.
//
// Bringing an interface up is slower than a client toggle — a device is
// created, addresses assigned, a driver opened — so the old behaviour, where
// nothing moved until a full reload of the list came back, read as a switch
// that did not work and invited a second click.
async function setEnabled(iface, on) {
  const was = iface.enabled
  if (was === on) return
  iface.enabled = on
  hold(iface.id)
  try {
    const updated = await api.updateInterface(iface.id, { enabled: on })
    Object.assign(iface, updated)
    notify(t('interface.updated'), 'success')
  } catch (err) {
    iface.enabled = was
    notify(err.message, 'error')
  } finally {
    release(iface.id)
    // The strip above the table counts what is enabled and what it carries.
    load(true)
  }
}

// Reopening one tunnel's driver. The reconciler heals most things, but not a
// driver whose Open failed at startup — a port that was taken, a tool that was
// not installed yet. Without this the only way out is restarting the panel,
// which disconnects every customer on every other interface to fix one.
async function restart(iface) {
  hold(iface.id)
  try {
    const res = await api.post(`/api/interfaces/${iface.id}/restart`)
    if (res.ok) {
      notify(t('interface.restarted'), 'success')
    } else {
      // The reason it will not come up is the whole point of asking.
      notify(t('interface.restartFailed').replace('{error}', res.error), 'error')
    }
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    release(iface.id)
  }
}

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

// The tunnel's own OpenVPN profile, which every customer on it uses.
//
// Fetched with the session token rather than linked: it carries the certificate
// authority, and a plain link sends no Authorization header — the panel would
// either refuse it or have to serve it to anyone who guessed the URL.
const downloadingProfile = ref(0)

async function downloadProfile(iface) {
  downloadingProfile.value = iface.id
  try {
    const res = await fetch(apiURL(`/api/interfaces/${iface.id}/profile`), {
      headers: { Authorization: `Bearer ${getToken()}` },
    })
    if (!res.ok) throw new Error((await res.text()) || t('error.unknown'))
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = `${iface.name}.ovpn`
    a.click()
    URL.revokeObjectURL(url)
    notify(t('interface.profileDownloaded'), 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    downloadingProfile.value = 0
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
        // Only sent for OpenVPN, and only when it actually moved: the server
        // refuses the field on a WireGuard interface rather than ignoring it.
        ...(input.protocol === 'openvpn' && input.transport ? { transport: input.transport } : {}),
      })
      notify(t('interface.updated'), 'success')
    } else {
      // transport is OpenVPN's alone and the server refuses it elsewhere, so
      // a WireGuard interface must not carry the form's default along with it.
      const { transport, ...rest } = input
      await api.createInterface({
        ...rest,
        listenPort: Number(input.listenPort),
        mtu: Number(input.mtu),
        ...(input.protocol === 'openvpn' && transport ? { transport } : {}),
      })
      notify(t('interface.created'), 'success')
    }
    formFor.value = null
    await load()
  } catch (err) {
    // A message the server attached to a field belongs under that field, not in
    // a toast the operator has to map back to one of nine inputs themselves.
    if (err?.field) {
      formRef.value?.showError(err)
    } else {
      notify(err.message, 'error')
    }
    // Deliberately not rethrown. It has been handled; letting it escape an
    // event handler sends it to Vue, which routes it to the error boundary and
    // turns "that port is out of range" into "this page could not be
    // displayed".
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
    <ErrorState v-if="loadError" :error="loadError" @retry="load()" />

    <table v-else-if="showSkeleton" class="skeleton" aria-hidden="true">
      <tbody>
        <tr v-for="n in 5" :key="n">
          <td v-for="c in 7" :key="c"><span class="sk"></span></td>
        </tr>
      </tbody>
    </table>
    <div v-else-if="loading" class="empty"></div>

    <div v-else-if="!interfaces.length" class="empty empty-cta">
      <p>{{ t('interface.noneYet') }}</p>
      <p class="small muted">{{ t('interface.noneYetHint') }}</p>
      <button class="btn" @click="formFor = {}">
        <Icon name="plus" :size="15" />
        <span>{{ t('interface.create') }}</span>
      </button>
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
                :loading="isPending(i.id)"
                @update:model-value="(v) => setEnabled(i, v)"
              />
            </td>

            <td class="right">
              <div class="actions">
                <button class="act" :title="t('action.edit')" @click="formFor = { iface: i }">
                  <Icon name="edit" :size="16" />
                </button>
                <button
                  class="act"
                  :title="t('interface.restart')"
                  :disabled="isPending(i.id)"
                  @click="restart(i)"
                >
                  <span v-if="isPending(i.id)" class="spin sm"></span>
                  <Icon v-else name="refresh" :size="16" />
                </button>
                <!-- OpenVPN only. Its profile is the tunnel's and is the same
                     for everyone on it, so it is downloaded from here once;
                     a WireGuard profile belongs to a device and comes from
                     that device instead. -->
                <button
                  v-if="i.protocol === 'openvpn'"
                  class="act"
                  :title="t('interface.downloadProfile')"
                  :disabled="downloadingProfile === i.id"
                  @click="downloadProfile(i)"
                >
                  <span v-if="downloadingProfile === i.id" class="spin sm"></span>
                  <Icon v-else name="download" :size="16" />
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
      ref="formRef"
    v-if="formFor"
    :iface="formFor.iface"
    :nodes="nodeList"
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
