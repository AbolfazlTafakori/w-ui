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
// How a row reads is lib/resellers/rows.js; the rows, the cards and the
// dialogs are their own; this holds the list's state and wires them.
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import { useIsMobile } from '../lib/mobile.js'
import { SORTS, STATUS_FILTERS, shownRows, summarize } from '../lib/resellers/rows.js'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import PageSpin from '../components/PageSpin.vue'
import ResellerForm from '../components/ResellerForm.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MoreMenu from '../components/MoreMenu.vue'
import ResellersTable from '../components/resellers/ResellersTable.vue'
import ResellerCards from '../components/resellers/ResellerCards.vue'
import RemoveDialog from '../components/resellers/RemoveDialog.vue'
import BulkDialog from '../components/resellers/BulkDialog.vue'

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

// --- Finding one. -----------------------------------------------------------
//
// The customer list's search, filters and sort, over a list that is small
// enough to hold in the page: filtering here answers as the operator types,
// with no request per keystroke. Kept in the address, so a reload or a link
// lands on the same view.
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

const shown = computed(() =>
  shownRows(rows.value, { search: search.value, status: statusFilter.value, server: serverFilter.value, sort: sort.value }),
)

// A summary tile is also the way to its list.
function showBucket(k) {
  statusFilter.value = statusFilter.value === k ? '' : k
}

// --- Across the top. -------------------------------------------------------
const summary = computed(() => summarize(rows.value))

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

// The dialog for the actions that need a value, by the action's key.
const bulkKind = ref(null)

function pickBulk(key) {
  const subject = t('admins.bulk.nResellers', { n: nf(selected.value.size) })
  const customers = selectedRows.value.reduce((sum, a) => sum + (a.clients || 0), 0)
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
  bulkKind.value = key
}

async function submitBulk(input) {
  bulkKind.value = null
  await runBulk(input)
}

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
              <MoreMenu :items="bulkItems" :compact="isMobile" :disabled="busy" @pick="pickBulk" />
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

          <ResellerCards
            v-else-if="isMobile"
            :rows="shown"
            :selected="selected"
            :pending="pending"
            :all-selected="allSelected"
            :some-selected="someSelected"
            @toggle="toggleOne"
            @toggle-all="toggleAll"
            @edit="(a) => (formFor = { admin: a })"
            @reset="resetUsage"
            @remove="(a) => askRemove([a])"
            @enable="setEnabled"
          />
          <ResellersTable
            v-else
            :rows="shown"
            :selected="selected"
            :pending="pending"
            :servers="servers"
            :all-selected="allSelected"
            :some-selected="someSelected"
            @toggle="toggleOne"
            @toggle-all="toggleAll"
            @edit="(a) => (formFor = { admin: a })"
            @reset="resetUsage"
            @remove="(a) => askRemove([a])"
            @enable="setEnabled"
          />
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

  <RemoveDialog v-if="removing" :removing="removing" :busy="busy" @close="removing = null" @remove="remove" />

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

  <BulkDialog v-if="bulkKind" :kind="bulkKind" :count="selected.size" :servers="servers" :busy="busy" @close="bulkKind = null" @submit="submitBulk" />
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
</style>
