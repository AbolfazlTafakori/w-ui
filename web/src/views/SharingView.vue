<script setup>
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { dateTime } from '../lib/format.js'
import { useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import PageSpin from '../components/PageSpin.vue'

// Credentials seen from several places at once. Ours, not the classic panel's, so it
// is laid out the way its list pages are: a summary card of three figures,
// then a card whose title holds the actions and the search, and a small
// table with sortable columns and a menu column at the front.
//
// Two tabs: what is true now, and the record -- every device that has been
// on the list or held off for going over its plan, kept after it leaves the
// list and after the customer is deleted.

const TAB_KEY = 'wui.sharing.tab'
const router = useRouter()
const tab = ref(readTab())
const reports = ref([])
const events = ref([])
const importState = ref(null)
const loading = ref(true)
const loadError = ref(null)
const importing = ref(false)
const rows = computed(() => (tab.value === 'history' ? events.value : reports.value))
const showWait = useDelayed(computed(() => loading.value && !rows.value.length))
let timer = null

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

function readTab() {
  try {
    return localStorage.getItem(TAB_KEY) === 'history' ? 'history' : 'now'
  } catch {
    return 'now'
  }
}
watch(tab, (v) => {
  try {
    localStorage.setItem(TAB_KEY, v)
  } catch {
    /* private window */
  }
  search.value = ''
  sort.value = { key: '', dir: '' }
  loading.value = true
  load()
})

onMounted(() => {
  load()
  // The window this looks at is ten minutes wide, so a slow refresh is enough
  // and a fast one would only add load for no new information.
  timer = setInterval(() => load(true), 30_000)
})
onBeforeUnmount(() => clearInterval(timer))

async function load(quiet = false) {
  try {
    if (tab.value === 'history') {
      const h = await api.get('/api/sharing/history', { background: quiet })
      events.value = h.events || []
      importState.value = h.import || null
    } else {
      reports.value = await api.get('/api/sharing', { background: quiet })
    }
    loadError.value = null
  } catch (e) {
    loadError.value = e
    if (!quiet) notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}

async function readLogAgain() {
  importing.value = true
  try {
    importState.value = await api.post('/api/sharing/history/import')
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    importing.value = false
  }
}

function since(iso) {
  if (!iso) return '—'
  const mins = Math.round((Date.now() - new Date(iso)) / 60000)
  if (mins < 60) return tn('sharing.minutesAgo', mins)
  const hours = Math.round(mins / 60)
  if (hours < 24) return tn('sharing.hoursAgo', hours)
  return tn('sharing.daysAgo', Math.round(hours / 24))
}

const addrsOf = (r) => (Array.isArray(r.addrs) ? r.addrs : String(r.addrs || '').split(',').filter(Boolean))

const totals = computed(() =>
  tab.value === 'history'
    ? {
        records: events.value.length,
        clients: new Set(events.value.map((r) => r.clientName)).size,
        held: events.value.filter((r) => r.kind === 'held').length,
      }
    : {
        devices: reports.value.length,
        clients: new Set(reports.value.map((r) => r.clientId)).size,
        addrs: reports.value.reduce((a, r) => a + addrsOf(r).length, 0),
      },
)

// What the record says about the time before it was kept.
const importNote = computed(() => {
  const s = importState.value
  if (!s) return null
  const n = (s.held || 0) + (s.shared || 0)
  switch (s.state) {
    case 'done':
      return { tone: 'info', text: n ? t('sharing.import.done', { n }) : t('sharing.import.doneNone'), retry: false }
    case 'denied':
      return { tone: 'warning', text: t('sharing.import.denied', { error: s.error || '' }), retry: true }
    case 'unavailable':
      return { tone: 'info', text: t('sharing.import.unavailable'), retry: false }
    default:
      return { tone: 'warning', text: t('sharing.import.failed', { error: s.error || '' }), retry: true }
  }
})

// Their search box beside the buttons: customer, device, address.
const search = ref('')
const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return rows.value
  return rows.value.filter((r) =>
    [r.clientName, r.deviceName, ...addrsOf(r)].some((v) => String(v || '').toLowerCase().includes(q)),
  )
})

// Their sortable headers: ascending, descending, then the server's order.
const sort = ref({ key: '', dir: '' })
const cmpText = (a, b) => String(a || '').localeCompare(String(b || ''), undefined, { numeric: true, sensitivity: 'base' })
const sorters = {
  client: (a, b) => cmpText(a.clientName, b.clientName),
  device: (a, b) => cmpText(a.deviceName, b.deviceName),
  addrs: (a, b) => addrsOf(a).length - addrsOf(b).length,
  since: (a, b) => new Date(a.since || 0) - new Date(b.since || 0),
  kind: (a, b) => cmpText(a.kind, b.kind),
  when: (a, b) => new Date(a.lastSeen || 0) - new Date(b.lastSeen || 0),
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
  const list = [...visible.value].sort(sorters[key])
  return dir === 'desc' ? list.reverse() : list
})

function open(r) {
  router.push(`/clients/${r.clientId}`)
}

// What a hold says: the plan's connections, and which server held it.
function detail(r) {
  const parts = []
  if (r.limit) parts.push(t('sharing.detail.limit', { n: r.limit }))
  if (r.place === 'here') parts.push(t('sharing.detail.here'))
  else if (r.place?.startsWith('node ')) parts.push(t('sharing.detail.node', { n: Number(r.place.slice(5)) || r.place.slice(5) }))
  return parts.length ? parts.join(store.locale === 'fa' ? '، ' : ', ') : '—'
}

// From and to, on one line when they are the same moment.
function when(r) {
  const from = dateTime(r.startedAt, store.locale)
  const to = dateTime(r.lastSeen, store.locale)
  return from === to ? from : `${from} – ${to}`
}
</script>

<template>
  <div class="antpage sharing">
    <!-- The summary Card: size="small", three Statistics. -->
    <div class="acard small summary-card">
      <div class="acard-body">
        <div v-if="tab === 'history'" class="arow">
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.records') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="ClockCircleOutlined" /></span><span>{{ nf(totals.records) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.clients') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(totals.clients) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.held') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="StopOutlined" /></span><span>{{ nf(totals.held) }}</span></div>
          </div>
        </div>
        <div v-else class="arow">
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.devices') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="EyeOutlined" /></span><span>{{ nf(totals.devices) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.clients') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(totals.clients) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.addrs') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="GlobalOutlined" /></span><span>{{ nf(totals.addrs) }}</span></div>
          </div>
        </div>
      </div>
    </div>

    <!-- The list Card: the title is a Space of Refresh and the search. -->
    <div class="acard">
      <div class="acard-head">
        <div class="aspace">
          <button class="abtn primary" @click="load()">
            <AntIcon name="ReloadOutlined" /><span>{{ t('common.refresh') }}</span>
          </button>
          <label class="ainput">
            <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
            <input v-model="search" type="text" :placeholder="t('iface.menu.search')" :aria-label="t('iface.menu.search')" />
            <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.cancel')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
          </label>
        </div>
      </div>

      <div class="acard-body">
        <div class="atabs-nav" role="tablist"><div class="atabs-list">
          <button class="atab" role="tab" :aria-selected="tab === 'now'" :class="{ active: tab === 'now' }" @click="tab = 'now'"><AntIcon name="EyeOutlined" /><span>{{ t('sharing.tab.now') }}</span></button>
          <button class="atab" role="tab" :aria-selected="tab === 'history'" :class="{ active: tab === 'history' }" @click="tab = 'history'"><AntIcon name="ClockCircleOutlined" /><span>{{ t('sharing.tab.history') }}</span></button>
        </div></div>

        <!-- Said plainly and up front, because acting on this without reading
             it means disconnecting paying customers who did nothing wrong. -->
        <div v-if="tab === 'now'" class="aalert warning with-desc" role="note">
          <AntIcon name="ExclamationCircleFilled" />
          <div class="aalert-body">
            <span class="aalert-title">{{ t('sharing.readFirst') }}</span>
            <span class="aalert-desc">{{ t('sharing.caveat') }}</span>
          </div>
        </div>
        <template v-else>
          <div class="aalert info with-desc" role="note">
            <AntIcon name="InfoCircleFilled" />
            <div class="aalert-body"><span class="aalert-desc">{{ t('sharing.historyHint') }}</span></div>
          </div>
          <div v-if="importNote" class="aalert with-desc import-note" :class="importNote.tone" role="status">
            <AntIcon :name="importNote.tone === 'warning' ? 'ExclamationCircleFilled' : 'InfoCircleFilled'" />
            <div class="aalert-body"><span class="aalert-desc">{{ importNote.text }}</span></div>
            <button v-if="importNote.retry" class="abtn sm" :disabled="importing" @click="readLogAgain">
              <AntIcon :name="importing ? 'LoadingOutlined' : 'ReloadOutlined'" :class="{ spin: importing }" />
              <span>{{ importing ? t('sharing.import.running') : t('sharing.import.retry') }}</span>
            </button>
          </div>
        </template>

        <ErrorState v-if="loadError && !rows.length" :error="loadError" @retry="load()" />
        <PageSpin v-else-if="showWait" />
        <div v-else-if="loading" class="empty"></div>

        <!-- Now: what is on the list at this moment. -->
        <div v-else-if="tab === 'now'" class="atable-wrap">
          <table class="atable small" style="min-width: 760px">
            <thead>
              <tr>
                <th class="w-menu center">{{ t('iface.col.menu') }}</th>
                <th class="w-client sortable" :class="sortCls('client')" @click="sortBy('client')">
                  <div class="sorters"><span class="title">{{ t('sharing.client') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-device sortable" :class="sortCls('device')" @click="sortBy('device')">
                  <div class="sorters"><span class="title">{{ t('sharing.device') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="sortable" :class="sortCls('addrs')" @click="sortBy('addrs')">
                  <div class="sorters"><span class="title">{{ t('sharing.addresses') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-since center sortable" :class="sortCls('since')" @click="sortBy('since')">
                  <div class="sorters"><span class="title">{{ t('sharing.firstSeen') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!sorted.length" class="empty-row">
                <td colspan="5">
                  <div class="card-empty">
                    <AntIcon name="CheckCircleFilled" :size="32" />
                    <div>{{ t('sharing.none') }}</div>
                    <div class="muted">{{ t('sharing.noneHint') }}</div>
                  </div>
                </td>
              </tr>
              <tr v-for="r in sorted" :key="r.accountId">
                <td class="center">
                  <div class="action-buttons">
                    <button class="abtn text sm" :title="t('sharing.review')" :aria-label="t('sharing.review')" @click="open(r)"><AntIcon name="EyeOutlined" /></button>
                  </div>
                </td>
                <td><RouterLink :to="`/clients/${r.clientId}`" class="cell-link">{{ r.clientName }}</RouterLink></td>
                <td class="muted">{{ r.deviceName }}</td>
                <td>
                  <div class="protocol-tags">
                    <span v-for="a in addrsOf(r)" :key="a" class="atag blue ltr" style="margin: 0">{{ a }}</span>
                  </div>
                </td>
                <td class="center"><span class="atag ltr" style="margin: 0">{{ since(r.since) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- History: every time, kept. -->
        <div v-else class="atable-wrap">
          <table class="atable small history-table">
            <thead>
              <tr>
                <th class="w-kind sortable" :class="sortCls('kind')" @click="sortBy('kind')">
                  <div class="sorters"><span class="title">{{ t('sharing.col.kind') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-client sortable" :class="sortCls('client')" @click="sortBy('client')">
                  <div class="sorters"><span class="title">{{ t('sharing.client') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-device sortable" :class="sortCls('device')" @click="sortBy('device')">
                  <div class="sorters"><span class="title">{{ t('sharing.device') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-addrs sortable" :class="sortCls('addrs')" @click="sortBy('addrs')">
                  <div class="sorters"><span class="title">{{ t('sharing.addresses') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-detail">{{ t('sharing.col.detail') }}</th>
                <th class="w-when sortable" :class="sortCls('when')" @click="sortBy('when')">
                  <div class="sorters"><span class="title">{{ t('sharing.col.when') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!sorted.length" class="empty-row">
                <td colspan="6">
                  <div class="card-empty">
                    <AntIcon name="CheckCircleFilled" :size="32" />
                    <div>{{ t('sharing.historyNone') }}</div>
                  </div>
                </td>
              </tr>
              <tr v-for="r in sorted" :key="r.id">
                <td>
                  <span class="atag" :class="r.kind === 'held' ? 'red' : 'orange'" style="margin: 0">{{ t(`sharing.kind.${r.kind}`) }}</span>
                  <span v-if="r.source === 'log'" class="atag ltr-auto from-log">{{ t('sharing.fromLog') }}</span>
                </td>
                <td>
                  <RouterLink v-if="!r.deleted" :to="`/clients/${r.clientId}`" class="cell-link">{{ r.clientName }}</RouterLink>
                  <template v-else><span>{{ r.clientName }}</span> <span class="atag deleted">{{ t('sharing.deleted') }}</span></template>
                </td>
                <td class="muted">{{ r.deviceName }}</td>
                <td>
                  <div class="protocol-tags">
                    <span v-for="a in addrsOf(r)" :key="a" class="atag blue ltr" style="margin: 0">{{ a }}</span>
                    <span v-if="!addrsOf(r).length" class="muted">—</span>
                  </div>
                </td>
                <td class="muted">{{ detail(r) }}</td>
                <td class="when">{{ when(r) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.aalert.with-desc { margin-bottom: 10px; }
.import-note { align-items: center; }
.import-note .abtn { margin-inline-start: auto; flex-shrink: 0; }
.atabs-nav { margin-bottom: 12px; }
.w-menu { width: 70px; }
.w-client { width: 200px; }
.w-device { width: 160px; }
.w-since { width: 120px; }
.w-kind { width: 170px; }
.w-detail { width: 150px; }
.w-when { width: 230px; }
.w-addrs { width: 250px; }
/* Every column has its width; the table is their sum, and scrolls. */
.history-table { min-width: 1160px; table-layout: fixed; }
.history-table td { overflow-wrap: anywhere; }
.when { font-size: 12px; white-space: nowrap; }
.from-log, .deleted { margin: 0 0 0 4px; font-size: 11px; color: var(--muted); }
.atable th.center, .atable td.center { text-align: center; }
.action-buttons { display: flex; align-items: center; justify-content: center; gap: 4px; }
.card-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 24px 12px;
  color: var(--muted);
}
.card-empty .anticon { margin-bottom: 8px; color: var(--ok); }
.cell-link { color: var(--ink); text-decoration: none; }
.cell-link:hover { color: var(--accent-text); }
.muted { color: var(--faint); }
</style>
