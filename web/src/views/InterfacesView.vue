<script setup>
// The tunnels: the strip of what they carry, a list by server with search,
// sorting and pages, and what can be done to each. The rows, the actions and
// the dialogs are their own; this holds the list's state and wires them.
import { ref, computed, watch, onMounted } from 'vue'
import { api } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import { store, t, notify } from '../lib/store.js'
import { cmpText, generalItems, nextSort, ocPayload, sortRows, totals as totalsOf } from '../lib/interfaces/rows.js'
import { useInterfaceActions } from '../lib/interfaces/actions.js'
import ErrorState from '../components/ErrorState.vue'
import InterfaceForm from '../components/InterfaceForm.vue'
import InterfaceDetail from '../components/InterfaceDetail.vue'
import AntIcon from '../components/AntIcon.vue'
import MoreMenu from '../components/MoreMenu.vue'
import TextDialog from '../components/TextDialog.vue'
import InterfaceStats from '../components/interfaces/InterfaceStats.vue'
import InterfacesTable from '../components/interfaces/InterfacesTable.vue'
import InterfaceCards from '../components/interfaces/InterfaceCards.vue'
import ImportDialog from '../components/interfaces/ImportDialog.vue'
import AttachDialog from '../components/interfaces/AttachDialog.vue'
import CloneDialog from '../components/interfaces/CloneDialog.vue'
import MoveDialog from '../components/interfaces/MoveDialog.vue'
import GroupDialog from '../components/interfaces/GroupDialog.vue'
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
// On a phone the table becomes a list of cards, as the classic panel's inbounds do.
const isMobile = useIsMobile()

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// Their search box beside the buttons: name, port, protocol.
const search = ref('')

// The servers the tunnels are on, as tabs over the list: All, this server,
// then each node by the name it was given when it was added. Two nodes can
// each have a tunnel on 443 or one called wg0; the tab tells them apart.
const SERVER_KEY = 'wui.interfaces.server'
const server = ref(readServer())
function readServer() {
  try { return localStorage.getItem(SERVER_KEY) || 'all' } catch { return 'all' }
}
watch(server, (v) => {
  page.value = 1
  try { localStorage.setItem(SERVER_KEY, v) } catch { /* private window */ }
})
const servers = computed(() => {
  const by = new Map()
  for (const i of interfaces.value) {
    const id = String(i.nodeId)
    if (!by.has(id)) by.set(id, { id, local: !!i.nodeLocal, name: i.nodeName, up: i.nodeUp !== false, count: 0 })
    by.get(id).count++
  }
  return [...by.values()].sort((a, b) => (a.local !== b.local ? (a.local ? -1 : 1) : cmpText(a.name, b.name)))
})
const serverLabel = (s) => (s.local ? t('iface.server.local') : s.name || `#${s.id}`)
// A node removed while its tab was picked leaves nothing to show; go back to All.
watch(servers, (list) => {
  if (server.value !== 'all' && !list.some((s) => s.id === server.value)) server.value = 'all'
})
const onServer = computed(() =>
  server.value === 'all' ? interfaces.value : interfaces.value.filter((i) => String(i.nodeId) === server.value),
)
const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return onServer.value
  return onServer.value.filter((i) =>
    [i.name, String(i.listenPort), i.protocol, i.endpointHost, i.nodeName].some((v) => String(v || '').toLowerCase().includes(q)),
  )
})
const hasNodes = computed(() => interfaces.value.some((i) => i.nodeName && !i.nodeLocal))

// The order the headers set, and the pages: the size from the settings
// page, and no bar at all when everything fits on one page.
const sort = ref({ key: '', dir: '' })
const sorted = computed(() => sortRows(visible.value, sort.value))
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

// The dialogs, while open.
const textModal = ref(null) // { title, text, file }
const importOpen = ref(false)
const cloning = ref(null) // the tunnel being copied
const attach = ref(null) // { iface, list }
const move = ref(null) // { iface, kind }
const grouping = ref(null) // { iface, names }

// What an operator does to tunnels, and the menus that offer it.
const { pending, setEnabled, ask, runConfirmed, bulkDelete, pickRow, pickGeneral } = useInterfaceActions({
  interfaces, selected, busy, load,
  open: {
    text: (m) => (textModal.value = m),
    importer: () => (importOpen.value = true),
    clone: (i) => (cloning.value = i),
    attach: (i, list) => (attach.value = { iface: i, list }),
    move: (i, kind) => (move.value = { iface: i, kind }),
    group: (i, names) => (grouping.value = { iface: i, names }),
  },
})

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const fresh = await api.interfaces({ background: quiet })
    loadError.value = null
    interfaces.value = quiet ? mergeRows(interfaces.value, fresh, pending.value) : fresh
    const ids = new Set(interfaces.value.map((i) => i.id))
    selected.value = new Set([...selected.value].filter((id) => ids.has(id)))
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

// What these rows carry -- the traffic, the speed, how much of the address
// pool is spoken for -- changes constantly. An interface that has just been
// created also takes a moment to come up, and this is what shows it happening.
const showSkeleton = useDelayed(computed(() => loading.value && !interfaces.value.length))
useLive(load, {
  every: 5000,
  busy: () => !!formFor.value || !!detailFor.value || !!ask.value,
})

const totals = computed(() => totalsOf(interfaces.value))

const allSelected = computed(() => !!interfaces.value.length && selected.value.size === interfaces.value.length)
function toggleAll(checked) {
  selected.value = checked ? new Set(interfaces.value.map((i) => i.id)) : new Set()
}
function toggleOne(id, checked) {
  const next = new Set(selected.value)
  checked ? next.add(id) : next.delete(id)
  selected.value = next
}

async function submitForm(input) {
  try {
    if (formFor.value?.iface) {
      await api.updateInterface(formFor.value.iface.id, {
        enabled: input.enabled,
        name: input.name,
        listenPort: Number(input.listenPort),
        subnet: input.subnet,
        ...(input.protocol === 'wireguard' && input.mode ? { mode: input.mode } : {}),
        endpointHost: input.endpointHost,
        mtu: Number(input.mtu),
        dns: input.dns,
        natInterface: input.natInterface,
        // Only sent for OpenVPN, and only when it actually moved: the server
        // refuses the field on a WireGuard interface rather than ignoring it.
        ...(input.protocol === 'openvpn' && input.transport ? { transport: input.transport } : {}),
        // Only when it changed: a new key ends every session on the tunnel.
        ...(input.protocol === 'l2tp' && input.psk && input.psk !== (formFor.value.iface.l2tp?.psk || '') ? { psk: input.psk } : {}),
        // The same for routers without IPsec: a change restarts the tunnel.
        ...(input.protocol === 'l2tp' && input.l2tpPlain !== !!formFor.value.iface.l2tp?.allowPlain ? { l2tpPlain: input.l2tpPlain } : {}),
        ...(input.protocol === 'ikev2' ? { ikev2Modes: input.ikev2Modes } : {}),
        ...(input.protocol === 'openconnect' ? { openconnect: ocPayload(input.oc) } : {}),
        // Only when it moved: a new profile means new files for everyone.
        ...(input.mode === 'amnezia' && input.awg && JSON.stringify(input.awg) !== JSON.stringify(formFor.value.iface.awg || {}) ? { awg: input.awg } : {}),
      })
      notify(t('interface.updated'), 'success')
    } else {
      // transport is OpenVPN's alone and the server refuses it elsewhere, so
      // a WireGuard interface must not carry the form's default along with it.
      // The same for psk, which is L2TP's.
      const { transport, psk, l2tpPlain, ikev2Modes, awg, awgLevel, oc, xray, ...rest } = input
      if (xray) {
        // An inbound on a 3x-ui or Pasarguard node, made there as that
        // panel's own form would make it.
        await api.createInterface({
          name: input.name, nodeId: input.nodeId, protocol: 'xray', listenPort: Number(input.listenPort), xray,
        })
        notify(t('interface.created'), 'success')
        formFor.value = null
        await load()
        return
      }
      await api.createInterface({
        ...rest,
        listenPort: Number(input.listenPort),
        mtu: Number(input.mtu),
        ...(input.protocol === 'openvpn' && transport ? { transport } : {}),
        ...(input.protocol === 'l2tp' && psk ? { psk } : {}),
        ...(input.protocol === 'l2tp' && l2tpPlain ? { l2tpPlain: true } : {}),
        ...(input.protocol === 'ikev2' ? { ikev2Modes } : {}),
        ...(input.protocol === 'openconnect' ? { openconnect: ocPayload(oc) } : {}),
        // The profile the form shows, as drawn for this server and perhaps
        // changed: what is created is what the operator saw.
        ...(input.protocol === 'wireguard' && input.mode === 'amnezia' ? (awg ? { awg } : { awgLevel }) : {}),
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
  <InterfaceStats :totals="totals" />

  <!-- Their list Card: the title is a Space of Add Inbound, General Actions,
       the search, and -- with rows picked -- the count and a Delete. -->
  <div class="acard">
    <div class="acard-head">
      <div class="aspace">
        <button class="abtn primary" @click="formFor = {}">
          <AntIcon name="PlusOutlined" /><span v-if="!isMobile">{{ t('iface.menu.add') }}</span>
        </button>
        <MoreMenu :items="generalItems()" :compact="isMobile" button-class="abtn primary more-btn" icon="MenuOutlined" :label="t('iface.menu.general')" @pick="pickGeneral" />
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
      <div v-if="hasNodes" class="atabs-nav server-tabs"><div class="atabs-list" role="tablist">
        <button class="atab" role="tab" :class="{ active: server === 'all' }" :aria-selected="server === 'all'" @click="server = 'all'">
          <AntIcon name="BarsOutlined" /><span>{{ t('iface.server.all') }}</span><span class="atag count">{{ nf(interfaces.length) }}</span>
        </button>
        <button v-for="s in servers" :key="s.id" class="atab" role="tab" :class="{ active: server === s.id }" :aria-selected="server === s.id" @click="server = s.id">
          <AntIcon :name="s.local ? 'DesktopOutlined' : 'CloudServerOutlined'" /><span>{{ serverLabel(s) }}</span>
          <span class="atag count" :class="{ red: !s.up }">{{ nf(s.count) }}</span>
        </button>
      </div></div>
      <ErrorState v-if="loadError" :error="loadError" @retry="load()" />

      <PageSpin v-else-if="showSkeleton" />
      <div v-else-if="loading" class="empty"></div>

      <InterfaceCards v-else-if="isMobile" :rows="paged" :selected="selected" :all-selected="allSelected" :pending="pending"
        @toggle="toggleOne" @toggle-all="toggleAll" @enable="setEnabled" @pick="pickRow">
        <ul v-if="pageCount > 1" class="apagination">
          <li><button class="apage" :disabled="page <= 1" :aria-label="t('action.previous')" @click="page--">‹</button></li>
          <li v-for="n in pageNumbers" :key="n"><button class="apage" :class="{ active: n === page }" @click="page = n">{{ n }}</button></li>
          <li><button class="apage" :disabled="page >= pageCount" :aria-label="t('action.next')" @click="page++">›</button></li>
        </ul>
      </InterfaceCards>
      <InterfacesTable v-else :rows="paged" :selected="selected" :all-selected="allSelected" :pending="pending"
        @toggle="toggleOne" @toggle-all="toggleAll" @enable="setEnabled" @pick="pickRow"
        :has-nodes="hasNodes" :sort="sort" @edit="(i) => (formFor = { iface: i })" @sort="(key) => (sort = nextSort(sort, key))">
        <ul v-if="pageCount > 1" class="apagination">
          <li><button class="apage" :disabled="page <= 1" :aria-label="t('action.previous')" @click="page--">‹</button></li>
          <li v-for="n in pageNumbers" :key="n"><button class="apage" :class="{ active: n === page }" @click="page = n">{{ n }}</button></li>
          <li><button class="apage" :disabled="page >= pageCount" :aria-label="t('action.next')" @click="page++">›</button></li>
        </ul>
      </InterfacesTable>
    </div>
  </div>
  </div>

  <MoveDialog v-if="move" :iface="move.iface" :kind="move.kind" :interfaces="interfaces" @close="move = null" @done="load()" />
  <GroupDialog v-if="grouping" :iface="grouping.iface" :names="grouping.names" @close="grouping = null" @done="load()" />

  <TextDialog v-if="textModal" :title="textModal.title" :text="textModal.text" :file="textModal.file" @close="textModal = null" />
  <ImportDialog v-if="importOpen" @close="importOpen = false" @imported="load()" />
  <AttachDialog v-if="attach" :iface="attach.iface" :list="attach.list" @close="attach = null" @attached="load()" />

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

  <CloneDialog v-if="cloning" :from="cloning" :interfaces="interfaces" @close="cloning = null" @cloned="load()" />
</template>

<style>
/* Keyed on the page's root rather than scoped, so the components it is
   made of are styled as when their markup was the page's. */
/* Their column widths, exactly */
.inbounds .w-id { width: 60px; }
.inbounds .w-menu { width: 70px; }
.inbounds .w-enable { width: 80px; }
.inbounds .w-remark { width: 90px; }
.inbounds .w-node { width: 130px; }
.inbounds .server-tabs .atag.count { margin-inline-start: 6px; }
.inbounds .w-port { width: 80px; }
.inbounds .w-proto { width: 190px; }
.inbounds .w-clients { width: 200px; }
.inbounds .w-itraffic { width: 140px; }
.inbounds .w-speed { width: 216px; }
.inbounds .w-dur { width: 100px; }
.inbounds .atable th.center, .inbounds .atable td.center { text-align: center; }
.inbounds .atable th.right, .inbounds .atable td.right { text-align: end; }
.inbounds .action-buttons {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
}
.inbounds .card-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 24px 12px;
  color: var(--muted);
}
.inbounds .card-empty .anticon { margin-bottom: 8px; }

.inbounds .strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1px;
  background: var(--line-soft);
  margin-bottom: 16px;
  overflow: hidden;
}
.inbounds .strip-item {
  background: var(--surface);
  padding: 14px 18px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.inbounds .strip-label {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: var(--t-xs);
  color: var(--muted);
}
.inbounds .strip-value {
  font-size: var(--t-lg);
  font-weight: 600;
  line-height: 1.15;
}

.inbounds .actionbar {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.inbounds .selcount {
  color: var(--muted);
  margin-inline-start: 8px;
}

.inbounds .name {
  color: var(--ink);
  font-weight: 600;
}
.inbounds .tags {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
}
.inbounds .linkish {
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
.inbounds .linkish:hover {
  color: var(--accent-hover);
}
</style>
