<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, apiURL, getToken } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import ErrorState from '../components/ErrorState.vue'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes, relative, dateTime } from '../lib/format.js'
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

// Their search box beside the buttons: name, port, protocol.
const search = ref('')
const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return interfaces.value
  return interfaces.value.filter((i) =>
    [i.name, String(i.listenPort), i.protocol, i.endpointHost, i.nodeName].some((v) => String(v || '').toLowerCase().includes(q)),
  )
})
const hasNodes = computed(() => interfaces.value.some((i) => i.nodeName && !i.nodeLocal))

// ── the two menus: General Actions on the toolbar, and each row's ──
const generalOpen = ref(false)
const rowMenu = ref(null) // { iface, x, y }
function openRowMenu(i, e) {
  if (rowMenu.value?.iface?.id === i.id) {
    rowMenu.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  const height = 8 * 34 + 10
  const up = r.bottom + height > window.innerHeight && r.top > height
  rowMenu.value = { iface: i, x: r.left, y: up ? r.top - height - 4 : r.bottom + 4 }
}
function closeMenus() {
  rowMenu.value = null
  generalOpen.value = false
}
function onDocClick(e) {
  if (!rowMenu.value && !generalOpen.value) return
  if (e.target.closest?.('.rowmenu') || e.target.closest?.('.act') || e.target.closest?.('.more-btn')) return
  closeMenus()
}
function onKey(e) {
  if (e.key === 'Escape') closeMenus()
}
onMounted(() => {
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('scroll', closeMenus, true)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', closeMenus, true)
})

// Their row menu, in their order. Attach/Detach/Add-to-group take the
// customers on this tunnel; Delete All Clients removes them from it.
function rowItems(i) {
  const many = i.clients > 0
  const items = [
    { key: 'info', icon: 'info', label: t('iface.menu.info') },
    { key: 'export', icon: 'copy', label: t('iface.menu.exportInbound') },
    { key: 'reset', icon: 'refresh', label: t('outbound.resetTraffic') },
    { key: 'clone', icon: 'copy', label: t('interface.clone') },
    { key: 'restart', icon: 'power', label: t('interface.restart') },
    { key: 'attachExisting', icon: 'users', label: t('iface.menu.attachExisting') },
  ]
  if (many) {
    items.push(
      { key: 'detach', icon: 'users', label: t('iface.menu.detachClients') },
      { divider: true },
      { key: 'delAll', icon: 'users', label: t('iface.menu.delAllClients'), danger: true },
    )
  } else {
    items.push({ divider: true })
  }
  items.push({ key: 'delete', icon: 'trash', label: t('action.delete'), danger: true })
  if (i.protocol === 'openvpn') items.splice(1, 0, { key: 'profile', icon: 'download', label: t('interface.downloadProfile') })
  return items
}
function pickRow(i, key) {
  rowMenu.value = null
  switch (key) {
    case 'info': return (detailFor.value = i)
    case 'export': return exportOne(i)
    case 'profile': return downloadProfile(i)
    case 'reset': return resetUsage(i)
    case 'clone': return openClone(i)
    case 'restart': return restart(i)
    case 'attachExisting': return openAttach(i)
    case 'detach':
    case 'delAll': return clearTunnel(i)
    case 'delete': return removeOne(i)
  }
}

// Export: the tunnel as the JSON the API creates one from -- what
// "Import an Inbound" reads back.
const textModal = ref(null) // { title, text }
function exportable(i) {
  return {
    name: i.name, protocol: i.protocol, listenPort: i.listenPort, subnet: i.subnet,
    endpointHost: i.endpointHost, mtu: i.mtu, dns: i.dns, natInterface: i.natInterface,
    mode: i.mode, awg: i.awg || undefined, openvpn: i.openvpn || undefined,
  }
}
function exportOne(i) {
  textModal.value = { title: `${t('iface.menu.exportInbound')} — ${i.name}`, text: JSON.stringify(exportable(i), null, 2) }
}
function exportAll() {
  textModal.value = { title: t('iface.menu.exportAll'), text: JSON.stringify(interfaces.value.map(exportable), null, 2) }
}
async function copyText() {
  try {
    await navigator.clipboard.writeText(textModal.value.text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
const importOpen = ref(false)
const importText = ref('')
async function runImport() {
  let parsed
  try {
    parsed = JSON.parse(importText.value)
  } catch {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  const list = Array.isArray(parsed) ? parsed : [parsed]
  busy.value = true
  let n = 0
  try {
    for (const it of list) {
      await api.post('/api/interfaces', it)
      n++
    }
    notify(`${t('iface.menu.import')}: ${nf(n)}`, 'success')
    importOpen.value = false
    importText.value = ''
    await load()
  } catch (err) {
    notify(err.message, 'error')
    if (n) await load()
  } finally {
    busy.value = false
  }
}
function pickGeneral(key) {
  generalOpen.value = false
  if (key === 'import') importOpen.value = true
  else if (key === 'export') exportAll()
  else if (key === 'resetAll') resetAllUsage()
}
function resetAllUsage() {
  ask.value = {
    title: t('iface.menu.resetAll'),
    body: t('interface.resetUsageBody'),
    subject: tn('interface.nTunnels', interfaces.value.length),
    confirmLabel: t('outbound.reset'),
    run: async () => {
      for (const i of interfaces.value) await api.post(`/api/interfaces/${i.id}/reset-usage`)
      await load()
    },
  }
}

// Attach Existing Clients: pick customers not yet on this tunnel.
const attach = ref(null) // { iface, list, chosen: Set }
async function openAttach(i) {
  try {
    const cs = await api.get('/api/clients?perPage=500', { background: true })
    const items = (cs.items || cs || []).filter((c) => !(c.accounts || []).some((a) => a.interfaceId === i.id))
    attach.value = { iface: i, list: items, chosen: new Set(), q: '' }
  } catch (err) {
    notify(err.message, 'error')
  }
}
async function submitAttach() {
  const a = attach.value
  if (!a.chosen.size) return
  busy.value = true
  try {
    const res = await api.post('/api/clients/servers/attach', { ids: [...a.chosen], interfaceIds: [a.iface.id] })
    const failed = Object.entries(res?.failures || {})
    if (failed.length) notify(failed.map(([n, why]) => `${n}: ${why}`).join('\n'), 'error')
    else notify(`${t('iface.menu.attachExisting')} — ${nf(res?.changed || 0)}`, 'success')
    attach.value = null
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}
function toggleAttach(id) {
  const next = new Set(attach.value.chosen)
  next.has(id) ? next.delete(id) : next.add(id)
  attach.value.chosen = next
}

// Their traffic tag colour: by how much of the limit is used; ours have
// no limit, so it is the calm one.
function expiryOf(i) {
  return null
}

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
    up: list.reduce((a, i) => a + (i.upBytes || 0), 0),
    down: list.reduce((a, i) => a + (i.downBytes || 0), 0),
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
// Copying a tunnel.
//
// The port and the address range have to be new — two tunnels sharing either
// would collide — so they are suggested rather than assumed, moved one along
// from the original where that is free.
const cloning = ref(null)
const cloneBusy = ref(false)

function openClone(iface) {
  cloning.value = {
    from: iface,
    // Stepped past names already in use, so the suggestion is one that will
    // actually be accepted rather than one the operator has to correct.
    name: freeName(iface.name),
    listenPort: (iface.listenPort || 51820) + 1,
    subnet: nextSubnet(iface.subnet),
    endpointHost: iface.endpointHost || '',
  }
}

// wg0 -> wg1, ovpn443 -> ovpn444, anything else -> name-2. Only a suggestion;
// the operator can type whatever they like over it.
function nextName(name) {
  const m = String(name || '').match(/^(.*?)(\d+)$/)
  if (!m) return `${name}-2`
  return m[1] + String(Number(m[2]) + 1)
}

// freeName steps nextName along until it finds one nothing is using.
function freeName(name) {
  const taken = new Set((interfaces.value || []).map((i) => i.name))
  let candidate = nextName(name)
  for (let i = 0; i < 100 && taken.has(candidate); i++) {
    candidate = nextName(candidate)
  }
  return candidate
}

// 10.66.0.0/24 -> 10.67.0.0/24. Only the third octet moves, which is the one
// that is free to move in every range this panel hands out.
function nextSubnet(cidr) {
  const m = String(cidr || '').match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)(\/\d+)$/)
  if (!m) return cidr || ''
  const third = (Number(m[3]) + 1) % 256
  return `${m[1]}.${m[2]}.${third}.${m[4]}${m[5]}`
}

async function submitClone() {
  cloneBusy.value = true
  try {
    const c = cloning.value
    await api.post(`/api/interfaces/${c.from.id}/clone`, {
      name: c.name.trim(),
      listenPort: Number(c.listenPort),
      subnet: c.subnet.trim(),
      endpointHost: c.endpointHost.trim(),
    })
    notify(t('interface.cloned'), 'success')
    cloning.value = null
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    cloneBusy.value = false
  }
}

// Zeroing what everyone on a tunnel has used.
//
// A customer on three tunnels has one total, so this clears all of it for them
// — which is on the confirmation rather than discovered from a customer who
// suddenly has their whole allowance back.
function resetUsage(iface) {
  ask.value = {
    title: t('interface.resetUsageTitle'),
    subject: iface.name,
    body: t('interface.resetUsageBody'),
    confirmLabel: t('action.resetTraffic'),
    run: async () => {
      const res = await api.post(`/api/interfaces/${iface.id}/reset-usage`)
      notify(`${t('interface.resetUsageDone')} — ${res?.customers ?? 0}`, 'success')
      await load()
    },
  }
}

// Emptying a tunnel, which is what has to happen before it can be removed.
function clearTunnel(iface) {
  ask.value = {
    title: t('interface.clearTitle'),
    subject: iface.name,
    body: t('interface.clearBody'),
    confirmLabel: t('interface.clear'),
    danger: true,
    run: async () => {
      const res = await api.post(`/api/interfaces/${iface.id}/clear`)
      const failed = Object.entries(res?.failures || {})
      if (failed.length) {
        notify(failed.map(([name, why]) => `${name}: ${why}`).join('\n'), 'error')
      } else {
        notify(`${t('interface.clearDone')} — ${res?.changed ?? 0}`, 'success')
      }
      await load()
    },
  }
}

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
  <!-- Their summary Card: three figures. -->
  <div class="card summary-card">
    <div class="summary-grid three">
      <div class="stat">
        <div class="stat-title">{{ t('iface.stat.totalDownUp') }}</div>
        <div class="stat-value num ltr">
          <Icon name="upload" :size="16" class="stat-icon" /> {{ bytes(totals.up, store.locale) }}
          <span class="sep">/</span>
          <Icon name="download" :size="16" class="stat-icon" /> {{ bytes(totals.down, store.locale) }}
        </div>
      </div>
      <div class="stat">
        <div class="stat-title">{{ t('iface.stat.totalUsage') }}</div>
        <div class="stat-value num ltr"><Icon name="dashboard" :size="18" class="stat-icon" />{{ bytes(totals.used, store.locale) }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">{{ t('iface.stat.count') }}</div>
        <div class="stat-value num"><Icon name="menu" :size="18" class="stat-icon" />{{ nf(totals.count) }}</div>
      </div>
    </div>
  </div>

  <div class="card">
    <!-- Their Card title: Add Inbound, General Actions, the search, and --
         with rows picked -- the count and a Delete. -->
    <div class="card-head">
      <div class="card-toolbar">
        <button class="btn primary" @click="formFor = {}">
          <Icon name="plus" :size="14" />
          <span>{{ t('iface.menu.add') }}</span>
        </button>
        <div class="more-wrap">
          <button class="btn primary more-btn" :aria-expanded="generalOpen" @click="generalOpen = !generalOpen">
            <Icon name="menu" :size="14" />
            <span>{{ t('iface.menu.general') }}</span>
          </button>
          <div v-if="generalOpen" class="rowmenu below" role="menu">
            <button class="menu-item" role="menuitem" @click="pickGeneral('import')"><Icon name="download" :size="14" />{{ t('iface.menu.import') }}</button>
            <button class="menu-item" role="menuitem" @click="pickGeneral('export')"><Icon name="upload" :size="14" />{{ t('iface.menu.exportAll') }}</button>
            <button class="menu-item" role="menuitem" @click="pickGeneral('resetAll')"><Icon name="refresh" :size="14" />{{ t('iface.menu.resetAll') }}</button>
          </div>
        </div>
        <div class="search">
          <Icon name="search" :size="14" />
          <input v-model="search" type="search" :placeholder="t('action.search')" :aria-label="t('action.search')" />
        </div>
        <template v-if="selected.size">
          <span class="tag blue selchip">
            {{ t('client.menu.selectedCount').replace('{count}', nf(selected.size)) }}
            <button type="button" class="chip-x" :aria-label="t('action.cancel')" @click="selected = new Set()"><Icon name="close" :size="11" /></button>
          </span>
          <button class="btn danger-ghost" @click="bulkDelete">
            <Icon name="trash" :size="14" />
            <span>{{ t('action.delete') }}</span>
          </button>
        </template>
      </div>
    </div>

    <div class="card-body ifaces-body">
      <ErrorState v-if="loadError" :error="loadError" @retry="load()" />

      <table v-else-if="showSkeleton" class="skeleton" aria-hidden="true">
        <tbody>
          <tr v-for="n in 5" :key="n">
            <td v-for="c in 10" :key="c"><span class="sk"></span></td>
          </tr>
        </tbody>
      </table>
      <div v-else-if="loading" class="empty"></div>

      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th class="tick">
                <input type="checkbox" :checked="allSelected" :aria-label="t('action.selectAll')" @change="toggleAll($event.target.checked)" />
              </th>
              <th class="w-id right">ID</th>
              <th class="w-menu center">{{ t('iface.col.menu') }}</th>
              <th class="w-enable center">{{ t('table.enabled') }}</th>
              <th class="w-remark center">{{ t('iface.col.remark') }}</th>
              <th v-if="hasNodes" class="w-node center">{{ t('iface.col.node') }}</th>
              <th class="w-port center">{{ t('interface.port') }}</th>
              <th class="w-proto">{{ t('client.protocol') }}</th>
              <th class="w-clients">{{ t('nav.clients') }}</th>
              <th class="w-itraffic center">{{ t('client.traffic') }}</th>
              <th class="w-speed center">{{ t('client.speed') }}</th>
              <th class="w-dur center">{{ t('iface.col.duration') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="!visible.length" class="empty-row">
              <td :colspan="hasNodes ? 12 : 11">
                <div class="card-empty">
                  <Icon name="server" :size="32" />
                  <div>{{ t('common.nothingYet') }}</div>
                  <button v-if="!interfaces.length" class="btn sm primary" @click="formFor = {}">{{ t('iface.menu.add') }}</button>
                </div>
              </td>
            </tr>
            <tr v-for="i in visible" :key="i.id" :class="{ picked: selected.has(i.id), off: !i.enabled }">
              <td class="tick">
                <input type="checkbox" :checked="selected.has(i.id)" :aria-label="i.name" @change="toggleOne(i.id, $event.target.checked)" />
              </td>
              <td class="right num">{{ i.id }}</td>
              <td class="center">
                <div class="action-buttons center">
                  <button class="act text" :title="t('action.edit')" @click="formFor = { iface: i }"><Icon name="edit" :size="16" /></button>
                  <button class="act text" :title="t('action.more')" :aria-expanded="rowMenu?.iface?.id === i.id" @click="openRowMenu(i, $event)"><Icon name="more" :size="16" /></button>
                </div>
              </td>
              <td class="center">
                <Toggle :model-value="i.enabled" :label="i.name" :loading="isPending(i.id)" @update:model-value="(v) => setEnabled(i, v)" />
              </td>
              <td class="center"><span class="remark">{{ i.name }}</span></td>
              <td v-if="hasNodes" class="center">
                <span v-if="i.nodeName && !i.nodeLocal" class="tag" :class="i.nodeUp ? 'blue' : 'red'">{{ i.nodeName }}</span>
                <span v-else class="tag">{{ t('iface.col.localPanel') }}</span>
              </td>
              <td class="center num">{{ i.listenPort }}</td>
              <td>
                <div class="protocol-tags">
                  <span class="tag purple">{{ i.protocol }}</span>
                  <span class="tag green">{{ i.protocol === 'openvpn' ? (i.openvpn?.transport || 'udp').toUpperCase() : 'UDP' }}</span>
                  <span v-if="i.mode === 'amnezia'" class="tag blue">AmneziaWG</span>
                </div>
              </td>
              <td>
                <span class="tag count" :title="t('nav.clients')"><Icon name="users" :size="12" /> {{ nf(i.clients) }}</span>
                <span class="tag green count" :title="t('status.active')">{{ nf(i.active) }}</span>
                <span v-if="i.disabled" class="tag count" :title="t('status.disabled')">{{ nf(i.disabled) }}</span>
                <span v-if="i.depleted" class="tag red count" :title="t('stat.depleted')">{{ nf(i.depleted) }}</span>
                <span v-if="i.online" class="tag blue count" :title="t('status.online')">{{ nf(i.online) }}</span>
              </td>
              <td class="center">
                <span class="tag green num ltr" :title="`↑ ${bytes(i.upBytes || 0, store.locale)}  ↓ ${bytes(i.downBytes || 0, store.locale)}`">
                  {{ bytes(i.usedBytes, store.locale) }} / ∞
                </span>
              </td>
              <td class="center"><span class="tag num ltr speed-tag">—</span></td>
              <td class="center"><span class="tag purple">∞</span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>

  <Teleport to="body">
    <div v-if="rowMenu" class="rowmenu" role="menu" :style="{ top: rowMenu.y + 'px', left: rowMenu.x + 'px' }">
      <template v-for="(m, idx) in rowItems(rowMenu.iface)" :key="m.key || `d${idx}`">
        <hr v-if="m.divider" class="menu-divider" />
        <button v-else class="menu-item" :class="{ danger: m.danger }" role="menuitem" @click="pickRow(rowMenu.iface, m.key)">
          <Icon :name="m.icon" :size="14" />{{ m.label }}
        </button>
      </template>
    </div>
  </Teleport>

  <div v-if="textModal" class="modal-backdrop" @click.self="textModal = null">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="tx-title">
      <div class="card-head">
        <h2 id="tx-title">{{ textModal.title }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="textModal = null"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body"><div class="field"><textarea class="ltr mono" rows="14" readonly spellcheck="false" :value="textModal.text"></textarea></div></div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="textModal = null">{{ t('common.close') }}</button>
        <button class="btn primary" @click="copyText"><Icon name="copy" :size="14" /><span>{{ t('action.copy') }}</span></button>
      </div>
    </div>
  </div>

  <div v-if="importOpen" class="modal-backdrop" @click.self="importOpen = false">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="im-title">
      <div class="card-head">
        <h2 id="im-title">{{ t('iface.menu.import') }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="importOpen = false"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body"><div class="field"><textarea v-model="importText" class="ltr mono" rows="14" spellcheck="false" placeholder="{ ... }"></textarea></div></div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="importOpen = false">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !importText.trim()" @click="runImport">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('iface.menu.importBtn') }}</template>
        </button>
      </div>
    </div>
  </div>

  <div v-if="attach" class="modal-backdrop" @click.self="attach = null">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="at-title">
      <div class="card-head">
        <h2 id="at-title">{{ t('iface.menu.attachExisting') }} — {{ attach.iface.name }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="attach = null"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field">
          <input v-model="attach.q" :placeholder="t('action.search')" />
        </div>
        <ul class="picklist">
          <li v-for="c in attach.list.filter((x) => !attach.q || x.name.toLowerCase().includes(attach.q.toLowerCase()))" :key="c.id">
            <label class="pick">
              <input type="checkbox" :checked="attach.chosen.has(c.id)" @change="toggleAttach(c.id)" />
              <span class="pick-name">{{ c.name }}</span>
              <span v-if="c.group" class="tag geekblue">{{ c.group }}</span>
              <span class="muted small ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
            </label>
          </li>
          <li v-if="!attach.list.length" class="muted small pick">{{ t('common.nothingYet') }}</li>
        </ul>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="attach = null">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !attach.chosen.size" @click="submitAttach">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('iface.menu.attach') }}</template>
        </button>
      </div>
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

  <!-- Copying a tunnel. Its own name, port, range and keys; no customers. -->
  <div v-if="cloning" class="modal-backdrop" @click.self="cloning = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="cl-title">
      <div class="card-head">
        <h2 id="cl-title">{{ t('interface.cloneTitle') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="cloning = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="cl-form" class="card-body" @submit.prevent="submitClone">
        <p class="muted small">{{ cloning.from.name }} → {{ cloning.name }}</p>

        <div class="field">
          <label for="cl-name">{{ t('interface.name') }}</label>
          <input id="cl-name" v-model="cloning.name" required autofocus maxlength="32" class="ltr" />
        </div>

        <div class="grid-2">
          <div class="field">
            <label for="cl-port">{{ t('interface.port') }}</label>
            <input id="cl-port" v-model="cloning.listenPort" type="number" min="1" max="65535" required class="ltr" />
          </div>
          <div class="field">
            <label for="cl-subnet">{{ t('interface.subnet') }}</label>
            <input id="cl-subnet" v-model="cloning.subnet" required class="ltr" />
          </div>
        </div>

        <div class="field">
          <span class="hint">{{ t('interface.cloneHint') }}</span>
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="cloning = null">{{ t('action.cancel') }}</button>
        <button type="submit" form="cl-form" class="btn primary" :disabled="cloneBusy">
          <span v-if="cloneBusy" class="spin"></span>
          <span v-else>{{ t('interface.clone') }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.summary-card {
  padding: 12px 16px;
  margin-bottom: 12px;
}
.summary-grid.three {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px 16px;
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.stat-title {
  font-size: 14px;
  color: var(--muted);
}
.stat-value {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 24px;
  line-height: 32px;
}
.stat-icon { color: var(--muted); }
.sep { color: var(--faint); }
@media (max-width: 760px) { .summary-grid.three { grid-template-columns: 1fr 1fr; } }

.card-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  width: 100%;
  padding: 6px 0;
}
.card-toolbar .search {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 200px;
  height: 32px;
  padding: 0 11px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--faint);
}
.card-toolbar .search input {
  flex: 1;
  min-width: 0;
  height: 100%;
  border: 0;
  background: none;
  color: var(--ink);
  font-size: 14px;
}
.card-toolbar .search input:focus { outline: none; box-shadow: none; }
.selchip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.chip-x {
  display: inline-flex;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  opacity: 0.6;
  cursor: pointer;
}
.ifaces-body { padding: 16px; }

/* Their columns and widths. */
.w-id { width: 60px; }
.w-menu { width: 70px; }
.w-enable { width: 80px; }
.w-remark { width: 90px; }
.w-node { width: 130px; }
.w-port { width: 80px; }
.w-proto { width: 190px; }
.w-clients { width: 200px; }
.w-itraffic { width: 140px; }
.w-speed { width: 110px; }
.w-dur { width: 100px; }
th.center, td.center { text-align: center; }
th.right, td.right { text-align: end; }
.action-buttons.center {
  display: flex;
  justify-content: center;
  gap: 4px;
}
.act.text {
  width: 24px;
  height: 24px;
  color: var(--ink);
}
.remark { font-weight: 500; }
.protocol-tags {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px;
}
.tag.count {
  margin: 0 4px 0 0;
  padding: 0 4px;
  font-variant-numeric: tabular-nums;
}
.speed-tag { min-width: 72px; }
tr.off td { opacity: 0.6; }

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
