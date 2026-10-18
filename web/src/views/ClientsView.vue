<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import { store, t, notify } from '../lib/store.js'
import ClientForm from '../components/ClientForm.vue'
import ExtendDialog from '../components/ExtendDialog.vue'
import BulkClientForm from '../components/BulkClientForm.vue'
import ClientQrModal from '../components/ClientQrModal.vue'
import ClientInfoModal from '../components/ClientInfoModal.vue'
import AntIcon from '../components/AntIcon.vue'
import ImportDialog from '../components/clients/ImportDialog.vue'
import SubLinksDialog from '../components/clients/SubLinksDialog.vue'
import ClientsTable from '../components/clients/ClientsTable.vue'
import ClientCards from '../components/clients/ClientCards.vue'
import Pager from '../components/Pager.vue'
import BulkDialog from '../components/clients/BulkDialog.vue'
import AccountCard from '../components/clients/AccountCard.vue'
import { filterChips as chipsFor, filtersFromLink, listQuery, withoutChip } from '../lib/clients/filters.js'
import { useClientActions } from '../lib/clients/actions.js'
import ClientStats from '../components/clients/ClientStats.vue'
import { useSelection } from '../lib/clients/selection.js'
import MoreMenu from '../components/MoreMenu.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import FilterDrawer, { emptyFilters, activeFilterCount } from '../components/FilterDrawer.vue'
import PageSpin from '../components/PageSpin.vue'
import { useIsMobile } from '../lib/mobile.js'

const router = useRouter()
const route = useRoute()

const page = ref(null)
const stats = ref(null)
const interfaces = ref([])
const groupNames = ref([])
const loading = ref(true)

const search = ref(route.query.search || '')
const statusFilter = ref('')
const groupFilter = ref('')
// Oldest first: the list reads in the order customers were added, so a
// customer's place in it does not move each time another is added.
const sort = ref('oldest')

// Their sort list, in their order and words.
const SORT_OPTIONS = [
  { value: 'oldest', key: 'client.sort.oldest' },
  { value: 'newest', key: 'client.sort.newest' },
  { value: 'updated', key: 'client.sort.recentlyUpdated' },
  { value: 'online', key: 'client.sort.recentlyOnline' },
  { value: 'name', key: 'client.sort.nameAZ' },
  { value: 'name_desc', key: 'client.sort.nameZA' },
  { value: 'traffic', key: 'client.sort.mostTraffic' },
  { value: 'remaining', key: 'client.sort.highestRemaining' },
  { value: 'expiry', key: 'client.sort.expiringSoonest' },
]
const currentPage = ref(1)
// Their pagination's size changer: the panel's page size to start with, and
// the sizes Ant offers.
const PAGE_SIZES = [10, 25, 50, 100, 200]
const pageSize = ref(store.panel?.pageSize > 0 ? store.panel.pageSize : 25)
function setPageSize(n) {
  pageSize.value = Number(n)
  currentPage.value = 1
  load()
}
function goPage(n) {
  currentPage.value = n
  load()
}
const formFor = ref(null)
const shareFor = ref(null)
const infoFor = ref(null)
const dialog = ref(null) // { kind }
const bulkOpen = ref(false)
const selected = ref(new Set())
// On a phone the table becomes a list of cards, as the classic panel's clients do,
// and each card's actions live behind one menu.
const isMobile = useIsMobile()
const busy = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// `quiet` marks a poll: no spinner, no progress bar, and a failure that is not
// worth a toast — the page keeps showing what it had and tries again shortly.
// The filter drawer's state. Kept beside the search box rather than inside the
// drawer so the list can be narrowed while it is shut, and so the button can
// say how many categories are doing the narrowing.
const filterOpen = ref(false)
// A link from elsewhere may name what to open the list on: a status, from
// the overview's tiles (/clients?status=depleting), or a group, from the
// groups page (/clients?group=trial). Each becomes that one filter, which
// shows as a chip and is cleared like any other.
const filters = ref(filtersFromLink(route.query))
const filterCount = computed(() => activeFilterCount(filters.value))

// What the list is asked for, but the page.
const query = () => listQuery({ search: search.value, status: statusFilter.value, group: groupFilter.value, sort: sort.value, filters: filters.value })

// A narrowed list is a different list: staying on page four of the old one
// would show an empty table and look like the filter matched nothing.
watch(filters, () => { currentPage.value = 1; load() }, { deep: true })

// A reseller's own account, over their customers: how many they may still
// add, how much traffic and how many days they have left. Read again with
// the list, because the traffic moves with every customer's.
const reseller = computed(() => store.admin?.role === 'reseller')
const account = ref(null)
async function loadAccount() {
  if (!reseller.value) return
  try {
    account.value = await api.me()
  } catch {
    /* the strip stays as it was; the list is what matters */
  }
}
async function load(quiet = false) {
  if (!quiet) loading.value = true
  loadAccount()
  try {
    const [p, o] = await Promise.all([
      api.clients({ ...query(), page: currentPage.value, perPage: pageSize.value }, { background: quiet }),
      api.overview({ background: quiet }),
    ])

    stats.value = o
    if (quiet && page.value) {
      // Patched in place so a switch the operator has just flipped does not
      // snap back for one tick while its own request is still in flight.
      page.value.total = p.total
      page.value.items = mergeRows(page.value.items, p.items, pending.value)
    } else {
      page.value = p
    }

    // Drop selections for rows no longer on screen, so a bulk action can never
    // reach a client the operator can no longer see -- unless every customer
    // the filter matches was selected on purpose, across the pages.
    if (!allMatching.value) {
      const visible = new Set(p.items.map((c) => c.id))
      selected.value = new Set([...selected.value].filter((id) => visible.has(id)))
    }
  } catch (err) {
    if (!quiet) notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

// Online, traffic and remaining are recomputed on the server every couple of
// seconds. Five is often enough to follow that without asking a busy panel for
// a hundred rows every three.
// Shown only once a first load has been running for 160ms. Anything faster
// leaves the screen alone: a skeleton that appears and vanishes inside two
// frames reads as a rendering fault, not as progress.
const firstLoad = computed(() => loading.value && !page.value)
const showSkeleton = useDelayed(firstLoad)
// A refilter keeps the rows on screen and dims them instead, so the page does
// not collapse and spring back for every change of a dropdown.
const refiltering = computed(() => loading.value && !!page.value)

useLive(load, {
  every: 3000,
  // Not while something is open over the top of the list.
  busy: () => !!formFor.value || !!shareFor.value || !!ask.value,
})

// Waiting out the interval after the operator has just changed something is
// the one case where a poll is too slow to be acceptable: the switch moves and
// the counts above it go on insisting the opposite for four seconds.
//
// Deliberately not the poll's own refresh, which declines to run while another
// is in flight. A poll that started before this change committed would return
// the state from before it, and skipping this one would leave the stale answer
// on screen until the next tick. The operator has just acted and is looking at
// the result, so this asks regardless.
function settled() {
  load(true)
}

async function loadGroups() {
  try {
    groupNames.value = await api.groupNames()
  } catch {
    /* the group column simply stays hidden */
  }
}

onMounted(async () => {
  try {
    interfaces.value = await api.interfaces()
  } catch (err) {
    notify(err.message, 'error')
  }
  await Promise.all([load(), loadGroups()])
})


let searchTimer = null
watch(search, () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    currentPage.value = 1
    load()
  }, 250)
})
watch([statusFilter, groupFilter, sort], () => {
  currentPage.value = 1
  load()
})

const hasGroups = computed(() => groupNames.value.length > 0)

// Their chips: one per active filter, each closable on its own.
const filterChips = computed(() => chipsFor(filters.value, interfaces.value, t))
function clearChip(chip) {
  filters.value = withoutChip(filters.value, chip)
}

// Clearing every filter at once. Undoing three separately, to find out whether
// the list is empty or merely filtered, is work the panel should do.
function clearFilters() {
  search.value = ''
  statusFilter.value = ''
  groupFilter.value = ''
  filters.value = emptyFilters()
  currentPage.value = 1
  load()
}

// Which customers are chosen: ticked by hand, or every one the filter matches.
const { allSelected, toggleAll, toggleOne, allMatching, matchingBusy, filtering, offerAllMatching, selectAllMatching, clearSelection } =
  useSelection({
    page, selected, query,
    filtered: () => !!(search.value || statusFilter.value || groupFilter.value || filterCount.value),
    filterKey: () => JSON.stringify([search.value, statusFilter.value, groupFilter.value, filters.value]),
  })

// What an operator does to customers, and the menu that offers it.
const { ids, pending, setEnabled, ask, runConfirmed, rotateKeysFor, resetOne, removeOne, bulk, pickMore: pick, menu: moreItems } =
  useClientActions({
    page, stats, selected, busy, load, loadGroups, settled,
    open: {
      dialog: (kind) => (dialog.value = { kind }),
      subLinks: openSubLinks,
      importer: () => (importOpen.value = true),
      batch: () => (bulkOpen.value = true),
      extend: (kind) => (extendFor.value = { kind }),
    },
  })
function dialogDone() {
  clearSelection()
  Promise.all([load(), loadGroups()])
}


// The import and subscription-links dialogs, while open. The links are for
// the customers chosen when it opened, named from the rows on this page.
const importOpen = ref(false)
const subLinks = ref(null) // { ids, names }
function openSubLinks() {
  const names = {}
  for (const c of page.value?.items || []) names[c.id] = c.name
  subLinks.value = { ids: ids(), names }
}

// Time or traffic for the selection: its own dialog, which works out what
// will happen before anything does.
const extendFor = ref(null)
function extendDone() {
  extendFor.value = null
  clearSelection()
  load()
}

// A batch is created in one call and reported as one number; a failure
// part way names how many were made, from the server's own message.
async function submitBulk(input) {
  try {
    const res = await api.createBatch(input)
    notify(`${t('client.created')} — ${nf(res.created)}`, 'success')
    bulkOpen.value = false
    await Promise.all([load(), loadGroups()])
  } catch (err) {
    notify(err.message, 'error')
  }
}

async function submitForm(input) {
  const payload = { ...input }
  try {
    if (formFor.value?.client) {
      // interfaceIds is deliberately kept on an edit: it is how an operator
      // adds or removes a server for an existing customer. deviceNames is not,
      // because devices are managed on the customer's own page.
      delete payload.deviceNames
      await api.updateClient(formFor.value.client.id, payload)
      notify(t('client.updated'), 'success')
    } else {
      await api.createClient(payload)
      notify(t('client.created'), 'success')
    }
    formFor.value = null
    await Promise.all([load(), loadGroups()])
  } catch (err) {
    notify(err.message, 'error')
    // Handled here. Rethrowing sends it to Vue's error boundary, which would
    // report a failed save as a page that cannot be displayed.
  }
}
</script>

<template>
  <FilterDrawer
    v-model="filters"
    :open="filterOpen"
    :interfaces="interfaces"
    :groups="groupNames"
    @close="filterOpen = false"
  />

  <div class="antpage clients">
  <!-- A reseller's own account: what they bought and how much of it is left. -->
  <AccountCard v-if="reseller" :account="account || store.admin" />

  <!-- Their summary Card: size="small", six Statistics with a coloured dot. -->
  <ClientStats v-if="stats" :stats="stats" />

  <div v-if="!interfaces.length" class="aalert warning">
    <AntIcon name="ExclamationCircleFilled" />
    <div class="aalert-body">
      <span v-if="reseller" class="aalert-title">{{ t('admins.noServersGiven') }}</span>
      <span v-else class="aalert-title">{{ t('interface.noneYet') }} <a href="#" @click.prevent="router.push('/interfaces')">{{ t('interface.create') }}</a></span>
    </div>
  </div>

  <!-- Their list Card: size="small", the title a toolbar of Add Clients (or
       the selection count), More, and -- with rows picked -- Delete at the end. -->
  <div class="acard small">
    <div class="acard-head">
      <div class="card-toolbar">
        <button v-if="!selected.size" class="abtn primary" :disabled="!interfaces.length" :title="interfaces.length ? '' : t('interface.noneYet')" @click="formFor = {}">
          <AntIcon name="PlusOutlined" /><span v-if="!isMobile">{{ t('client.menu.addClients') }}</span>
        </button>
        <span v-else class="atag blue closable" style="padding: 4px 8px; font-size: 13px">
          {{ t('client.menu.selectedCount').replace('{count}', nf(selected.size)) }}
          <button type="button" class="atag-close" :aria-label="t('action.cancel')" @click="selected = new Set()"><AntIcon name="CloseOutlined" /></button>
        </span>
        <MoreMenu :items="moreItems" :compact="isMobile" @pick="pick" />
        <button v-if="selected.size" class="abtn danger" style="margin-inline-start: auto" @click="bulk('delete')">
          <AntIcon name="DeleteOutlined" /><span v-if="!isMobile">{{ t('action.delete') }}</span>
        </button>
      </div>
    </div>

    <div class="acard-body">
      <!-- Their filter bar: search, Filter with its badge, Sort, Clear all,
           and how many of the total are shown. -->
      <div class="filter-bar" :class="{ mobile: isMobile }">
        <label class="ainput" :class="{ small: isMobile }" :style="isMobile ? 'max-width: 200px; width: 100%' : 'max-width: 320px; width: 100%'">
          <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
          <input v-model="search" type="text" :placeholder="t('client.menu.searchPlaceholder')" :aria-label="t('action.search')" />
          <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.cancel')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
        </label>
        <span class="abadge-wrap">
          <button class="abtn" :class="{ primary: filterCount > 0 }" @click="filterOpen = true">
            <AntIcon name="FilterOutlined" /><span v-if="!isMobile">{{ t('filter.button') }}</span>
          </button>
          <sup v-if="filterCount" class="abadge">{{ filterCount }}</sup>
        </span>
        <div class="aselect sort-select" :class="{ small: isMobile }" :style="{ minWidth: (isMobile ? 130 : 200) + 'px' }">
          <select v-model="sort" :aria-label="t('client.sort.label')">
            <option v-for="o in SORT_OPTIONS" :key="o.value" :value="o.value">{{ t(o.key) }}</option>
          </select>
          <span class="aselect-suffix"><AntIcon name="SortAscendingOutlined" /></span>
        </div>
        <button v-if="filterCount || search || statusFilter || groupFilter" class="abtn" @click="clearFilters">{{ t('client.menu.clearAllFilters') }}</button>
        <span v-if="page && (filterCount || search || statusFilter || groupFilter)" class="filter-count">
          {{ t('client.menu.showingCount').replace('{shown}', nf(page.total)).replace('{total}', nf(stats?.clients ?? page.total)) }}
        </span>
      </div>

      <!-- Every customer the filter matches, across the pages: offered once
           the page is ticked, said once it is chosen. -->
      <div v-if="offerAllMatching || allMatching" class="sel-bar" role="status">
        <template v-if="allMatching">
          <span>{{ t(filtering ? 'client.sel.allFilteredSelected' : 'client.sel.allSelected', { n: selected.size }) }}</span>
          <button type="button" class="linkbtn" @click="clearSelection">{{ t('client.sel.clear') }}</button>
        </template>
        <template v-else>
          <span>{{ t('client.sel.page', { n: page.items.length }) }}</span>
          <button type="button" class="linkbtn" :disabled="matchingBusy" @click="selectAllMatching">
            <span v-if="matchingBusy" class="spin sm"></span>
            {{ t(filtering ? 'client.sel.selectAllFiltered' : 'client.sel.selectAll', { n: page.total }) }}
          </button>
        </template>
      </div>
      <div v-if="filterChips.length" class="filter-chips">
        <span v-for="(chip, i) in filterChips" :key="i" class="atag closable" :class="chip.color">
          {{ chip.text }}
          <button type="button" class="atag-close" :aria-label="t('action.remove')" @click="clearChip(chip)"><AntIcon name="CloseOutlined" /></button>
        </span>
      </div>

      <PageSpin v-if="showSkeleton" />
      <div v-else-if="loading && !page" class="empty"></div>

      <ClientCards
        v-else-if="isMobile"
        :items="page?.items || []" :selected="selected" :all-selected="allSelected" :pending="pending" :interfaces="interfaces"
        @toggle="toggleOne" @toggle-all="toggleAll" @qr="(c) => (shareFor = c)" @info="(c) => (infoFor = c)"
        @rotate="rotateKeysFor" @reset="resetOne" @edit="(c) => (formFor = { client: c })" @remove="removeOne"
        @enable="setEnabled" @group="(g) => (groupFilter = g)"
        :total="page?.total || 0" :page="currentPage" :page-size="pageSize" :stale="refiltering" @go="goPage"
      />

      <template v-else>
        <ClientsTable
          :items="page?.items || []" :selected="selected" :all-selected="allSelected" :pending="pending" :interfaces="interfaces"
          @toggle="toggleOne" @toggle-all="toggleAll" @qr="(c) => (shareFor = c)" @info="(c) => (infoFor = c)"
          @rotate="rotateKeysFor" @reset="resetOne" @edit="(c) => (formFor = { client: c })" @remove="removeOne"
          @enable="setEnabled" @group="(g) => (groupFilter = g)"
          :has-groups="hasGroups" :group-filter="groupFilter" :stale="refiltering"
        />
        <Pager v-if="page && page.total > pageSize" :total="page.total" :page="currentPage" :page-size="pageSize" :sizes="PAGE_SIZES" @go="goPage" @size="setPageSize" />
      </template>
    </div>
  </div>
  </div>


  <BulkDialog v-if="dialog" :kind="dialog.kind" :ids="ids()" :interfaces="interfaces" :group-names="groupNames"
    @close="dialog = null" @done="dialogDone" />

  <ImportDialog v-if="importOpen" :interfaces="interfaces" @close="importOpen = false" @imported="load()" />
  <SubLinksDialog v-if="subLinks" :ids="subLinks.ids" :names="subLinks.names" @close="subLinks = null" />
  <ExtendDialog
    v-if="extendFor"
    :kind="extendFor.kind"
    :ids="ids()"
    @close="extendFor = null"
    @done="extendDone"
  />

  <ClientForm
    v-if="formFor"
    :interfaces="interfaces"
    :client="formFor.client"
    @close="formFor = null"
    @submit="submitForm"
  />
  <BulkClientForm v-if="bulkOpen" :interfaces="interfaces" @close="bulkOpen = false" @submit="submitBulk" />
  <ClientQrModal v-if="shareFor" :client="shareFor" :interfaces="interfaces" @close="shareFor = null" />
  <ClientInfoModal v-if="infoFor" :client="infoFor" :interfaces="interfaces" @close="infoFor = null" />

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

<style>
/* Keyed on the page's root rather than scoped, so the components it is
   made of are styled as when their markup was the page's. */
/* the classic panel's ClientsPage.css, measured as it is. */
@media (max-width: 991px) { .clients .arow.six > .acol { flex: 0 0 33.3333%; max-width: 33.3333%; } }
@media (max-width: 575px) { .clients .arow.six > .acol { flex: 0 0 50%; max-width: 50%; } }

.clients .acard.small .acard-head { min-height: 38px; padding: 0 12px; }
.clients .acard.small .acard-body { padding: 12px; }
.clients .card-toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; width: 100%; padding: 6px 0; }
.clients .filter-bar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 12px; }
.clients .filter-count { margin-inline-start: auto; color: var(--muted); font-size: 13px; white-space: nowrap; }
.clients .filter-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 0 0 12px; padding: 6px 8px; background: var(--surface-2); border-radius: 8px; }
.clients .filter-chips .atag { margin: 0; }
.clients .abadge-wrap { position: relative; display: inline-block; }
.clients .abadge { position: absolute; top: 4px; inset-inline-end: 4px; transform: translate(50%, -50%); min-width: 16px; height: 16px; padding: 0 4px; border-radius: 8px; background: var(--bad); color: #fff; font-size: 12px; line-height: 16px; text-align: center; box-shadow: 0 0 0 1px var(--surface); }
.clients .filter-bar .sort-select { width: auto; }
.clients .sort-select .aselect-suffix { position: absolute; inset-inline-end: 11px; top: 50%; transform: translateY(-50%); color: var(--faint); font-size: 12px; pointer-events: none; }
.clients .sort-select::after { display: none; }
.clients .sort-select select { padding-inline-end: 28px; }

.clients .atable th.center, .clients .atable td.center { text-align: center; }
.clients .email-cell .email:hover, .clients a.tag-name:hover { text-decoration: underline; }
.clients .toggle-wrap { display: inline-flex; }
.clients .online-dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-inline-end: 5px; vertical-align: middle; background: var(--ok); animation: online-blink 1.1s ease-in-out infinite; }
@keyframes online-blink { .clients 0%, .clients 100% { opacity: 1; } .clients 50% { opacity: 0.35; } }
/* Figures of one width, so a speed that changes every few seconds does not
   shift the rest of the line. */

/* Their ClientTrafficCell: a pill with the figures either side of a bar. */
.clients .client-traffic-cell { display: flex; align-items: center; gap: 8px; width: 100%; min-width: 0; box-sizing: border-box; padding: 2px 10px; border-radius: 999px; background: var(--surface-2); }
.clients .client-traffic-cell-used, .clients .client-traffic-cell-limit { flex: 0 0 72px; min-width: 72px; font-size: 12px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.clients .client-traffic-cell-used { text-align: end; color: var(--ink); }
.clients .client-traffic-cell-limit { text-align: start; color: var(--muted); }
.clients .client-traffic-cell-bar { flex: 1 1 60px; min-width: 48px; }
.clients .client-traffic-cell.is-unlimited .client-traffic-cell-bar > span { border: 1px solid rgba(114, 46, 209, 0.55); }
.clients .client-traffic-cell-infinity { display: inline-flex; align-items: center; color: var(--tag-purple-ink); font-size: 14px; line-height: 1; }

.clients .account-item .atag { margin: 0; }

.clients .clients-empty .anticon { display: block; margin: 0 auto 8px; }

/* Every match, across the pages: offered and said in one quiet line. */
.clients .sel-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 4px 10px;
  margin: 8px 0 0;
  padding: 7px 12px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  color: var(--ink);
  font-size: var(--t-sm);
}
.clients .sel-bar .linkbtn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}
.clients .apagination-total { align-items: center; height: 32px; color: var(--ink); font-size: 14px; }
.clients .apagination.small .apagination-total { height: 24px; }
.clients .apage.jump { border-color: transparent; background: transparent; letter-spacing: 2px; color: var(--faint); }
/* The size changer is as wide as its words, 16px past the arrow as Ant's
   is -- not the full-width select a form field is. */
.clients .apagination > .apage-size { width: auto; margin-inline-start: 8px; }
.clients .apage-size { padding: 0 32px 0 11px; font-size: 14px; }
.clients .apage-size-label { white-space: nowrap; color: var(--ink); }
.clients .apage-size select { position: absolute; inset: 0; width: 100%; height: 100%; opacity: 0; cursor: pointer; }
.clients .apage .anticon { font-size: 12px; }
</style>

