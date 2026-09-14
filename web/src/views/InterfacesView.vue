<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { api, apiURL, getToken } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import ErrorState from '../components/ErrorState.vue'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import InterfaceForm from '../components/InterfaceForm.vue'
import InterfaceDetail from '../components/InterfaceDetail.vue'
import Toggle from '../components/Toggle.vue'
import Icon from '../components/Icon.vue'
import AntIcon from '../components/AntIcon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import { useIsMobile } from '../lib/mobile.js'

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
// On a phone the table becomes a list of cards, as the classic panel's inbounds do;
// tapping a card's info opens its stats.
const isMobile = useIsMobile()
const cardOpen = ref(new Set())
function toggleCard(id) {
  const next = new Set(cardOpen.value)
  next.has(id) ? next.delete(id) : next.add(id)
  cardOpen.value = next
}

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

// Their sortable headers. A click goes ascending, another descending, a
// third back to the order the server gave, exactly as Ant's Table cycles.
const sort = ref({ key: '', dir: '' })
const cmpText = (a, b) => String(a || '').localeCompare(String(b || ''), undefined, { numeric: true, sensitivity: 'base' })
const sorters = {
  id: (a, b) => a.id - b.id,
  remark: (a, b) => cmpText(a.name, b.name),
  node: (a, b) => cmpText(a.nodeName, b.nodeName),
  port: (a, b) => a.listenPort - b.listenPort,
  protocol: (a, b) => cmpText(a.protocol, b.protocol),
  clients: (a, b) => (a.clients || 0) - (b.clients || 0),
  traffic: (a, b) => (a.usedBytes || 0) - (b.usedBytes || 0),
  speed: () => 0,
  expiry: () => 0,
}
function sortCls(key) {
  return sort.value.key === key ? `sorted ${sort.value.dir}` : ''
}
function sortBy(key) {
  const cur = sort.value
  if (cur.key !== key) sort.value = { key, dir: 'asc' }
  else if (cur.dir === 'asc') sort.value = { key, dir: 'desc' }
  else sort.value = { key: '', dir: '' }
}
const sorted = computed(() => {
  const { key, dir } = sort.value
  if (!key || !sorters[key]) return visible.value
  const rows = [...visible.value].sort(sorters[key])
  return dir === 'desc' ? rows.reverse() : rows
})

// Their pagination: the size from the settings page, and no bar at all when
// everything fits on one page.
const page = ref(1)
const pageSize = computed(() => (store.panel.pageSize > 0 ? store.panel.pageSize : sorted.value.length || 1))
const pageCount = computed(() => Math.max(1, Math.ceil(sorted.value.length / pageSize.value)))
// Clamped from a watcher, not inside the computed: a filter that shrinks
// the list must not write to the page while the page is being read.
watch(pageCount, (n) => { if (page.value > n) page.value = n })
const paged = computed(() => {
  const start = (Math.min(page.value, pageCount.value) - 1) * pageSize.value
  return sorted.value.slice(start, start + pageSize.value)
})
const pageNumbers = computed(() => Array.from({ length: pageCount.value }, (_, i) => i + 1))

// ── the two menus: General Actions on the toolbar, and each row's ──
const generalOpen = ref(false)
const rowMenu = ref(null) // { iface, x, y }
function openRowMenu(i, e) {
  if (rowMenu.value?.iface?.id === i.id) {
    rowMenu.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  const height = rowItems(i).length * 32 + 8
  const up = r.bottom + height > window.innerHeight && r.top > height
  rowMenu.value = { iface: i, x: r.left, y: up ? r.top - height - 4 : r.bottom + 4 }
}
function closeMenus() {
  rowMenu.value = null
  generalOpen.value = false
}
function onDocClick(e) {
  if (!rowMenu.value && !generalOpen.value) return
  if (e.target.closest?.('.amenu') || e.target.closest?.('.abtn.text') || e.target.closest?.('.more-btn')) return
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

// Their row menu, item for item: what the classic panel shows for an inbound that
// carries many customers (every one of ours does). Restart is the one
// addition; a kernel tunnel can be bounced, an Xray inbound cannot.
function rowItems(i) {
  const many = i.clients > 0
  const items = [
    { key: 'urls', icon: 'ExportOutlined', label: t('iface.menu.exportUrls') },
    { key: 'export', icon: 'CopyOutlined', label: t('iface.menu.exportInbound') },
    { key: 'reset', icon: 'RetweetOutlined', label: t('outbound.resetTraffic') },
    { key: 'clone', icon: 'BlockOutlined', label: t('interface.clone') },
    { key: 'restart', icon: 'ReloadOutlined', label: t('interface.restart') },
    { key: 'attachExisting', icon: 'UsergroupAddOutlined', label: t('iface.menu.attachExisting') },
  ]
  if (i.protocol === 'openvpn') items.splice(2, 0, { key: 'profile', icon: 'ExportOutlined', label: t('interface.downloadProfile') })
  if (many) {
    items.push(
      { key: 'attachTo', icon: 'UsergroupAddOutlined', label: t('iface.menu.attachTo') },
      { key: 'detach', icon: 'UsergroupDeleteOutlined', label: t('iface.menu.detachClients') },
      { key: 'group', icon: 'TagsOutlined', label: t('iface.menu.addToGroup') },
      { divider: true },
      { key: 'delAll', icon: 'UsergroupDeleteOutlined', label: t('iface.menu.delAllClients'), danger: true },
    )
  } else {
    items.push({ divider: true })
  }
  items.push({ key: 'delete', icon: 'DeleteOutlined', label: t('action.delete'), danger: true })
  return items
}
function pickRow(i, key) {
  rowMenu.value = null
  switch (key) {
    case 'urls': return exportUrls(i)
    case 'export': return exportOne(i)
    case 'profile': return downloadProfile(i)
    case 'reset': return resetUsage(i)
    case 'clone': return openClone(i)
    case 'restart': return restart(i)
    case 'attachExisting': return openAttach(i)
    case 'attachTo': return openMove(i, 'attach')
    case 'detach': return openMove(i, 'detach')
    case 'group': return openGroup(i)
    case 'delAll': return clearTunnel(i)
    case 'delete': return removeOne(i)
  }
}

// The customers on one tunnel, for the actions that take all of them.
async function clientsOn(i) {
  const cs = await api.get(`/api/clients?perPage=500&interfaceIds=${i.id}`, { background: true })
  return cs.items || cs || []
}

// Export All URLs: every customer's subscription link, one per line, the
// way the classic panel hands over every client's share link at once.
async function exportUrls(i) {
  try {
    const list = await clientsOn(i)
    const links = []
    for (const c of list) {
      const r = await api.get(`/api/clients/${c.id}/subscription`, { background: true })
      if (r?.link) links.push(r.link)
    }
    textModal.value = { title: `${t('iface.menu.exportUrls')} — ${i.name}`, text: links.join('\n') }
  } catch (err) {
    notify(err.message, 'error')
  }
}

// Attach Clients To… / Detach Clients: this tunnel's customers put on, or
// taken off, another tunnel.
const move = ref(null) // { iface, kind, target }
async function openMove(i, kind) {
  move.value = { iface: i, kind, target: null }
}
async function submitMove() {
  const m = move.value
  if (!m.target) return
  busy.value = true
  try {
    const list = await clientsOn(m.iface)
    const res = await api.post(`/api/clients/servers/${m.kind}`, { ids: list.map((c) => c.id), interfaceIds: [m.target] })
    const failed = Object.entries(res?.failures || {})
    if (failed.length) notify(failed.map(([n, why]) => `${n}: ${why}`).join('\n'), 'error')
    else notify(`${m.kind === 'attach' ? t('iface.menu.attachTo') : t('iface.menu.detachClients')} — ${nf(res?.changed || 0)}`, 'success')
    move.value = null
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

// Add Clients To Group…: every customer here gets the group.
const grouping = ref(null) // { iface, name, names }
async function openGroup(i) {
  let names = []
  try { names = await api.groupNames() } catch { /* the box still takes a new name */ }
  grouping.value = { iface: i, name: '', names }
}
async function submitGroup() {
  const g = grouping.value
  if (!g.name.trim()) return
  busy.value = true
  try {
    const list = await clientsOn(g.iface)
    await api.assignGroup(g.name.trim(), list.map((c) => c.id))
    notify(`${t('iface.menu.addToGroup')} — ${nf(list.length)}`, 'success')
    grouping.value = null
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
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
async function exportAllUrls() {
  const links = []
  try {
    for (const i of interfaces.value) {
      for (const c of await clientsOn(i)) {
        const r = await api.get(`/api/clients/${c.id}/subscription`, { background: true })
        if (r?.link && !links.includes(r.link)) links.push(r.link)
      }
    }
    textModal.value = { title: t('iface.menu.exportUrls'), text: links.join('\n') }
  } catch (err) {
    notify(err.message, 'error')
  }
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
  else if (key === 'export') exportAllUrls()
  else if (key === 'exportAll') exportAll()
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

// The strip the classic panel puts above its inbound table: what the whole set is carrying.
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
  <div class="antpage inbounds">
  <!-- Their summary Card: size="small", three Statistics. -->
  <div class="acard small summary-card">
    <div class="acard-body">
      <div class="arow">
        <div class="acol">
          <div class="stat-title">{{ t('iface.stat.totalDownUp') }}</div>
          <div class="stat-content ltr">
            <span><AntIcon name="ArrowUpOutlined" /> {{ bytes(totals.up, store.locale) }} / <AntIcon name="ArrowDownOutlined" /> {{ bytes(totals.down, store.locale) }}</span>
          </div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('iface.stat.totalUsage') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="PieChartOutlined" /></span><span>{{ bytes(totals.used, store.locale) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('iface.stat.count') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="BarsOutlined" /></span><span>{{ nf(totals.count) }}</span></div>
        </div>
      </div>
    </div>
  </div>

  <!-- Their list Card: the title is a Space of Add Inbound, General Actions,
       the search, and -- with rows picked -- the count and a Delete. -->
  <div class="acard">
    <div class="acard-head">
      <div class="aspace">
        <button class="abtn primary" @click="formFor = {}">
          <AntIcon name="PlusOutlined" /><span v-if="!isMobile">{{ t('iface.menu.add') }}</span>
        </button>
        <div class="more-wrap">
          <button class="abtn primary more-btn" :aria-expanded="generalOpen" @click="generalOpen = !generalOpen">
            <AntIcon name="MenuOutlined" /><span v-if="!isMobile">{{ t('iface.menu.general') }}</span>
          </button>
          <div v-if="generalOpen" class="amenu below" role="menu">
            <button class="amenu-item" role="menuitem" @click="pickGeneral('import')"><AntIcon name="ImportOutlined" /><span>{{ t('iface.menu.import') }}</span></button>
            <button class="amenu-item" role="menuitem" @click="pickGeneral('export')"><AntIcon name="ExportOutlined" /><span>{{ t('iface.menu.exportUrls') }}</span></button>
            <button class="amenu-item" role="menuitem" @click="pickGeneral('exportAll')"><AntIcon name="CopyOutlined" /><span>{{ t('iface.menu.exportAll') }}</span></button>
            <button class="amenu-item" role="menuitem" @click="pickGeneral('resetAll')"><AntIcon name="ReloadOutlined" /><span>{{ t('iface.menu.resetAll') }}</span></button>
          </div>
        </div>
        <label class="ainput">
          <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
          <input v-model="search" type="text" :placeholder="t('iface.menu.search')" :aria-label="t('iface.menu.search')" />
          <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.cancel')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
        </label>
        <template v-if="selected.size">
          <span class="atag blue closable">
            {{ t('client.menu.selectedCount').replace('{count}', nf(selected.size)) }}
            <button type="button" class="atag-close" :aria-label="t('action.cancel')" @click="selected = new Set()"><AntIcon name="CloseOutlined" /></button>
          </span>
          <button class="abtn danger" @click="bulkDelete">
            <AntIcon name="DeleteOutlined" /><span v-if="!isMobile">{{ t('action.delete') }}</span>
          </button>
        </template>
      </div>
    </div>

    <div class="acard-body">
      <ErrorState v-if="loadError" :error="loadError" @retry="load()" />

      <PageSpin v-else-if="showSkeleton" />
      <div v-else-if="loading" class="empty"></div>

      <div v-else-if="isMobile" class="inbound-cards">
        <div v-if="!sorted.length" class="card-empty">
          <AntIcon name="ImportOutlined" :size="28" style="opacity: 0.5" />
          <div>{{ t('common.nothingYet') }}</div>
        </div>
        <template v-else>
          <div class="card-bulk-bar">
            <label class="acheckbox">
              <input type="checkbox" class="acheck" :checked="allSelected" @change="toggleAll($event.target.checked)" />
              <span>{{ t('action.selectAll') }}</span>
            </label>
            <span v-if="selected.size" class="bulk-count">{{ nf(selected.size) }}</span>
          </div>
          <div v-for="i in paged" :key="i.id" class="inbound-card" :class="{ 'is-selected': selected.has(i.id) }">
            <div class="card-head" @click="toggleCard(i.id)">
              <input type="checkbox" class="acheck" :checked="selected.has(i.id)" :aria-label="i.name" @click.stop @change="toggleOne(i.id, $event.target.checked)" />
              <span class="card-id">#{{ i.id }}</span>
              <span class="tag-name">{{ i.name }}</span>
              <div class="card-actions" @click.stop>
                <button type="button" class="row-action-trigger" :aria-label="t('iface.menu.info')" :title="t('iface.menu.info')" @click="toggleCard(i.id)"><AntIcon name="InfoCircleOutlined" /></button>
                <Toggle :model-value="i.enabled" :label="i.name" :loading="isPending(i.id)" small @update:model-value="(v) => setEnabled(i, v)" />
                <button type="button" class="row-action-trigger" :aria-label="t('action.more')" :aria-expanded="rowMenu?.iface?.id === i.id" @click="openRowMenu(i, $event)"><AntIcon name="MoreOutlined" /></button>
              </div>
            </div>
            <div v-if="cardOpen.has(i.id)" class="card-stats">
              <div class="stat-row">
                <span class="stat-label">{{ t('client.protocol') }}</span>
                <span class="atag purple">{{ i.protocol }}</span>
                <span class="atag green">{{ i.protocol === 'openvpn' ? (i.openvpn?.transport || 'udp').toUpperCase() : 'UDP' }}</span>
                <span v-if="i.mode === 'amnezia'" class="atag blue">AmneziaWG</span>
              </div>
              <div class="stat-row">
                <span class="stat-label">{{ t('interface.port') }}</span>
                <span class="ltr">{{ i.listenPort }}</span>
              </div>
              <div class="stat-row">
                <span class="stat-label">{{ t('nav.clients') }}</span>
                <span class="atag count"><AntIcon name="TeamOutlined" /> {{ nf(i.clients) }}</span>
                <span class="atag green count">{{ nf(i.active) }}</span>
                <span v-if="i.depleted" class="atag red count">{{ nf(i.depleted) }}</span>
                <span v-if="i.online" class="atag blue count">{{ nf(i.online) }}</span>
              </div>
              <div class="stat-row">
                <span class="stat-label">{{ t('client.traffic') }}</span>
                <span class="atag purple ltr">{{ bytes(i.usedBytes, store.locale) }} / <span class="infinity">∞</span></span>
              </div>
            </div>
          </div>
          <ul v-if="pageCount > 1" class="apagination">
            <li><button class="apage" :disabled="page <= 1" :aria-label="t('action.previous')" @click="page--">‹</button></li>
            <li v-for="n in pageNumbers" :key="n"><button class="apage" :class="{ active: n === page }" @click="page = n">{{ n }}</button></li>
            <li><button class="apage" :disabled="page >= pageCount" :aria-label="t('action.next')" @click="page++">›</button></li>
          </ul>
        </template>
      </div>

      <div v-else class="atable-wrap">
        <table class="atable small" :style="{ minWidth: (hasNodes ? 1366 : 1236) + 'px' }">
          <thead>
            <tr>
              <th class="sel">
                <input type="checkbox" class="acheck" :checked="allSelected" :aria-label="t('action.selectAll')" @change="toggleAll($event.target.checked)" />
              </th>
              <th class="w-id right sortable" :class="sortCls('id')" @click="sortBy('id')">
                <div class="sorters"><span class="title">ID</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-menu center">{{ t('iface.col.menu') }}</th>
              <th class="w-enable center">{{ t('table.enabled') }}</th>
              <th class="w-remark center sortable" :class="sortCls('remark')" @click="sortBy('remark')">
                <div class="sorters"><span class="title">{{ t('iface.col.remark') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th v-if="hasNodes" class="w-node center sortable" :class="sortCls('node')" @click="sortBy('node')">
                <div class="sorters"><span class="title">{{ t('iface.col.node') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-port center sortable" :class="sortCls('port')" @click="sortBy('port')">
                <div class="sorters"><span class="title">{{ t('interface.port') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-proto sortable" :class="sortCls('protocol')" @click="sortBy('protocol')">
                <div class="sorters"><span class="title">{{ t('client.protocol') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-clients sortable" :class="sortCls('clients')" @click="sortBy('clients')">
                <div class="sorters"><span class="title">{{ t('nav.clients') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-itraffic center sortable" :class="sortCls('traffic')" @click="sortBy('traffic')">
                <div class="sorters"><span class="title">{{ t('client.traffic') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-speed center sortable" :class="sortCls('speed')" @click="sortBy('speed')">
                <div class="sorters"><span class="title">{{ t('client.speed') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
              <th class="w-dur center sortable" :class="sortCls('expiry')" @click="sortBy('expiry')">
                <div class="sorters"><span class="title">{{ t('iface.col.duration') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="!sorted.length" class="empty-row">
              <td :colspan="hasNodes ? 12 : 11">
                <div class="card-empty">
                  <AntIcon name="ImportOutlined" :size="32" />
                  <div>{{ t('common.nothingYet') }}</div>
                </div>
              </td>
            </tr>
            <tr v-for="i in paged" :key="i.id" :class="{ picked: selected.has(i.id) }">
              <td class="sel">
                <input type="checkbox" class="acheck" :checked="selected.has(i.id)" :aria-label="i.name" @change="toggleOne(i.id, $event.target.checked)" />
              </td>
              <td class="right">{{ i.id }}</td>
              <td class="center">
                <div class="action-buttons">
                  <button class="abtn text sm" :title="t('action.edit')" @click="formFor = { iface: i }"><AntIcon name="EditOutlined" /></button>
                  <button class="abtn text sm" :title="t('action.more')" :aria-expanded="rowMenu?.iface?.id === i.id" @click="openRowMenu(i, $event)"><AntIcon name="MoreOutlined" /></button>
                </div>
              </td>
              <td class="center">
                <Toggle :model-value="i.enabled" :label="i.name" :loading="isPending(i.id)" @update:model-value="(v) => setEnabled(i, v)" />
              </td>
              <td class="center">{{ i.name }}</td>
              <td v-if="hasNodes" class="center">
                <span v-if="i.nodeName && !i.nodeLocal" class="atag" :class="i.nodeUp ? 'blue' : 'red'">{{ i.nodeName }}</span>
                <span v-else class="atag">{{ t('iface.col.localPanel') }}</span>
              </td>
              <td class="center">{{ i.listenPort }}</td>
              <td>
                <div class="protocol-tags">
                  <span class="atag purple">{{ i.protocol }}</span>
                  <span class="atag green">{{ i.protocol === 'openvpn' ? (i.openvpn?.transport || 'udp').toUpperCase() : 'UDP' }}</span>
                  <span v-if="i.mode === 'amnezia'" class="atag blue">AmneziaWG</span>
                </div>
              </td>
              <td>
                <span class="atag count" :title="t('nav.clients')"><AntIcon name="TeamOutlined" /> {{ nf(i.clients) }}</span>
                <span class="atag green count" :title="t('status.active')">{{ nf(i.active) }}</span>
                <span v-if="i.disabled" class="atag count" :title="t('status.disabled')">{{ nf(i.disabled) }}</span>
                <span v-if="i.depleted" class="atag red count" :title="t('stat.depleted')">{{ nf(i.depleted) }}</span>
                <span v-if="i.online" class="atag blue count last" :title="t('status.online')">{{ nf(i.online) }}</span>
              </td>
              <td class="center">
                <span class="atag purple ltr" :title="`↑ ${bytes(i.upBytes || 0, store.locale)}  ↓ ${bytes(i.downBytes || 0, store.locale)}`">
                  {{ bytes(i.usedBytes, store.locale) }} / <span class="infinity">∞</span>
                </span>
              </td>
              <td class="center"><span class="atag speed-tag">—</span></td>
              <td class="center"><span class="atag purple"><span class="infinity">∞</span></span></td>
            </tr>
          </tbody>
        </table>
        <ul v-if="pageCount > 1" class="apagination">
          <li><button class="apage" :disabled="page <= 1" :aria-label="t('action.previous')" @click="page--">‹</button></li>
          <li v-for="n in pageNumbers" :key="n"><button class="apage" :class="{ active: n === page }" @click="page = n">{{ n }}</button></li>
          <li><button class="apage" :disabled="page >= pageCount" :aria-label="t('action.next')" @click="page++">›</button></li>
        </ul>
      </div>
    </div>
  </div>
  </div>

  <Teleport to="body">
    <div v-if="rowMenu" class="amenu" role="menu" :style="{ top: rowMenu.y + 'px', left: rowMenu.x + 'px' }">
      <template v-for="(m, idx) in rowItems(rowMenu.iface)" :key="m.key || `d${idx}`">
        <hr v-if="m.divider" class="amenu-divider" />
        <button v-else class="amenu-item" :class="{ danger: m.danger }" role="menuitem" @click="pickRow(rowMenu.iface, m.key)">
          <AntIcon :name="m.icon" /><span>{{ m.label }}</span>
        </button>
      </template>
    </div>
  </Teleport>

  <!-- Attach Clients To… / Detach Clients: a target tunnel to pick. -->
  <div v-if="move" class="modal-backdrop" @click.self="move = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="mv-title">
      <div class="card-head">
        <h2 id="mv-title">{{ move.kind === 'attach' ? t('iface.menu.attachTo') : t('iface.menu.detachClients') }} — {{ move.iface.name }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="move = null"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field">
          <label for="mv-target">{{ t('iface.menu.targetInbound') }}</label>
          <select id="mv-target" v-model="move.target">
            <option :value="null" disabled>—</option>
            <option v-for="o in interfaces.filter((x) => x.id !== move.iface.id)" :key="o.id" :value="o.id">{{ o.name }} · {{ o.protocol }}:{{ o.listenPort }}</option>
          </select>
        </div>
        <p class="muted small">{{ tn('interface.nClients', move.iface.clients || 0) }}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="move = null">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !move.target" @click="submitMove">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ move.kind === 'attach' ? t('iface.menu.attach') : t('iface.menu.detach') }}</template>
        </button>
      </div>
    </div>
  </div>

  <!-- Add Clients To Group…: a name, existing or new. -->
  <div v-if="grouping" class="modal-backdrop" @click.self="grouping = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="gr-title">
      <div class="card-head">
        <h2 id="gr-title">{{ t('iface.menu.addToGroup') }} — {{ grouping.iface.name }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="grouping = null"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field">
          <label for="gr-name">{{ t('client.group') }}</label>
          <input id="gr-name" v-model="grouping.name" list="gr-names" autofocus />
          <datalist id="gr-names"><option v-for="n in grouping.names" :key="n" :value="n" /></datalist>
        </div>
        <p class="muted small">{{ tn('interface.nClients', grouping.iface.clients || 0) }}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="grouping = null">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !grouping.name.trim()" @click="submitGroup">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>

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
/* Their column widths, exactly */
.w-id { width: 60px; }
.w-menu { width: 70px; }
.w-enable { width: 80px; }
.w-remark { width: 90px; }
.w-node { width: 130px; }
.w-port { width: 80px; }
.w-proto { width: 190px; }
.w-clients { width: 200px; }
.w-itraffic { width: 140px; }
.w-speed { width: 216px; }
.w-dur { width: 100px; }
.atable th.center, .atable td.center { text-align: center; }
.atable th.right, .atable td.right { text-align: end; }
.action-buttons {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}
.card-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 24px 12px;
  color: var(--muted);
}
.card-empty .anticon { margin-bottom: 8px; }

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
