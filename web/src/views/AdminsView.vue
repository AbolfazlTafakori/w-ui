<script setup>
// The people who sell this panel on.
//
// The owner's page, and only the owner's -- the route guard keeps it off
// everyone else's menu and every endpoint behind it refuses them again. The
// owner is not listed on it: this is a page about the people they sell to,
// and a row for themselves with every column empty is a row to skip past.
//
// Laid out as the customer list is, because a reseller is managed the way a
// customer is: a summary across the top, then a row each with how much of
// what they bought is left -- customers, traffic, days -- in the same colours
// the customer list uses, so "orange" means the same thing on both pages.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes, dateTime, unitToBytes } from '../lib/format.js'
import { useIsMobile } from '../lib/mobile.js'
import AntIcon from '../components/AntIcon.vue'
import Toggle from '../components/Toggle.vue'
import ErrorState from '../components/ErrorState.vue'
import PageSpin from '../components/PageSpin.vue'
import ResellerForm from '../components/ResellerForm.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MultiSelect from '../components/MultiSelect.vue'

const isMobile = useIsMobile()
const route = useRoute()
const router = useRouter()

const operators = ref([])
const servers = ref([])
const groupNames = ref([])
const loading = ref(true)
const loadError = ref(null)
const pending = ref(new Set())

const formFor = ref(null) // {} to add, { admin } to edit
const removing = ref(null)
const busy = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load({ quiet = false } = {}) {
  if (!quiet) loading.value = true
  loadError.value = null
  try {
    const [list, ifaces, names] = await Promise.all([
      api.admins({ background: quiet }),
      api.interfaces({ background: quiet }),
      api.groupNames().catch(() => []),
    ])
    operators.value = list.items || []
    servers.value = ifaces || []
    groupNames.value = names || []
  } catch (err) {
    loadError.value = err
  } finally {
    loading.value = false
  }
}
onMounted(load)

// Everybody but the owner.
const rows = computed(() => operators.value.filter((a) => a.role !== 'owner'))
const resellers = computed(() => rows.value.filter((a) => a.role === 'reseller'))

// --- What is left, per reseller. -----------------------------------------
const DAY = 86400e3
const WARN_DAYS = 7
const WARN_PERCENT = 85

function usedPercent(a) {
  return a.quotaBytes ? Math.min(100, Math.round((a.usedBytes / a.quotaBytes) * 100)) : null
}
function daysLeft(a) {
  if (!a.expiresAt) return null
  return Math.ceil((new Date(a.expiresAt).getTime() - Date.now()) / DAY)
}

// One word for where a reseller stands, first reason first: the owner's
// switch, then the date, then the traffic, then a term not yet started.
function standing(a) {
  if (!a.enabled) return { key: 'off', color: '', label: t('admins.switchedOff') }
  if (a.role !== 'reseller') return { key: 'active', color: 'green', label: t('status.active') }
  const d = daysLeft(a)
  if (d !== null && d <= 0) return { key: 'ended', color: 'red', label: t('admins.termEnded') }
  if (a.quotaBytes && a.usedBytes >= a.quotaBytes) return { key: 'spent', color: 'red', label: t('admins.allowanceSpent') }
  if (!a.expiresAt && a.durationDays > 0) return { key: 'hold', color: 'blue', label: t('status.onHold') }
  if ((d !== null && d <= WARN_DAYS) || (usedPercent(a) ?? 0) >= WARN_PERCENT) {
    return { key: 'soon', color: 'orange', label: t('admins.endingSoon') }
  }
  return { key: 'active', color: 'green', label: t('status.active') }
}

function trafficLeftTag(a) {
  if (!a.quotaBytes) return { color: 'purple', label: '∞' }
  const p = usedPercent(a)
  const label = bytes(Math.max(0, a.quotaBytes - a.usedBytes), store.locale)
  if (p >= 100) return { color: 'red', label }
  if (p >= WARN_PERCENT) return { color: 'orange', label }
  return { color: 'green', label }
}
function barColor(a) {
  if (!a.enabled) return 'var(--faint)'
  if (!a.quotaBytes) return 'rgba(114, 46, 209, 0.35)'
  const p = usedPercent(a) ?? 0
  if (p >= 100) return 'var(--bad)'
  if (p >= WARN_PERCENT) return 'var(--warn)'
  return 'var(--ok)'
}
function termTag(a) {
  if (!a.expiresAt && a.durationDays > 0) {
    return { color: 'blue', label: t('admins.daysOnHold', { n: nf(a.durationDays) }), title: t('admins.onHoldHint') }
  }
  const d = daysLeft(a)
  if (d === null) return { color: 'purple', label: '∞', title: t('admins.noEnd') }
  const title = dateTime(a.expiresAt, store.locale)
  if (d <= 0) return { color: 'red', label: t('admins.termEnded'), title }
  return { color: d <= WARN_DAYS ? 'orange' : 'green', label: t('admins.daysLeft', { n: nf(d) }), title }
}
function customersTag(a) {
  if (!a.clientLimit) return { color: '', label: `${nf(a.clients)} / ∞` }
  const full = a.clients >= a.clientLimit
  return { color: full ? 'orange' : '', label: `${nf(a.clients)} / ${nf(a.clientLimit)}` }
}

function serverName(id) {
  return servers.value.find((s) => s.id === id)?.name || `#${id}`
}
function serverProto(id) {
  return servers.value.find((s) => s.id === id)?.protocol
}
const CHIP_LIMIT = 2

// --- Finding one. -----------------------------------------------------------
//
// The customer list's search, filters and sort, over a list that is small
// enough to hold in the page: filtering here answers as the operator types,
// with no request per keystroke. Kept in the address, so a reload or a link
// lands on the same view.
const STATUS_FILTERS = ['active', 'hold', 'soon', 'stopped']
const SORTS = ['name', 'customers', 'traffic', 'timeLeft', 'newest']

const qs = (k) => (typeof route.query[k] === 'string' ? route.query[k] : '')
const search = ref(qs('q'))
const statusFilter = ref(STATUS_FILTERS.includes(qs('status')) ? qs('status') : '')
const serverFilter = ref(Number(qs('server')) || '')
const sort = ref(SORTS.includes(qs('sort')) ? qs('sort') : 'name')

watch([search, statusFilter, serverFilter, sort], () => {
  const query = {}
  if (search.value.trim()) query.q = search.value.trim()
  if (statusFilter.value) query.status = statusFilter.value
  if (serverFilter.value) query.server = String(serverFilter.value)
  if (sort.value !== 'name') query.sort = sort.value
  router.replace({ query })
})

const filtering = computed(() => !!(search.value.trim() || statusFilter.value || serverFilter.value))
function clearFilters() {
  search.value = ''
  statusFilter.value = ''
  serverFilter.value = ''
}

// Which of the four a reseller falls in. An administrator is active or
// switched off and nothing else: they have no ceiling to run into.
function bucket(a) {
  const k = standing(a).key
  if (k === 'off' || k === 'ended' || k === 'spent') return 'stopped'
  return k
}

const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  let list = rows.value.filter((a) => {
    if (statusFilter.value && bucket(a) !== statusFilter.value) return false
    if (serverFilter.value && !(a.interfaceIds || []).includes(serverFilter.value)) return false
    if (!q) return true
    return [a.username, a.note, a.groupName].some((v) => (v || '').toLowerCase().includes(q))
  })
  // Unlimited sorts last on "time left" and first on nothing else: it is the
  // one that will never need renewing.
  const left = (a) => (a.expiresAt ? new Date(a.expiresAt).getTime() : a.durationDays > 0 ? Date.now() + a.durationDays * DAY : Infinity)
  const by = {
    name: (x, y) => x.username.localeCompare(y.username),
    customers: (x, y) => (y.clients || 0) - (x.clients || 0),
    traffic: (x, y) => (y.usedBytes || 0) - (x.usedBytes || 0),
    timeLeft: (x, y) => left(x) - left(y),
    newest: (x, y) => new Date(y.createdAt || 0) - new Date(x.createdAt || 0),
  }[sort.value]
  return [...list].sort((x, y) => by(x, y) || x.username.localeCompare(y.username))
})

// A summary tile is also the way to its list.
function showBucket(k) {
  statusFilter.value = statusFilter.value === k ? '' : k
}

// --- Across the top. -------------------------------------------------------
const summary = computed(() => {
  const list = resellers.value
  const by = (key) => list.filter((a) => standing(a).key === key).length
  return {
    total: rows.value.length,
    active: by('active') + by('hold'),
    soon: by('soon'),
    stopped: by('off') + by('ended') + by('spent'),
    customers: list.reduce((n, a) => n + (a.clients || 0), 0),
    traffic: list.reduce((n, a) => n + (a.usedBytes || 0), 0),
  }
})

// --- Doing things to one. ---------------------------------------------------
function markPending(id, on) {
  const next = new Set(pending.value)
  if (on) next.add(id)
  else next.delete(id)
  pending.value = next
}

async function setEnabled(a, on) {
  const was = a.enabled
  a.enabled = on
  markPending(a.id, true)
  try {
    await api.updateAdmin(a.id, { enabled: on })
  } catch (err) {
    a.enabled = was
    notify(err.message, 'error')
  } finally {
    markPending(a.id, false)
  }
}

async function resetUsage(a) {
  if (!window.confirm(t('admins.resetConfirm', { name: a.username }))) return
  markPending(a.id, true)
  try {
    await api.resetAdminUsage(a.id)
    a.usedBytes = 0
    notify(t('admins.usageReset'), 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    markPending(a.id, false)
  }
}

// Removing one or a selection. `removing` holds what is being removed: the
// ids, how to name them, and how many customers they hold between them --
// the dialog asks the same question for one reseller or forty.
function askRemove(list) {
  removing.value = {
    ids: list.map((a) => a.id),
    name: list.length === 1 ? list[0].username : t('admins.bulk.nResellers', { n: nf(list.length) }),
    customers: list.reduce((n, a) => n + (a.clients || 0), 0),
  }
}

async function remove(mode) {
  const r = removing.value
  if (!r) return
  busy.value = true
  try {
    if (r.ids.length === 1) {
      await api.deleteAdmin(r.ids[0], mode)
      notify(t('admins.saved'), 'success')
    } else {
      report(await api.bulkAdmins({ action: 'delete', mode, ids: r.ids }))
    }
    removing.value = null
    selected.value = new Set()
    await load({ quiet: true })
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

// --- A selection. -----------------------------------------------------------
//
// The customer list's selection, over resellers: a box on every row, one in
// the header for everything shown, and the actions that apply to all of them
// in the toolbar. Only what is on screen can be selected, and narrowing the
// list drops whatever it hides -- an action must never reach a reseller the
// owner can no longer see.
const selected = ref(new Set())
watch(shown, (list) => {
  const visible = new Set(list.map((a) => a.id))
  const kept = [...selected.value].filter((id) => visible.has(id))
  if (kept.length !== selected.value.size) selected.value = new Set(kept)
})
const allSelected = computed(() => shown.value.length > 0 && shown.value.every((a) => selected.value.has(a.id)))
const someSelected = computed(() => selected.value.size > 0 && !allSelected.value)
function toggleAll(on) {
  selected.value = on ? new Set(shown.value.map((a) => a.id)) : new Set()
}
function toggleOne(id, on) {
  const next = new Set(selected.value)
  if (on) next.add(id)
  else next.delete(id)
  selected.value = next
}
const selectedRows = computed(() => rows.value.filter((a) => selected.value.has(a.id)))

// The actions, in the order the customer list has them. Those that need a
// value -- how many days, how much traffic, which servers -- open a small
// dialog; the rest ask first when they take service away or cannot be undone.
const moreOpen = ref(null)
function openMore(e) {
  if (moreOpen.value) {
    moreOpen.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  moreOpen.value = { rect: r, x: r.left, y: r.bottom + 4 }
}
function closeMore(e) {
  if (moreOpen.value && !e.target.closest?.('.amenu') && !e.target.closest?.('.more-btn')) moreOpen.value = null
}
function onKey(e) {
  if (e.key === 'Escape') moreOpen.value = null
}
onMounted(() => {
  window.addEventListener('click', closeMore, true)
  window.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  window.removeEventListener('click', closeMore, true)
  window.removeEventListener('keydown', onKey)
})

const bulkItems = computed(() => [
  { key: 'enable', label: t('admins.switchOn'), icon: 'CheckCircleOutlined' },
  { key: 'disable', label: t('admins.switchOff'), icon: 'StopOutlined', danger: true },
  { divider: true },
  { key: 'extend', label: t('admins.bulk.extend'), icon: 'ClockCircleOutlined' },
  { key: 'resetUsage', label: t('admins.resetUsage'), icon: 'RetweetOutlined' },
  { key: 'setQuota', label: t('admins.bulk.setQuota'), icon: 'BarChartOutlined' },
  { key: 'setClientLimit', label: t('admins.bulk.setLimit'), icon: 'TeamOutlined' },
  { divider: true },
  { key: 'addServers', label: t('admins.bulk.addServers'), icon: 'UsergroupAddOutlined' },
  { key: 'removeServers', label: t('admins.bulk.removeServers'), icon: 'UsergroupDeleteOutlined', danger: true },
])

// What the server answered, said once: how many changed, and every reseller
// that was not with the reason -- a count alone leaves the owner to work out
// which of forty is still on.
function report(res) {
  const failed = Object.entries(res?.failures || {})
  if (failed.length) {
    notify(`${t('admins.bulk.done', { n: nf(res.changed || 0) })}\n${failed.map(([who, why]) => `${who}: ${why}`).join('\n')}`, 'error')
  } else {
    notify(t('admins.bulk.done', { n: nf(res?.changed || 0) }), 'success')
  }
}

async function runBulk(input) {
  busy.value = true
  try {
    report(await api.bulkAdmins({ ...input, ids: [...selected.value] }))
    selected.value = new Set()
    await load({ quiet: true })
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

const ask = ref(null)
async function runConfirmed() {
  const spec = ask.value
  if (!spec) return
  try {
    await spec.run()
  } finally {
    ask.value = null
  }
}

// The dialog for the actions that need a value.
const bulkDialog = ref(null) // { kind, days, quota, quotaUnit, limit, interfaceIds }
const serverOptions = computed(() =>
  servers.value.map((i) => ({
    value: i.id,
    label: i.name,
    tags: [{ text: t(`protocol.${i.protocol}`), kind: 'proto' }, ...(i.mode === 'amnezia' ? [{ text: 'AmneziaWG' }] : [])],
  })),
)
const bulkError = ref('')

function pickBulk(key) {
  moreOpen.value = null
  const n = selected.value.size
  const subject = t('admins.bulk.nResellers', { n: nf(n) })
  const customers = selectedRows.value.reduce((sum, a) => sum + (a.clients || 0), 0)
  bulkError.value = ''
  if (key === 'enable') return runBulk({ action: 'enable' })
  if (key === 'disable') {
    ask.value = {
      title: t('admins.switchOff'),
      body: t('admins.bulk.disableBody'),
      subject,
      consequences: [t('admins.bulk.disableCustomers', { n: nf(customers) }), t('admins.bulk.disableSignOut')],
      confirmLabel: t('admins.switchOff'),
      run: () => runBulk({ action: 'disable' }),
    }
    return
  }
  if (key === 'resetUsage') {
    ask.value = {
      title: t('admins.resetUsage'),
      body: t('admins.bulk.resetBody'),
      subject,
      confirmLabel: t('admins.resetShort'),
      danger: false,
      run: () => runBulk({ action: 'resetUsage' }),
    }
    return
  }
  bulkDialog.value = { kind: key, days: 30, quota: '', quotaUnit: 'GB', limit: '', interfaceIds: [] }
}

async function submitBulkDialog() {
  const d = bulkDialog.value
  if (!d) return
  bulkError.value = ''
  const input = { action: d.kind }
  if (d.kind === 'extend') {
    const days = Number(d.days)
    if (!Number.isInteger(days) || days < 1 || days > 3650) {
      bulkError.value = t('admins.daysRequired')
      return
    }
    input.days = days
  } else if (d.kind === 'setQuota') {
    input.quotaBytes = unitToBytes(d.quota, d.quotaUnit)
  } else if (d.kind === 'setClientLimit') {
    input.clientLimit = Math.max(0, Math.floor(Number(d.limit) || 0))
  } else if (d.kind === 'addServers' || d.kind === 'removeServers') {
    if (!d.interfaceIds.length) {
      bulkError.value = t('client.pickAServer')
      return
    }
    input.interfaceIds = d.interfaceIds
  }
  bulkDialog.value = null
  await runBulk(input)
}

const bulkTitle = computed(() => {
  const k = bulkDialog.value?.kind
  return {
    extend: t('admins.bulk.extend'),
    setQuota: t('admins.bulk.setQuota'),
    setClientLimit: t('admins.bulk.setLimit'),
    addServers: t('admins.bulk.addServers'),
    removeServers: t('admins.bulk.removeServers'),
  }[k] || ''
})

async function saved() {
  formFor.value = null
  notify(t('admins.saved'), 'success')
  await load({ quiet: true })
}
</script>

<template>
  <div class="antpage resellers">
    <ErrorState v-if="loadError" :error="loadError" @retry="load" />
    <PageSpin v-else-if="loading" />

    <template v-else>
      <!-- The classic summary card, over resellers rather than customers. -->
      <div class="acard small summary-card">
        <div class="acard-body">
          <div class="arow six">
            <div class="acol">
              <div class="stat-title">{{ t('nav.admins') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="ShopOutlined" /></span><span>{{ nf(summary.total) }}</span></div>
            </div>
            <div class="acol">
              <button type="button" class="stat-button" :class="{ on: statusFilter === 'active' }" :aria-pressed="statusFilter === 'active'" @click="showBucket('active')">
                <span class="stat-title">{{ t('status.active') }}</span>
                <span class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-green"></i></span><span>{{ nf(summary.active) }}</span></span>
              </button>
            </div>
            <div class="acol">
              <button type="button" class="stat-button" :class="{ on: statusFilter === 'soon' }" :aria-pressed="statusFilter === 'soon'" @click="showBucket('soon')">
                <span class="stat-title">{{ t('admins.endingSoon') }}</span>
                <span class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-orange"></i></span><span>{{ nf(summary.soon) }}</span></span>
              </button>
            </div>
            <div class="acol">
              <button type="button" class="stat-button" :class="{ on: statusFilter === 'stopped' }" :aria-pressed="statusFilter === 'stopped'" @click="showBucket('stopped')">
                <span class="stat-title">{{ t('admins.stopped') }}</span>
                <span class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-red"></i></span><span>{{ nf(summary.stopped) }}</span></span>
              </button>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('admins.theirCustomers') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(summary.customers) }}</span></div>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('admins.trafficUsed') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="BarChartOutlined" /></span><span>{{ bytes(summary.traffic, store.locale) }}</span></div>
            </div>
          </div>
        </div>
      </div>

      <div class="acard small">
        <div class="acard-head">
          <div class="card-toolbar">
            <button v-if="!selected.size" class="abtn primary" :disabled="!servers.length" :title="servers.length ? '' : t('interface.noneYet')" @click="formFor = {}">
              <AntIcon name="PlusOutlined" /><span>{{ t('admins.add') }}</span>
            </button>
            <template v-else>
              <span class="atag blue closable" style="padding: 4px 8px; font-size: 13px">
                {{ t('client.menu.selectedCount').replace('{count}', nf(selected.size)) }}
                <button type="button" class="atag-close" :aria-label="t('action.cancel')" @click="selected = new Set()"><AntIcon name="CloseOutlined" /></button>
              </span>
              <button class="abtn more-btn" :aria-expanded="!!moreOpen" aria-haspopup="menu" :disabled="busy" @click="openMore">
                <AntIcon name="MoreOutlined" /><span v-if="!isMobile">{{ t('outbound.more') }}</span>
              </button>
              <button class="abtn danger" style="margin-inline-start: auto" :disabled="busy" @click="askRemove(selectedRows)">
                <AntIcon name="DeleteOutlined" /><span v-if="!isMobile">{{ t('action.delete') }}</span>
              </button>
            </template>
          </div>
        </div>

        <div class="acard-body">
          <div v-if="rows.length" class="filter-bar">
            <label class="ainput" :class="{ small: isMobile }" :style="isMobile ? 'width: 100%' : 'max-width: 320px; width: 100%'">
              <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
              <input v-model="search" type="search" :placeholder="t('admins.searchPlaceholder')" :aria-label="t('action.search')" />
              <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.clear')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
            </label>
            <div class="aselect" :class="{ small: isMobile }">
              <select v-model="statusFilter" :aria-label="t('admins.status')">
                <option value="">{{ t('admins.filter.anyStatus') }}</option>
                <option value="active">{{ t('status.active') }}</option>
                <option value="hold">{{ t('status.onHold') }}</option>
                <option value="soon">{{ t('admins.endingSoon') }}</option>
                <option value="stopped">{{ t('admins.stopped') }}</option>
              </select>
            </div>
            <div class="aselect" :class="{ small: isMobile }">
              <select v-model.number="serverFilter" :aria-label="t('admins.servers')">
                <option value="">{{ t('admins.filter.anyServer') }}</option>
                <option v-for="sv in servers" :key="sv.id" :value="sv.id">{{ sv.name }}</option>
              </select>
            </div>
            <div class="aselect sort-select" :class="{ small: isMobile }">
              <select v-model="sort" :aria-label="t('client.sort.label')">
                <option value="name">{{ t('admins.sort.name') }}</option>
                <option value="customers">{{ t('admins.sort.customers') }}</option>
                <option value="traffic">{{ t('admins.sort.traffic') }}</option>
                <option value="timeLeft">{{ t('admins.sort.timeLeft') }}</option>
                <option value="newest">{{ t('admins.sort.newest') }}</option>
              </select>
              <span class="aselect-suffix"><AntIcon name="SortAscendingOutlined" /></span>
            </div>
            <button v-if="filtering" type="button" class="abtn" @click="clearFilters">{{ t('client.menu.clearAllFilters') }}</button>
            <span v-if="filtering" class="filter-count" aria-live="polite">
              {{ t('client.menu.showingCount').replace('{shown}', nf(shown.length)).replace('{total}', nf(rows.length)) }}
            </span>
          </div>

          <div v-if="!rows.length" class="resellers-empty">
            <AntIcon name="ShopOutlined" :size="32" />
            <div>{{ t('admins.none') }}</div>
          </div>
          <div v-else-if="!shown.length" class="resellers-empty">
            <AntIcon name="SearchOutlined" :size="32" />
            <div>{{ t('admins.noMatch') }}</div>
            <button type="button" class="abtn" style="margin-top: 12px" @click="clearFilters">{{ t('client.menu.clearAllFilters') }}</button>
          </div>

          <!-- A phone gets a card each: the same figures, stacked. -->
          <div v-else-if="isMobile" class="rcards">
            <label class="acheckbox card-bulk-bar">
              <input type="checkbox" class="acheck" :checked="allSelected" :indeterminate="someSelected" @change="toggleAll($event.target.checked)" />
              <span>{{ t('action.selectAll') }}</span>
            </label>
            <div v-for="a in shown" :key="a.id" class="rcard" :class="{ off: !a.enabled, picked: selected.has(a.id) }">
              <div class="rcard-head">
                <input type="checkbox" class="acheck" :checked="selected.has(a.id)" :aria-label="a.username" @change="toggleOne(a.id, $event.target.checked)" />
                <div class="rcard-name">
                  <span class="name">{{ a.username }}</span>
                  <span class="atag" dir="auto" :class="standing(a).color" style="margin: 0">{{ standing(a).label }}</span>
                </div>
                <Toggle :model-value="a.enabled" :label="a.username" small :loading="pending.has(a.id)" @update:model-value="(v) => setEnabled(a, v)" />
              </div>
              <div v-if="a.note" class="rcard-note">{{ a.note }}</div>
              <dl v-if="a.role === 'reseller'" class="rcard-stats">
                <div><dt>{{ t('admins.customers') }}</dt><dd><span class="atag ltr" :class="customersTag(a).color" style="margin: 0">{{ customersTag(a).label }}</span></dd></div>
                <div><dt>{{ t('admins.trafficLeft') }}</dt><dd><span class="atag ltr" :class="trafficLeftTag(a).color" style="margin: 0">{{ trafficLeftTag(a).label }}</span></dd></div>
                <div><dt>{{ t('admins.timeLeft') }}</dt><dd><span class="atag" dir="auto" :class="termTag(a).color" :title="termTag(a).title" style="margin: 0">{{ termTag(a).label }}</span></dd></div>
              </dl>
              <div v-else class="rcard-note"><span class="atag geekblue" style="margin: 0">{{ t('admins.role.admin') }}</span></div>
              <div class="rcard-actions">
                <button class="abtn text sm" :aria-label="t('action.edit')" @click="formFor = { admin: a }"><AntIcon name="EditOutlined" /><span>{{ t('action.edit') }}</span></button>
                <button v-if="a.role === 'reseller' && a.quotaBytes" class="abtn text sm" :disabled="pending.has(a.id)" @click="resetUsage(a)"><AntIcon name="RetweetOutlined" /><span>{{ t('admins.resetShort') }}</span></button>
                <button class="abtn text sm danger" @click="askRemove([a])"><AntIcon name="DeleteOutlined" /><span>{{ t('action.delete') }}</span></button>
              </div>
            </div>
          </div>

          <div v-else class="atable-wrap" style="margin-top: 0">
            <table class="atable small" style="min-width: 1080px">
              <thead>
                <tr>
                  <th class="sel"><input type="checkbox" class="acheck" :checked="allSelected" :indeterminate="someSelected" :aria-label="t('action.selectAll')" @change="toggleAll($event.target.checked)" /></th>
                  <th style="width: 110px">{{ t('table.actions') }}</th>
                  <th style="width: 70px">{{ t('table.enabled') }}</th>
                  <th style="width: 200px">{{ t('admins.reseller') }}</th>
                  <th style="width: 110px">{{ t('admins.status') }}</th>
                  <th style="width: 100px">{{ t('admins.customers') }}</th>
                  <th style="width: 260px">{{ t('client.traffic') }}</th>
                  <th style="width: 110px">{{ t('admins.trafficLeft') }}</th>
                  <th style="width: 110px">{{ t('admins.timeLeft') }}</th>
                  <th>{{ t('admins.servers') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="a in shown" :key="a.id" :class="{ off: !a.enabled, picked: selected.has(a.id) }">
                  <td class="sel keep"><input type="checkbox" class="acheck" :checked="selected.has(a.id)" :aria-label="a.username" @change="toggleOne(a.id, $event.target.checked)" /></td>
                  <td class="keep">
                    <div class="aspace" style="gap: 4px; flex-wrap: nowrap">
                      <button class="abtn text sm" :title="t('action.edit')" :aria-label="t('action.edit')" @click="formFor = { admin: a }"><AntIcon name="EditOutlined" /></button>
                      <button v-if="a.role === 'reseller' && a.quotaBytes" class="abtn text sm" :title="t('admins.resetUsage')" :aria-label="t('admins.resetUsage')" :disabled="pending.has(a.id)" @click="resetUsage(a)"><AntIcon name="RetweetOutlined" /></button>
                      <button class="abtn text sm danger" :title="t('action.delete')" :aria-label="t('action.delete')" @click="askRemove([a])"><AntIcon name="DeleteOutlined" /></button>
                    </div>
                  </td>
                  <td class="keep">
                    <Toggle :model-value="a.enabled" :label="a.username" small :loading="pending.has(a.id)" @update:model-value="(v) => setEnabled(a, v)" />
                  </td>
                  <td>
                    <div class="who">
                      <span class="name ltr">{{ a.username }}</span>
                      <span v-if="a.role === 'admin'" class="sub"><span class="atag geekblue" style="margin: 0">{{ t('admins.role.admin') }}</span></span>
                      <span v-if="a.note" class="sub" :title="a.note">{{ a.note }}</span>
                      <span v-if="a.groupName" class="sub" :title="t('admins.groupHint')"><AntIcon name="TagsOutlined" /> {{ a.groupName }}</span>
                    </div>
                  </td>
                  <td><span class="atag" dir="auto" :class="standing(a).color" style="margin: 0">{{ standing(a).label }}</span></td>
                  <template v-if="a.role === 'reseller'">
                    <td><span class="atag ltr" :class="customersTag(a).color" style="margin: 0">{{ customersTag(a).label }}</span></td>
                    <td>
                      <div class="traffic-cell" :class="{ 'is-unlimited': !a.quotaBytes }">
                        <span class="traffic-used ltr">{{ bytes(a.usedBytes, store.locale) }}</span>
                        <span class="aprogress traffic-bar"><span :style="{ width: (a.quotaBytes ? usedPercent(a) : 100) + '%', background: barColor(a) }"></span></span>
                        <span class="traffic-limit ltr">
                          <span v-if="!a.quotaBytes" class="traffic-infinity">∞</span>
                          <template v-else>{{ bytes(a.quotaBytes, store.locale) }}</template>
                        </span>
                      </div>
                    </td>
                    <td><span class="atag ltr" :class="trafficLeftTag(a).color" style="margin: 0">{{ trafficLeftTag(a).label }}</span></td>
                    <td><span class="atag" dir="auto" :class="termTag(a).color" :title="termTag(a).title" style="margin: 0">{{ termTag(a).label }}</span></td>
                    <td>
                      <template v-if="(a.interfaceIds || []).length">
                        <span v-for="id in a.interfaceIds.slice(0, CHIP_LIMIT)" :key="id" class="atag" :class="serverProto(id) === 'openvpn' ? 'orange' : 'gold'" style="margin: 2px">{{ serverName(id) }}</span>
                        <span v-if="a.interfaceIds.length > CHIP_LIMIT" class="atag default" style="margin: 2px" :title="a.interfaceIds.slice(CHIP_LIMIT).map(serverName).join(', ')">+{{ a.interfaceIds.length - CHIP_LIMIT }}</span>
                      </template>
                      <span v-else class="atag red" style="margin: 0" :title="t('admins.noServersWarn')">{{ t('admins.noServers') }}</span>
                    </td>
                  </template>
                  <template v-else>
                    <td colspan="5" class="cell-empty">{{ t('admins.role.adminHint') }}</td>
                  </template>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </template>
  </div>

  <ResellerForm
    v-if="formFor"
    :interfaces="servers"
    :admin="formFor.admin || null"
    :group-names="groupNames"
    @close="formFor = null"
    @saved="saved"
  />

  <!-- Removing one. What becomes of their customers is asked rather than
       assumed: one answer quietly stops people paying for a service and the
       other quietly leaves the owner administering customers they did not
       know they had. -->
  <div v-if="removing" class="amodal-backdrop centered" @click.self="removing = null">
    <div class="amodal" role="alertdialog" aria-modal="true" aria-labelledby="rm-title">
      <div class="amodal-head">
        <h2 id="rm-title" class="amodal-title">{{ t('admins.remove') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="removing = null"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <p class="rm-text">{{ t(removing.ids.length > 1 ? 'admins.removeAskMany' : 'admins.removeAsk', { name: removing.name }) }}</p>
        <span class="atag red" style="margin: 0">{{ t('admins.holdsCustomers', { n: nf(removing.customers) }) }}</span>
      </div>
      <div class="amodal-foot">
        <button class="abtn" @click="removing = null">{{ t('action.cancel') }}</button>
        <button class="abtn" :disabled="busy" @click="remove('keep')">{{ t('admins.removeKeep') }}</button>
        <button class="abtn danger" :disabled="busy" @click="remove('delete')">{{ t('admins.removeWithClients') }}</button>
      </div>
    </div>
  </div>

  <Teleport to="body">
    <div v-if="moreOpen" v-fit="moreOpen.rect" class="amenu" role="menu" :style="{ top: moreOpen.y + 'px', left: moreOpen.x + 'px' }">
      <template v-for="(m, i) in bulkItems" :key="m.key || `d${i}`">
        <hr v-if="m.divider" class="amenu-divider" />
        <button v-else class="amenu-item" :class="{ danger: m.danger }" role="menuitem" @click="pickBulk(m.key)">
          <AntIcon :name="m.icon" /><span>{{ m.label }}</span>
        </button>
      </template>
    </div>
  </Teleport>

  <ConfirmDialog
    :open="!!ask"
    :title="ask?.title || ''"
    :body="ask?.body || ''"
    :subject="ask?.subject || ''"
    :consequences="ask?.consequences || []"
    :confirm-label="ask?.confirmLabel || ''"
    :danger="ask?.danger !== false"
    :busy="busy"
    @confirm="runConfirmed"
    @cancel="ask = null"
  />

  <!-- The actions that need a value. -->
  <div v-if="bulkDialog" class="amodal-backdrop centered" @click.self="bulkDialog = null">
    <div class="amodal" role="dialog" aria-modal="true" aria-labelledby="bd-title">
      <div class="amodal-head">
        <h2 id="bd-title" class="amodal-title">{{ bulkTitle }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="bulkDialog = null"><AntIcon name="CloseOutlined" /></button>
      </div>
      <form id="bulk-form" class="amodal-body" @submit.prevent="submitBulkDialog">
        <p class="bd-target">{{ t('admins.bulk.appliesTo', { n: nf(selected.size) }) }}</p>

        <div v-if="bulkDialog.kind === 'extend'" class="aform-item">
          <label class="aform-label" for="bd-days">{{ t('admins.bulk.days') }}</label>
          <label class="ainput number"><input id="bd-days" v-model="bulkDialog.days" type="number" min="1" max="3650" step="1" class="ltr" autofocus /><span class="ainput-suffix">{{ t('unit.days') }}</span></label>
          <p class="hint">{{ t('admins.bulk.extendHint') }}</p>
        </div>

        <div v-else-if="bulkDialog.kind === 'setQuota'" class="aform-item">
          <label class="aform-label" for="bd-quota">{{ t('admins.traffic') }}</label>
          <div class="acompact">
            <label class="ainput number"><input id="bd-quota" v-model="bulkDialog.quota" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="t('client.unlimited')" autofocus /></label>
            <div class="aselect unit"><select v-model="bulkDialog.quotaUnit" :aria-label="t('client.quotaUnit')"><option value="GB">GB</option><option value="TB">TB</option></select></div>
          </div>
          <p class="hint">{{ t('admins.bulk.emptyUnlimited') }}</p>
        </div>

        <div v-else-if="bulkDialog.kind === 'setClientLimit'" class="aform-item">
          <label class="aform-label" for="bd-limit">{{ t('admins.clientLimit') }}</label>
          <label class="ainput number"><input id="bd-limit" v-model="bulkDialog.limit" type="number" min="0" step="1" class="ltr" :placeholder="t('client.unlimited')" autofocus /></label>
          <p class="hint">{{ t('admins.bulk.emptyUnlimited') }}</p>
        </div>

        <div v-else class="aform-item">
          <label class="aform-label">{{ t('admins.servers') }}</label>
          <MultiSelect v-model="bulkDialog.interfaceIds" :options="serverOptions" :placeholder="t('client.selectServers')" direction="down" />
          <p class="hint">{{ bulkDialog.kind === 'addServers' ? t('admins.bulk.addServersHint') : t('admins.bulk.removeServersHint') }}</p>
        </div>

        <p v-if="bulkError" class="field-error">{{ bulkError }}</p>
      </form>
      <div class="amodal-foot">
        <button class="abtn" type="button" @click="bulkDialog = null">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="submit" form="bulk-form" :disabled="busy">{{ t('action.save') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.arow.six > .acol { flex: 0 0 16.6667%; max-width: 16.6667%; }
@media (max-width: 991px) { .arow.six > .acol { flex: 0 0 33.3333%; max-width: 33.3333%; } }
@media (max-width: 575px) { .arow.six > .acol { flex: 0 0 50%; max-width: 50%; } }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 4px; vertical-align: middle; }
.dot-green { background: var(--ok); }
.dot-red { background: var(--bad); }
.dot-orange { background: var(--warn); }
.summary-card { margin-bottom: 16px; }

.acard.small .acard-head { min-height: 38px; padding: 0 12px; }
.acard.small .acard-body { padding: 12px; }
.card-toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; width: 100%; padding: 6px 0; }

.resellers-empty { padding: 32px 0; text-align: center; color: var(--muted); }

.filter-bar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 12px; }
.filter-bar .aselect { width: auto; }
.filter-count { margin-inline-start: auto; color: var(--muted); font-size: 13px; white-space: nowrap; }
.sort-select { position: relative; }
.sort-select .aselect-suffix { position: absolute; inset-inline-end: 11px; top: 50%; transform: translateY(-50%); color: var(--faint); font-size: 12px; pointer-events: none; }
.sort-select::after { display: none; }
.sort-select select { padding-inline-end: 28px; }

/* A tile that filters: a button that looks like the tile it is. */
.stat-button { display: block; width: 100%; padding: 4px 6px; margin: -4px -6px; border: 1px solid transparent; border-radius: 8px; background: none; color: inherit; font: inherit; text-align: start; cursor: pointer; }
.stat-button:hover { background: var(--surface-2); }
.stat-button.on { border-color: var(--accent); background: var(--accent-soft); }
.stat-button .stat-title, .stat-button .stat-content { display: block; }
.resellers-empty .anticon { display: block; margin: 0 auto 8px; opacity: 0.5; }

tr.off .who .name { opacity: 0.55; }
/* A switched-off row recedes, but its switch and its actions do not: they
   are how it is switched back on, edited or removed, and a faded button
   reads as one that cannot be pressed. */
tr.off td.keep { opacity: 1; }
.who { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.who .name { font-weight: 500; }
.who .sub { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 200px; }
.cell-empty { color: var(--faint); font-size: 12px; }

/* The customer list's traffic pill: the figures either side of a bar. */
.traffic-cell { display: flex; align-items: center; gap: 8px; width: 100%; min-width: 0; box-sizing: border-box; padding: 2px 10px; border-radius: 999px; background: var(--surface-2); }
.traffic-used, .traffic-limit { flex: 0 0 68px; min-width: 68px; font-size: 12px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.traffic-used { text-align: end; color: var(--ink); }
.traffic-limit { text-align: start; color: var(--muted); }
.traffic-bar { flex: 1 1 60px; min-width: 40px; }
.traffic-cell.is-unlimited .traffic-bar > span { border: 1px solid rgba(114, 46, 209, 0.55); }
.traffic-infinity { color: var(--tag-purple-ink); font-size: 14px; line-height: 1; }

/* Phone: a card each. */
.rcards { display: flex; flex-direction: column; gap: 10px; }
.rcard { padding: 12px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
.rcard.off { opacity: 0.7; }
.rcard-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.rcard-name { display: flex; align-items: center; gap: 8px; min-width: 0; flex-wrap: wrap; }
.rcard-name .name { font-weight: 600; }
.rcard-note { margin-top: 4px; font-size: 12px; color: var(--muted); }
.rcard-stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin: 10px 0 0; }
.rcard-stats dt { font-size: 11px; color: var(--faint); margin-bottom: 2px; }
.rcard-stats dd { margin: 0; }
/* A tag here has a third of a phone to live in; a long one wraps inside it
   rather than pushing out past the card. */
.rcard-stats dd .atag { max-width: 100%; white-space: normal; line-height: 1.4; }
.rcard-actions { display: flex; gap: 4px; flex-wrap: wrap; margin-top: 10px; padding-top: 8px; border-top: 1px solid var(--border); }
.rcard-actions .abtn span { margin-inline-start: 4px; }

.rm-text { margin: 0 0 12px; color: var(--ink); line-height: 1.6; }

/* The selection. */
.atable th.sel, .atable td.sel { width: 40px; text-align: center; }
tr.picked td { background: var(--accent-soft); }
.rcard.picked { border-color: var(--accent); }
.rcard-head .acheck { flex: none; }
.card-bulk-bar { display: flex; align-items: center; gap: 8px; padding: 4px 2px; }
.bd-target { margin: 0 0 16px; color: var(--muted); }
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.field-error { margin: 8px 0 0; font-size: 12px; color: var(--bad); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.acompact .aselect.unit { flex: 0 0 auto; width: auto; min-width: 72px; }
</style>
