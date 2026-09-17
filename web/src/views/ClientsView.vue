<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes, relative, dateTime, percent, unitToBytes, unitToHours } from '../lib/format.js'
import ClientForm from '../components/ClientForm.vue'
import ClientQrModal from '../components/ClientQrModal.vue'
import ClientInfoModal from '../components/ClientInfoModal.vue'
import AntIcon from '../components/AntIcon.vue'
import Toggle from '../components/Toggle.vue'
import Icon from '../components/Icon.vue'
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
const sort = ref('newest')

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
// The page numbers Ant shows: every one up to seven, else the ends and a
// window around the current one with jumps between.
const pageItems = computed(() => {
  const total = totalPages.value
  const cur = currentPage.value
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1)
  const out = [1]
  const from = Math.max(2, cur - 2)
  const to = Math.min(total - 1, cur + 2)
  if (from > 2) out.push('prev')
  for (let i = from; i <= to; i++) out.push(i)
  if (to < total - 1) out.push('next')
  out.push(total)
  return out
})

const formFor = ref(null)
const shareFor = ref(null)
const infoFor = ref(null)
const dialog = ref(null) // { kind }
const form = ref({ group: '', addDays: '', addUnit: 'days', quotaGB: '', quotaUnit: 'GB', resetCycle: '', prefix: '', count: 10 })
const selected = ref(new Set())
// On a phone the table becomes a list of cards, as the classic panel's clients do,
// and each card's actions live behind one menu.
const isMobile = useIsMobile()
const cardMenu = ref(null)
function openCardMenu(c, e) {
  e.stopPropagation()
  const r = e.currentTarget.getBoundingClientRect()
  cardMenu.value = cardMenu.value?.client?.id === c.id ? null : { client: c, rect: r, x: Math.max(8, r.right - 180), y: r.bottom + 4 }
}
function cardAction(key) {
  const c = cardMenu.value?.client
  cardMenu.value = null
  if (!c) return
  if (key === 'qr') shareFor.value = c
  else if (key === 'reset') resetOne(c)
  else if (key === 'edit') formFor.value = { client: c }
  else if (key === 'delete') removeOne(c)
}
const moreOpen = ref(null) // { x, y }
const busy = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// `quiet` marks a poll: no spinner, no progress bar, and a failure that is not
// worth a toast — the page keeps showing what it had and tries again shortly.
// The filter drawer's state. Kept beside the search box rather than inside the
// drawer so the list can be narrowed while it is shut, and so the button can
// say how many categories are doing the narrowing.
const filterOpen = ref(false)
const filters = ref(emptyFilters())
const filterCount = computed(() => activeFilterCount(filters.value))

// Sent only when set, so an untouched filter leaves the query string as short
// as it was before any of this existed.
function filterParams() {
  const f = filters.value
  const out = {}
  if (f.buckets.length) out.buckets = f.buckets.join(',')
  if (f.protocols.length) out.protocols = f.protocols.join(',')
  if (f.interfaceIds.length) out.interfaceIds = f.interfaceIds.join(',')
  if (f.groups.length) out.groups = f.groups.join(',')
  if (f.expiryFrom) out.expiryFrom = f.expiryFrom
  if (f.expiryTo) out.expiryTo = f.expiryTo
  if (f.usedFromGB !== '') out.usedFromGB = f.usedFromGB
  if (f.usedToGB !== '') out.usedToGB = f.usedToGB
  if (f.renews) out.renews = f.renews
  if (f.hasNote) out.hasNote = f.hasNote
  return out
}

// A narrowed list is a different list: staying on page four of the old one
// would show an empty table and look like the filter matched nothing.
watch(filters, () => { currentPage.value = 1; load() }, { deep: true })

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const [p, o] = await Promise.all([
      api.clients(
        {
          search: search.value,
          status: ['active', 'disabled', 'expired', 'exhausted'].includes(statusFilter.value) ? statusFilter.value : '',
          buckets: statusFilter.value === 'depleted' ? 'exhausted,expired' : undefined,
          group: groupFilter.value,
          sort: sort.value,
          page: currentPage.value,
          perPage: pageSize.value,
          ...filterParams(),
        },
        { background: quiet },
      ),
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
    // reach a client the operator can no longer see.
    const visible = new Set(p.items.map((c) => c.id))
    selected.value = new Set([...selected.value].filter((id) => visible.has(id)))
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
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
})

function onDocClick(e) {
  if (moreOpen.value && !e.target.closest?.('.rowmenu') && !e.target.closest?.('.more-btn')) {
    moreOpen.value = null
  }
  if (cardMenu.value && !e.target.closest?.('.rowmenu') && !e.target.closest?.('.row-action-trigger')) {
    cardMenu.value = null
  }
}
function onKey(e) {
  if (e.key === 'Escape') { moreOpen.value = null; cardMenu.value = null }
}

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

const totalPages = computed(() =>
  page.value ? Math.max(1, Math.ceil(page.value.total / page.value.perPage)) : 1,
)
const hasGroups = computed(() => groupNames.value.length > 0)

// Their chips: one per active filter, each closable on its own.
const filterChips = computed(() => {
  const f = filters.value
  const out = []
  for (const b of f.buckets || []) out.push({ key: 'buckets', value: b, color: '', text: t(`filter.bucket.${b}`) })
  for (const p of f.protocols || []) out.push({ key: 'protocols', value: p, color: 'geekblue', text: t(`protocol.${p}`) })
  for (const id of f.interfaceIds || []) out.push({ key: 'interfaceIds', value: id, color: 'green', text: interfaces.value.find((i) => i.id === id)?.name || `#${id}` })
  for (const g of f.groups || []) out.push({ key: 'groups', value: g, color: 'geekblue', text: `${t('client.group')}: ${g}` })
  if (f.expiryFrom || f.expiryTo) out.push({ key: 'expiry', color: 'orange', text: `${t('client.expires')}: ${f.expiryFrom || '…'} → ${f.expiryTo || '…'}` })
  if (f.usedFromGB !== '' || f.usedToGB !== '') out.push({ key: 'usage', color: 'purple', text: `${t('client.traffic')}: ${f.usedFromGB || 0} → ${f.usedToGB || '∞'} GB` })
  if (f.renews) out.push({ key: 'renews', color: 'gold', text: `${t('client.resetCycle')}: ${f.renews === 'on' ? t('status.enabled') : t('status.disabled')}` })
  if (f.hasNote) out.push({ key: 'hasNote', color: '', text: `${t('client.note')}: ${f.hasNote === 'yes' ? t('filter.has') : t('filter.hasNot')}` })
  return out
})
function clearChip(chip) {
  const f = { ...filters.value }
  switch (chip.key) {
    case 'buckets':
    case 'protocols':
    case 'interfaceIds':
    case 'groups':
      f[chip.key] = f[chip.key].filter((v) => v !== chip.value)
      break
    case 'expiry':
      f.expiryFrom = ''
      f.expiryTo = ''
      break
    case 'usage':
      f.usedFromGB = ''
      f.usedToGB = ''
      break
    default:
      f[chip.key] = ''
  }
  filters.value = f
}

// The inbounds a customer is on, as their chips: one per server, coloured
// by protocol, three shown and the rest counted.
const INBOUND_CHIP_LIMIT = 1
function inboundChips(c) {
  const names = []
  const seen = new Set()
  for (const a of c.accounts || []) {
    if (seen.has(a.interfaceId)) continue
    seen.add(a.interfaceId)
    const i = interfaces.value.find((x) => x.id === a.interfaceId)
    names.push({ name: i?.name || `#${a.interfaceId}`, protocol: i?.protocol || c.protocol })
  }
  return names
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

const allSelected = computed(
  () => !!page.value?.items.length && selected.value.size === page.value.items.length,
)
function toggleAll(checked) {
  selected.value = checked ? new Set(page.value.items.map((c) => c.id)) : new Set()
}
function toggleOne(id, checked) {
  const next = new Set(selected.value)
  checked ? next.add(id) : next.delete(id)
  selected.value = next
}

// Online is what the reconciler sees moving right now -- bytes in the last
// window, counted every two seconds -- rather than a handshake written to
// the database a flush later. A device that connects shows within a poll.
const clientOnline = (c) => (c.onlineNow || 0) > 0
const usedPercent = (c) => percent(c.usedBytes, c.quotaBytes)

// Current throughput, derived from consecutive readings of the stored total.
// The panel has no per-second counter; two totals and the gap between them is
// the same arithmetic the overview already does for the host.
const lastSeen = new Map()
function speedOf(c) {
  const now = Date.now()
  const prev = lastSeen.get(c.id)
  lastSeen.set(c.id, { bytes: c.usedBytes, at: now })

  if (!prev || now === prev.at) return '—'
  const delta = c.usedBytes - prev.bytes
  // A reset makes the total go backwards; reporting a negative speed would be
  // worse than reporting none.
  if (delta <= 0) return '—'
  return `${bytes((delta * 1000) / (now - prev.at), store.locale)}/s`
}

// Their traffic bar: green while there is room, orange within the warning
// band, red when out; a disabled customer's bar is grey; unlimited is a
// purple wash.
function barColor(c) {
  if (c.status === 'disabled') return 'var(--faint)'
  if (!c.quotaBytes) return 'rgba(114, 46, 209, 0.35)'
  const p = usedPercent(c) ?? 0
  if (p >= 100) return 'var(--bad)'
  if (p >= DEPLETING_AT) return 'var(--warn)'
  return 'var(--ok)'
}
// The three cells below use the classic panel's own colour rules, so a row reads at a
// glance: purple is unlimited, green healthy, orange running low, red stopped.

function statusTag(c) {
  // Ended plans say why: out of data, or past their date. The switch beside
  // them is off, because the panel switched them off.
  if (c.status === 'exhausted') return { color: 'red', label: t('status.exhausted') }
  if (c.status === 'expired') return { color: 'red', label: t('status.expired') }
  if (c.status !== 'disabled' && clientOnline(c)) return { color: 'green', label: t('status.online'), dot: true }
  if (c.status === 'disabled') return { color: 'grey', label: t('status.disabled') }
  // A plan that has not started: on hold until the first connection.
  if (!c.expiresAt && c.startOnFirstUse && c.durationDays > 0 && !c.activatedAt) {
    return { color: 'blue', label: t('status.onHold') }
  }
  // "Running low" earns its own state: it is the moment to sell a renewal,
  // which is worth surfacing before the customer notices anything.
  const p = usedPercent(c)
  if (p != null && p >= DEPLETING_AT) return { color: 'orange', label: t('stat.depleting') }
  return { color: 'grey', label: t('status.offline') }
}

const DEPLETING_AT = 85

function remainingTag(c) {
  if (!c.quotaBytes) return { color: 'purple', label: '∞' }
  const p = usedPercent(c)
  const left = bytes(Math.max(0, c.quotaBytes - c.usedBytes), store.locale)
  if (p >= 100) return { color: 'red', label: left }
  if (p >= DEPLETING_AT) return { color: 'orange', label: left }
  return { color: 'green', label: left }
}

function expiryTag(c) {
  // A plan waiting for its first connection has no date yet. Showing the
  // unlimited mark here would read as "never expires", which is the opposite
  // of a thirty-day plan that simply has not started.
  if (!c.expiresAt && c.startOnFirstUse && c.durationDays > 0) {
    return { color: 'blue', label: `${c.durationDays}d`, title: t('client.notStartedHint') }
  }
  if (!c.expiresAt) return { color: 'purple', label: '∞' }
  const ms = new Date(c.expiresAt) - Date.now()
  const label = relative(c.expiresAt, store.locale)
  if (ms <= 0) return { color: 'red', label }
  if (ms < 3 * 86400e3) return { color: 'orange', label }
  return { color: 'green', label }
}

// The tooltip has to agree with the tag: "never expires" under a badge that
// says 30d would leave the operator with two different answers.
// Their tooltip on the status: when the customer was last seen.
function lastOnlineTitle(c) {
  let last = null
  for (const a of c.accounts || []) {
    if (a.lastHandshake && (!last || a.lastHandshake > last)) last = a.lastHandshake
  }
  return `${t('client.menu.lastOnline')}: ${last ? dateTime(last, store.locale) : '-'}`
}

function expiryTitle(c) {
  if (c.expiresAt) return dateTime(c.expiresAt, store.locale)
  if (c.startOnFirstUse && c.durationDays > 0) return t('client.notStartedHint')
  return t('client.neverExpires')
}

async function guard(fn, successKey) {
  try {
    await fn()
    if (successKey) notify(t(successKey), 'success')
    await Promise.all([load(), loadGroups()])
  } catch (err) {
    notify(err.message, 'error')
  }
}

// Rows with a change in flight, so the switch can spin on that row alone.
const pending = ref(new Set())

// Toggling one customer moves the switch immediately and patches that row from
// the reply. It used to await the request and then refetch the whole list and
// the group list, which meant the switch stayed where it was for the entire
// round trip — a control that does not move when clicked reads as broken, and
// the operator clicks it again.
async function setEnabled(c, on) {
  const was = c.status
  const next = on ? 'active' : 'disabled'
  if (was === next) return

  c.status = next                       // optimistic: the switch moves now
  pending.value = new Set(pending.value).add(c.id)

  try {
    const updated = await api.updateClient(c.id, { status: next })
    // Patched in place rather than reloading. Refetching would also reorder or
    // re-page the list under the operator's cursor.
    Object.assign(c, updated)
  } catch (err) {
    c.status = was                      // put it back where it was
    notify(err.message, 'error')
  } finally {
    release(c.id)
    // The row is right the moment the switch moves; the counts above it are
    // not, and they are what an operator checks to see the change took.
    settled()
  }
}

function hold(id) {
  pending.value = new Set(pending.value).add(id)
}
function release(id) {
  const next = new Set(pending.value)
  next.delete(id)
  pending.value = next
}
const isPending = (id) => pending.value.has(id)

// Zeroing one customer's counters. Fast on a small panel and not fast on a
// large one, and until it came back the button gave nothing at all — so the
// operator's second click reset the traffic they had just reset.
async function resetOne(c) {
  hold(c.id)
  try {
    await api.resetTraffic(c.id)
    c.rxBytes = 0
    c.txBytes = 0
    notify(t('client.trafficReset'), 'success')
    settled()
  } catch (err) {
    notify(err.message, 'error')
    await load()
  } finally {
    release(c.id)
  }
}
const ask = ref(null) // { title, body, subject, consequences, confirmLabel, requireText, run }

function confirmAnd(spec) {
  ask.value = spec
}

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

const removeOne = (c) =>
  confirmAnd({
    title: t('client.confirmDeleteTitle'),
    body: t('client.confirmDeleteBody'),
    subject: c.name,
    // Said because it is what an operator forgets: the customer's config stops
    // working the moment this runs, and reissuing it does not bring the old one
    // back.
    consequences: [
      tn('client.consequenceDevices', c.accounts?.length ?? 0),
      t('client.consequenceConfigs'),
      t('client.consequenceUsage'),
    ],
    confirmLabel: t('action.delete'),
    run: () => guard(() => api.deleteClient(c.id), 'client.deleted'),
  })

const ids = () => [...selected.value]

const bulk = (action) => {
  if (!selected.value.size) return
  const n = selected.value.size

  if (action === 'delete') {
    return confirmAnd({
      title: t('client.confirmDeleteManyTitle'),
      body: t('client.confirmDeleteBody'),
      subject: tn('client.nCustomers', n),
      consequences: [t('client.consequenceConfigs'), t('client.consequenceUsage')],
      confirmLabel: t('action.delete'),
      // Typed out, because a selection of forty is not something the eye
      // checks and this is the one action that cannot be walked back.
      requireText: n >= 5 ? String(n) : '',
      run: () => guard(async () => {
        await api.bulkClients(action, ids())
        selected.value = new Set()
      }, 'client.bulkDone'),
    })
  }

  return guard(async () => {
    await api.bulkClients(action, ids())
    selected.value = new Set()
  }, 'client.bulkDone')
}

// Moving a selection of customers onto a server, or off it.
//
// The answer names every customer it could not move and why — a device limit,
// an address pool with nothing left — because a count alone would leave an
// operator to work out which of three hundred people is still on the old
// server.
async function submitServers(kind) {
  const chosen = (form.value.interfaceIds || []).map(Number)
  if (!chosen.length) {
    notify(t('client.pickAServer'), 'error')
    return
  }

  const res = await api.post(`/api/clients/servers/${kind === 'attach' ? 'attach' : 'detach'}`, {
    ids: ids(),
    interfaceIds: chosen,
  })

  const failed = Object.entries(res?.failures || {})
  if (failed.length) {
    notify(failed.map(([name, why]) => `${name}: ${why}`).join('\n'), 'error')
  } else {
    notify(`${t('client.bulkDone')} — ${nf(res?.changed || 0)}`, 'success')
  }
  dialog.value = null
  selected.value = new Set()
  await load()
}

const ungroup = () =>
  guard(async () => {
    await api.assignGroup('', ids())
    selected.value = new Set()
  }, 'client.bulkDone')

function openMore(e) {
  if (moreOpen.value) {
    moreOpen.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  moreOpen.value = { rect: r, x: r.left, y: r.bottom + 4 }
}

// Their two menus: one for the page, one for a selection. Same items, same
// order, same dividers.
const moreItems = computed(() =>
  selected.value.size
    ? [
        { key: 'attach', label: t('client.menu.attach'), icon: 'UsergroupAddOutlined' },
        { key: 'detach', label: t('client.menu.detach'), icon: 'UsergroupDeleteOutlined', danger: true },
        { key: 'group', label: t('client.addToGroup'), icon: 'TagsOutlined' },
        { key: 'ungroup', label: t('client.ungroup'), icon: 'UngroupOutlined', danger: true },
        { divider: true },
        { key: 'enable', label: t('action.enable'), icon: 'CheckCircleOutlined' },
        { key: 'disable', label: t('action.disable'), icon: 'StopOutlined', danger: true },
        { key: 'adjust', label: t('client.adjust'), icon: 'ClockCircleOutlined' },
        { key: 'subLinks', label: t('client.menu.subLinks'), icon: 'LinkOutlined' },
      ]
    : [
        { key: 'batch', label: t('client.menu.bulk'), icon: 'UsergroupAddOutlined' },
        { key: 'export', label: t('client.export'), icon: 'DownloadOutlined' },
        { key: 'import', label: t('client.menu.import'), icon: 'UploadOutlined' },
        { key: 'resetAll', label: t('client.resetAll'), icon: 'RetweetOutlined' },
        { divider: true },
        { key: 'purgeDepleted', label: t('client.menu.delDepleted'), icon: 'RestOutlined', danger: true },
        { key: 'purgeUnattached', label: t('client.menu.delOrphans'), icon: 'DisconnectOutlined', danger: true },
      ],
)

// Import: a JSON list in the shape the export writes.
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
  const body = Array.isArray(parsed) ? { clients: parsed, onConflict: 'skip', interfaceId: interfaces.value[0]?.id } : parsed
  try {
    const rep = await api.post('/api/clients/import', body)
    notify(`${t('client.menu.import')}: ${nf(rep.created || 0)}`, 'success')
    importOpen.value = false
    importText.value = ''
    await load()
  } catch (err) {
    notify(err.message, 'error')
  }
}

// Sub links: every selected customer's subscription address, one per line.
const subLinksOpen = ref(false)
const subLinksText = computed(() =>
  (page.value?.items || [])
    .filter((c) => selected.value.has(c.id))
    .map((c) => c.subscriptionUrl || c.subscription?.url || `${c.name}: —`)
    .join('\n'),
)

function pickMore(key) {
  moreOpen.value = null
  if (key === 'attach' || key === 'detach' || key === 'group' || key === 'adjust') return openDialog(key)
  if (key === 'ungroup') return ungroup()
  if (key === 'enable' || key === 'disable') return bulk(key)
  if (key === 'subLinks') {
    subLinksOpen.value = true
    return
  }
  if (key === 'import') {
    importOpen.value = true
    return
  }
  if (key === 'purgeDepleted' || key === 'purgeUnattached') {
    const status = key === 'purgeDepleted' ? 'depleted' : 'unattached'
    const count = key === 'purgeDepleted' ? (stats.value?.exhausted ?? 0) + (stats.value?.expired ?? 0) : null
    return confirmAnd({
      title: t(key === 'purgeDepleted' ? 'client.menu.delDepletedTitle' : 'client.menu.delOrphansTitle'),
      body: t(key === 'purgeDepleted' ? 'client.menu.delDepletedBody' : 'client.menu.delOrphansBody'),
      subject: count == null ? '' : tn('client.nCustomers', count),
      consequences: [t('client.consequenceConfigs'), t('client.consequenceUsage')],
      confirmLabel: t('action.delete'),
      run: () => guard(() => api.purgeClients(status), 'client.deleted'),
    })
  }
  if (key === 'batch') {
    form.value.prefix = ''
    form.value.count = 10
    dialog.value = { kind: 'batch' }
    return
  }
  if (key === 'export') {
    api.downloadClients().catch((e) => notify(e.message, 'error'))
    return
  }
  if (key === 'resetAll') {
    return confirmAnd({
      title: t('client.confirmResetAllTitle'),
      body: t('client.confirmResetAllBody'),
      subject: tn('client.nCustomers', stats.value?.clients ?? 0),
      consequences: [t('client.consequenceReset')],
      confirmLabel: t('action.resetTraffic'),
      run: () => guard(() => api.resetAllTraffic(), 'client.bulkDone'),
    })
  }
  // The two purges are separate dialogs naming separate counts. They used to
  // share one sentence, so the dialog could not tell an operator which of them
  // they had picked.
  if (key === 'purgeExhausted' || key === 'purgeExpired') {
    const status = key === 'purgeExhausted' ? 'exhausted' : 'expired'
    const count = (key === 'purgeExhausted' ? stats.value?.exhausted : stats.value?.expired) ?? 0
    return confirmAnd({
      title: t(key === 'purgeExhausted' ? 'client.confirmPurgeExhaustedTitle' : 'client.confirmPurgeExpiredTitle'),
      body: t('client.confirmPurgeBody'),
      subject: tn('client.nCustomers', count),
      consequences: [t('client.consequenceConfigs'), t('client.consequenceUsage')],
      confirmLabel: t('action.delete'),
      requireText: count >= 5 ? String(count) : '',
      run: () => guard(() => api.purgeClients(status), 'client.deleted'),
    })
  }
}

function openDialog(kind) {
  if (kind === 'group') form.value.group = ''
  if (kind === 'adjust') {
    form.value.addDays = ''
    form.value.quotaGB = ''
    form.value.resetCycle = ''
  }
  // Cleared each time it opens: a list of ticks left over from the last use
  // would be a bulk action applied to servers nobody chose this time.
  if (kind === 'attach' || kind === 'detach') form.value.interfaceIds = []
  dialog.value = { kind }
}

async function submitDialog() {
  busy.value = true
  try {
    const d = dialog.value
    if (d.kind === 'attach' || d.kind === 'detach') {
      await submitServers(d.kind)
      return
    }
    if (d.kind === 'group') {
      const res = await api.assignGroup(form.value.group.trim(), ids())
      notify(`${t('client.bulkDone')} — ${nf(res.affected)}`, 'success')
      selected.value = new Set()
    } else if (d.kind === 'adjust') {
      const payload = { ids: ids() }
      if (form.value.addDays !== '') payload.addDays = unitToHours(form.value.addDays, form.value.addUnit) / 24
      if (form.value.quotaGB !== '') payload.quotaBytes = unitToBytes(form.value.quotaGB, form.value.quotaUnit)
      if (form.value.resetCycle) payload.resetCycle = form.value.resetCycle
      const res = await api.adjustClients(payload)
      notify(`${t('client.bulkDone')} — ${nf(res.affected)}`, 'success')
      selected.value = new Set()
    } else if (d.kind === 'batch') {
      const iface = interfaces.value[0]
      const res = await api.createBatch({
        prefix: form.value.prefix.trim(),
        count: Number(form.value.count),
        start: 1,
        interfaceId: iface?.id,
        deviceLimit: 1,
        quotaBytes: unitToBytes(form.value.quotaGB, form.value.quotaUnit),
        resetCycle: 'none',
        deviceNames: [],
      })
      notify(`${t('client.created')} — ${nf(res.created)}`, 'success')
    }
    dialog.value = null
    await Promise.all([load(), loadGroups()])
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
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
  <!-- Their summary Card: size="small", six Statistics with a coloured dot. -->
  <div v-if="stats" class="acard small summary-card">
    <div class="acard-body">
      <div class="arow six">
        <div class="acol">
          <div class="stat-title">{{ t('nav.clients') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(stats.clients) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('status.online') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-blue"></i></span><span>{{ nf(stats.online) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('stat.depleted') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-red"></i></span><span>{{ nf((stats.exhausted || 0) + (stats.expired || 0)) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('stat.depleting') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-orange"></i></span><span>{{ nf(stats.depleting) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('status.disabled') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-gray"></i></span><span>{{ nf(stats.disabled) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('status.active') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-green"></i></span><span>{{ nf(stats.active) }}</span></div>
        </div>
      </div>
    </div>
  </div>

  <div v-if="!interfaces.length" class="aalert warning">
    <AntIcon name="ExclamationCircleFilled" />
    <div class="aalert-body"><span class="aalert-title">{{ t('interface.noneYet') }} <a href="#" @click.prevent="router.push('/interfaces')">{{ t('interface.create') }}</a></span></div>
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
        <button class="abtn more-btn" :aria-expanded="!!moreOpen" @click="openMore">
          <AntIcon name="MoreOutlined" /><span v-if="!isMobile">{{ t('outbound.more') }}</span>
        </button>
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
      <div v-if="filterChips.length" class="filter-chips">
        <span v-for="(chip, i) in filterChips" :key="i" class="atag closable" :class="chip.color">
          {{ chip.text }}
          <button type="button" class="atag-close" :aria-label="t('action.remove')" @click="clearChip(chip)"><AntIcon name="CloseOutlined" /></button>
        </span>
      </div>

      <PageSpin v-if="showSkeleton" />
      <div v-else-if="loading && !page" class="empty"></div>

      <div v-else-if="isMobile" class="client-cards" :class="{ stale: refiltering }">
        <div v-if="page && page.items.length" class="card-bulk-bar">
          <label class="acheckbox">
            <input type="checkbox" class="acheck" :checked="allSelected" @change="toggleAll($event.target.checked)" />
            <span>{{ t('action.selectAll') }}</span>
          </label>
          <span v-if="selected.size" class="bulk-count">{{ nf(selected.size) }}</span>
        </div>
        <div v-if="!page || !page.items.length" class="card-empty">
          <AntIcon name="TeamOutlined" :size="28" style="opacity: 0.5" />
          <div>{{ t('common.nothingYet') }}</div>
        </div>
        <div v-if="page && page.total > pageSize" class="card-pagination">
          <ul class="apagination small">
            <li class="apagination-total">{{ nf(page.total) }}</li>
            <li><button class="apage" :disabled="currentPage <= 1" :aria-label="t('action.prev')" @click="goPage(currentPage - 1)"><AntIcon name="LeftOutlined" /></button></li>
            <li v-for="(it, i) in pageItems" :key="i">
              <button v-if="typeof it === 'number'" class="apage" :class="{ active: it === currentPage }" @click="goPage(it)">{{ it }}</button>
              <button v-else class="apage jump" @click="goPage(it === 'prev' ? Math.max(1, currentPage - 5) : Math.min(totalPages, currentPage + 5))">•••</button>
            </li>
            <li><button class="apage" :disabled="currentPage >= totalPages" :aria-label="t('action.next')" @click="goPage(currentPage + 1)"><AntIcon name="RightOutlined" /></button></li>
          </ul>
        </div>
        <div v-for="c in page?.items || []" :key="c.id" class="client-card" :class="{ 'is-selected': selected.has(c.id) }">
          <div class="card-head">
            <input type="checkbox" class="acheck" :checked="selected.has(c.id)" :aria-label="c.name" @change="toggleOne(c.id, $event.target.checked)" />
            <i v-if="statusTag(c).dot" class="online-dot" style="margin-inline-end: 0"></i>
            <i v-else class="abadge-dot" :class="statusTag(c).color"></i>
            <span class="tag-name">{{ c.name }}</span>
            <span v-if="c.status === 'exhausted' || c.status === 'expired'" class="atag red status-tag">{{ t('stat.depleted') }}</span>
            <span v-else-if="remainingTag(c).color === 'orange' || expiryTag(c).color === 'orange'" class="atag orange status-tag">{{ t('stat.depleting') }}</span>
            <div class="card-actions">
              <button type="button" class="row-action-trigger" :aria-label="t('client.menu.clientInfo')" @click="infoFor = c"><AntIcon name="InfoCircleOutlined" /></button>
              <Toggle :model-value="c.status === 'active'" :label="c.name" small :disabled="c.status === 'expired' || c.status === 'exhausted'" :loading="isPending(c.id)" @update:model-value="(v) => setEnabled(c, v)" />
              <button type="button" class="row-action-trigger" :aria-label="t('action.more')" :aria-expanded="cardMenu?.client?.id === c.id" @click="openCardMenu(c, $event)"><AntIcon name="MoreOutlined" /></button>
            </div>
          </div>
          <span v-if="c.note" class="client-card-comment">{{ c.note }}</span>
          <div class="client-traffic-cell is-compact" :class="{ 'is-unlimited': !c.quotaBytes }">
            <span class="client-traffic-cell-used ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
            <span class="aprogress client-traffic-cell-bar"><span :style="{ width: (c.quotaBytes ? Math.min(100, usedPercent(c) ?? 0) : 100) + '%', background: barColor(c) }"></span></span>
            <span class="client-traffic-cell-limit ltr">
              <span v-if="!c.quotaBytes" class="client-traffic-cell-infinity">∞</span>
              <template v-else>{{ bytes(c.quotaBytes, store.locale) }}</template>
            </span>
          </div>
          <div v-if="speedOf(c) !== '—'" class="client-card-speed"><span class="atag blue ltr" style="margin: 0">{{ speedOf(c) }}</span></div>
        </div>
        <Teleport to="body">
          <div v-if="cardMenu" v-fit="cardMenu.rect" class="rowmenu" role="menu" :style="{ top: cardMenu.y + 'px', left: cardMenu.x + 'px' }">
            <button class="menu-item" role="menuitem" @click="cardAction('qr')"><AntIcon name="QrcodeOutlined" />{{ t('client.qrCode') }}</button>
            <button class="menu-item" role="menuitem" @click="cardAction('reset')"><AntIcon name="RetweetOutlined" />{{ t('outbound.resetTraffic') }}</button>
            <button class="menu-item" role="menuitem" @click="cardAction('edit')"><AntIcon name="EditOutlined" />{{ t('action.edit') }}</button>
            <button class="menu-item danger" role="menuitem" @click="cardAction('delete')"><AntIcon name="DeleteOutlined" />{{ t('action.delete') }}</button>
          </div>
        </Teleport>
      </div>

      <template v-else>
        <div class="atable-wrap" :class="{ stale: refiltering }" style="margin-top: 0">
          <table class="atable small" style="min-width: 1200px">
            <thead>
              <tr>
                <th class="sel"><input type="checkbox" class="acheck" :checked="allSelected" :aria-label="t('action.selectAll')" @change="toggleAll($event.target.checked)" /></th>
                <th style="width: 200px">{{ t('table.actions') }}</th>
                <th style="width: 80px">{{ t('table.enabled') }}</th>
                <th style="width: 90px">{{ t('status.online') }}</th>
                <th style="width: 220px">{{ t('client.menu.client') }}</th>
                <th v-if="hasGroups" style="width: 130px">{{ t('client.group') }}</th>
                <th style="width: 170px">{{ t('client.attachedInbounds') }}</th>
                <th style="width: 300px">{{ t('client.traffic') }}</th>
                <th class="center" style="width: 216px">{{ t('client.speed') }}</th>
                <th style="width: 130px">{{ t('client.remaining') }}</th>
                <th style="width: 130px">{{ t('client.menu.duration') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!page || !page.items.length" class="empty-row">
                <td :colspan="hasGroups ? 11 : 10">
                  <div class="clients-empty">
                    <AntIcon name="TeamOutlined" :size="32" />
                    <div>{{ t('common.nothingYet') }}</div>
                  </div>
                </td>
              </tr>
              <tr v-for="c in page.items" :key="c.id" :class="{ picked: selected.has(c.id) }">
                <td class="sel"><input type="checkbox" class="acheck" :checked="selected.has(c.id)" :aria-label="c.name" @change="toggleOne(c.id, $event.target.checked)" /></td>
                <td>
                  <div class="aspace" style="gap: 4px; flex-wrap: nowrap">
                    <button class="abtn text sm" :title="t('client.qrCode')" :aria-label="t('client.qrCode')" @click="shareFor = c"><AntIcon name="QrcodeOutlined" /></button>
                    <button class="abtn text sm" :title="t('client.menu.clientInfo')" :aria-label="t('client.menu.clientInfo')" @click="infoFor = c"><AntIcon name="InfoCircleOutlined" /></button>
                    <button class="abtn text sm" :title="t('outbound.resetTraffic')" :aria-label="t('outbound.resetTraffic')" :disabled="isPending(c.id)" @click="resetOne(c)"><AntIcon name="RetweetOutlined" /></button>
                    <button class="abtn text sm" :title="t('action.edit')" :aria-label="t('action.edit')" @click="formFor = { client: c }"><AntIcon name="EditOutlined" /></button>
                    <button class="abtn text sm danger" :title="t('action.delete')" :aria-label="t('action.delete')" @click="removeOne(c)"><AntIcon name="DeleteOutlined" /></button>
                  </div>
                </td>
                <td>
                  <Toggle :model-value="c.status === 'active'" :label="c.name" small :disabled="c.status === 'expired' || c.status === 'exhausted'" :loading="isPending(c.id)" @update:model-value="(v) => setEnabled(c, v)" />
                </td>
                <td>
                  <span class="atag" :class="statusTag(c).color === 'grey' ? '' : statusTag(c).color" :title="lastOnlineTitle(c)" style="margin: 0">
                    <i v-if="statusTag(c).dot" class="online-dot"></i>{{ statusTag(c).label }}
                  </span>
                </td>
                <td>
                  <div class="email-cell">
                    <span class="email">{{ c.name }}</span>
                    <!-- Connections in use right now against what the plan allows at once,
                         the way the classic panel shows its IP count: a file on two devices
                         used one at a time is one connection; both at once is two. -->
                    <span class="sub ltr" :class="{ over: c.deviceLimit > 0 && (c.onlineNow || 0) > c.deviceLimit }"
                          :title="t('client.connectionsNow', { n: c.onlineNow || 0, limit: c.deviceLimit, files: c.accounts?.length ?? 0 })">
                      {{ c.onlineNow || 0 }} / {{ c.deviceLimit || '∞' }}
                    </span>
                    <span v-if="c.note" class="sub" :title="c.note">{{ c.note }}</span>
                  </div>
                </td>
                <td v-if="hasGroups">
                  <span v-if="c.group" class="atag geekblue" :style="{ margin: 0, cursor: 'pointer', opacity: groupFilter === c.group ? 0.6 : 1 }" @click="groupFilter = c.group">{{ c.group }}</span>
                  <span v-else class="cell-empty">—</span>
                </td>
                <td>
                  <template v-if="inboundChips(c).length">
                    <span v-for="ib in inboundChips(c).slice(0, INBOUND_CHIP_LIMIT)" :key="ib.name" class="atag" :class="ib.protocol === 'openvpn' ? 'orange' : 'gold'" style="margin: 2px" :title="ib.name">{{ ib.name }}</span>
                    <span v-if="inboundChips(c).length > INBOUND_CHIP_LIMIT" class="atag default" style="margin: 2px; cursor: pointer" :title="inboundChips(c).slice(INBOUND_CHIP_LIMIT).map((x) => x.name).join(', ')">+{{ inboundChips(c).length - INBOUND_CHIP_LIMIT }}</span>
                  </template>
                  <span v-else class="cell-empty">—</span>
                </td>
                <td>
                  <div class="client-traffic-cell" :class="{ 'is-unlimited': !c.quotaBytes }" :title="`↑ ${bytes(c.upBytes || 0, store.locale)}  ↓ ${bytes(c.downBytes || 0, store.locale)}`">
                    <span class="client-traffic-cell-used ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
                    <span class="aprogress client-traffic-cell-bar"><span :style="{ width: (c.quotaBytes ? Math.min(100, usedPercent(c) ?? 0) : 100) + '%', background: barColor(c) }"></span></span>
                    <span class="client-traffic-cell-limit ltr">
                      <span v-if="!c.quotaBytes" class="client-traffic-cell-infinity">∞</span>
                      <template v-else>{{ bytes(c.quotaBytes, store.locale) }}</template>
                    </span>
                  </div>
                </td>
                <td class="center"><span class="atag speed-tag ltr" :class="speedOf(c) ? 'blue' : ''">{{ speedOf(c) || '—' }}</span></td>
                <td><span class="atag ltr" :class="remainingTag(c).color" style="margin: 0">{{ remainingTag(c).label }}</span></td>
                <td><span class="atag ltr" :class="expiryTag(c).color" :title="expiryTitle(c)" style="margin: 0">{{ expiryTag(c).label }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Their pagination: total, the pages, and a size changer past ten rows. -->
        <ul v-if="page && page.total > pageSize" class="apagination">
          <li class="apagination-total">{{ nf(page.total) }}</li>
          <li><button class="apage" :disabled="currentPage <= 1" :aria-label="t('action.prev')" @click="goPage(currentPage - 1)"><AntIcon name="LeftOutlined" /></button></li>
          <li v-for="(it, i) in pageItems" :key="i">
            <button v-if="typeof it === 'number'" class="apage" :class="{ active: it === currentPage }" @click="goPage(it)">{{ it }}</button>
            <button v-else class="apage jump" :aria-label="it === 'prev' ? t('action.prev') : t('action.next')" @click="goPage(it === 'prev' ? Math.max(1, currentPage - 5) : Math.min(totalPages, currentPage + 5))">•••</button>
          </li>
          <li><button class="apage" :disabled="currentPage >= totalPages" :aria-label="t('action.next')" @click="goPage(currentPage + 1)"><AntIcon name="RightOutlined" /></button></li>
          <li v-if="page.total > 10" class="aselect apage-size"><select :value="pageSize" :aria-label="t('client.pageSize')" @change="setPageSize($event.target.value)">
            <option v-for="n in PAGE_SIZES" :key="n" :value="n">{{ n }} / {{ t('client.perPage') }}</option>
          </select></li>
        </ul>
      </template>
    </div>
  </div>
  </div>

  <div v-if="importOpen" class="modal-backdrop" @click.self="importOpen = false">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ci-title">
      <div class="card-head">
        <h2 id="ci-title">{{ t('client.menu.import') }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="importOpen = false"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field"><textarea v-model="importText" class="ltr mono" rows="12" spellcheck="false" placeholder="[ { ... } ]"></textarea></div>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="importOpen = false">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="!importText.trim()" @click="runImport">{{ t('client.menu.import') }}</button>
      </div>
    </div>
  </div>

  <div v-if="subLinksOpen" class="modal-backdrop" @click.self="subLinksOpen = false">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="sl-title">
      <div class="card-head">
        <h2 id="sl-title">{{ t('client.menu.subLinks') }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="subLinksOpen = false"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field"><textarea class="ltr mono" rows="10" readonly spellcheck="false" :value="subLinksText"></textarea></div>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="subLinksOpen = false">{{ t('common.close') }}</button>
      </div>
    </div>
  </div>

  <Teleport to="body">
    <div v-if="moreOpen" v-fit="moreOpen.rect" class="amenu" role="menu" :style="{ top: moreOpen.y + 'px', left: moreOpen.x + 'px' }">
      <template v-for="(m, i) in moreItems" :key="m.key || `d${i}`">
        <hr v-if="m.divider" class="amenu-divider" />
        <button v-else class="amenu-item" :class="{ danger: m.danger }" role="menuitem" @click="pickMore(m.key)">
          <AntIcon :name="m.icon" /><span>{{ m.label }}</span>
        </button>
      </template>
    </div>
  </Teleport>

  <div v-if="dialog" class="modal-backdrop" @click.self="dialog = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="cd-title">
      <div class="card-head">
        <h2 id="cd-title">
          {{ dialog.kind === 'group' ? t('client.addToGroup')
            : dialog.kind === 'adjust' ? t('client.adjust')
            : dialog.kind === 'attach' ? t('client.attachServers')
            : dialog.kind === 'detach' ? t('client.detachServers') : t('client.batchAdd') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="dialog = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="cd-form" class="card-body" @submit.prevent="submitDialog">
        <p v-if="dialog.kind !== 'batch'" class="target muted small">
          {{ t('action.selected') }}: <b>{{ nf(selected.size) }}</b>
        </p>

        <div v-if="dialog.kind === 'group'" class="field">
          <label for="cd-group">{{ t('client.group') }}</label>
          <input id="cd-group" v-model="form.group" list="cd-groups" :placeholder="t('client.groupPlaceholder')" autofocus />
          <datalist id="cd-groups">
            <option v-for="g in groupNames" :key="g" :value="g" />
          </datalist>
          <span class="hint">{{ t('client.groupHint') }}</span>
        </div>

        <!-- Adding keeps what a customer already has; only taking away
             removes anything. Said on the dialog, because "attach" and
             "detach" do not say it and the difference is the whole point of
             selling more than one server. -->
        <template v-else-if="dialog.kind === 'attach' || dialog.kind === 'detach'">
          <div class="field">
            <label>{{ dialog.kind === 'attach' ? t('client.attachServers') : t('client.detachServers') }}</label>
            <div class="srv-list">
              <label v-for="i in interfaces" :key="i.id" class="srv-item">
                <input v-model="form.interfaceIds" type="checkbox" :value="i.id" />
                <span class="srv-name">{{ i.name }}</span>
                <span class="srv-meta ltr">{{ i.protocol }} · {{ i.endpointHost }}</span>
              </label>
            </div>
            <span class="hint">
              {{ dialog.kind === 'attach' ? t('client.attachHint') : t('client.detachHint') }}
            </span>
          </div>
        </template>

        <template v-else-if="dialog.kind === 'adjust'">
          <div class="field">
            <label for="cd-days">{{ t('group.extend') }}</label>
            <div class="unit-field">
              <input id="cd-days" v-model="form.addDays" type="number" min="0" step="any" inputmode="decimal" :placeholder="t('client.leaveBlank')" autofocus />
              <select v-model="form.addUnit" class="unit-select" :aria-label="t('client.expiresUnit')">
                <option value="hours">{{ t('unit.hours') }}</option>
                <option value="days">{{ t('unit.days') }}</option>
                <option value="months">{{ t('unit.months') }}</option>
              </select>
            </div>
            <span class="hint">{{ t('group.extendHint') }}</span>
          </div>
          <div class="field">
            <label for="cd-quota">{{ t('client.quota') }}</label>
            <div class="unit-field">
              <input id="cd-quota" v-model="form.quotaGB" type="number" min="0" step="any" inputmode="decimal" :placeholder="t('client.leaveBlank')" />
              <select v-model="form.quotaUnit" class="unit-select" :aria-label="t('client.quotaUnit')">
                <option value="MB">MB</option><option value="GB">GB</option><option value="TB">TB</option>
              </select>
            </div>
          </div>
          <div class="field">
            <label for="cd-cycle">{{ t('client.resetCycle') }}</label>
            <select id="cd-cycle" v-model="form.resetCycle">
              <option value="">{{ t('client.leaveBlank') }}</option>
              <option value="none">{{ t('reset.none') }}</option>
              <option value="daily">{{ t('reset.daily') }}</option>
              <option value="weekly">{{ t('reset.weekly') }}</option>
              <option value="monthly">{{ t('reset.monthly') }}</option>
            </select>
          </div>
        </template>

        <template v-else>
          <div class="grid-2">
            <div class="field">
              <label for="cd-prefix"><span class="req">*</span>{{ t('client.batchPrefix') }}</label>
              <input id="cd-prefix" v-model="form.prefix" placeholder="batch-sep" required autofocus />
            </div>
            <div class="field">
              <label for="cd-count"><span class="req">*</span>{{ t('client.batchCount') }}</label>
              <input id="cd-count" v-model="form.count" type="number" min="1" max="200" required />
            </div>
          </div>
          <div class="field">
            <label for="cd-bquota">{{ t('client.quota') }}</label>
            <div class="unit-field">
              <input id="cd-bquota" v-model="form.quotaGB" type="number" min="0" step="any" inputmode="decimal" :placeholder="t('client.unlimited')" />
              <select v-model="form.quotaUnit" class="unit-select" :aria-label="t('client.quotaUnit')">
                <option value="MB">MB</option><option value="GB">GB</option><option value="TB">TB</option>
              </select>
            </div>
            <span class="hint">{{ t('client.batchHint') }}</span>
          </div>
        </template>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="dialog = null">{{ t('action.cancel') }}</button>
        <button type="submit" form="cd-form" class="btn primary" :disabled="busy">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>

  <ClientForm
    v-if="formFor"
    :interfaces="interfaces"
    :client="formFor.client"
    @close="formFor = null"
    @submit="submitForm"
  />
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

<style scoped>
/* the classic panel's ClientsPage.css, measured as it is. */
.arow.six > .acol { flex: 0 0 16.6667%; max-width: 16.6667%; }
@media (max-width: 991px) { .arow.six > .acol { flex: 0 0 33.3333%; max-width: 33.3333%; } }
@media (max-width: 575px) { .arow.six > .acol { flex: 0 0 50%; max-width: 50%; } }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 4px; vertical-align: middle; }
.dot-green { background: var(--ok); }
.dot-blue { background: var(--accent); }
.dot-red { background: var(--bad); }
.dot-orange { background: var(--warn); }
.dot-gray { background: var(--faint); }

.acard.small .acard-head { min-height: 38px; padding: 0 12px; }
.acard.small .acard-body { padding: 12px; }
.card-toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; width: 100%; padding: 6px 0; }
.unit-select { flex: 0 0 auto; width: auto; min-width: 72px; padding-inline: 8px; }
.filter-bar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 12px; }
.filter-count { margin-inline-start: auto; color: var(--muted); font-size: 13px; white-space: nowrap; }
.filter-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 0 0 12px; padding: 6px 8px; background: var(--surface-2); border-radius: 8px; }
.filter-chips .atag { margin: 0; }
.abadge-wrap { position: relative; display: inline-block; }
.abadge { position: absolute; top: 4px; inset-inline-end: 4px; transform: translate(50%, -50%); min-width: 16px; height: 16px; padding: 0 4px; border-radius: 8px; background: var(--bad); color: #fff; font-size: 12px; line-height: 16px; text-align: center; box-shadow: 0 0 0 1px var(--surface); }
.filter-bar .sort-select { width: auto; }
.sort-select .aselect-suffix { position: absolute; inset-inline-end: 11px; top: 50%; transform: translateY(-50%); color: var(--faint); font-size: 12px; pointer-events: none; }
.sort-select::after { display: none; }
.sort-select select { padding-inline-end: 28px; }

.atable th.center, .atable td.center { text-align: center; }
.atable-wrap.stale { opacity: 0.6; }
.email-cell { display: flex; flex-direction: column; }
.email-cell .email { font-weight: 500; }
.email-cell .sub.over { color: var(--danger, #e5484d); font-weight: 600; }
.email-cell .sub { font-size: 11px; opacity: 0.55; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 220px; }
.cell-empty { color: var(--faint); }
.online-dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; margin-inline-end: 5px; vertical-align: middle; background: var(--ok); animation: online-blink 1.1s ease-in-out infinite; }
@keyframes online-blink { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
.speed-tag { display: inline-flex; width: 200px; align-items: center; justify-content: center; margin-inline-end: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; box-sizing: border-box; }

/* Their ClientTrafficCell: a pill with the figures either side of a bar. */
.client-traffic-cell { display: flex; align-items: center; gap: 8px; width: 100%; min-width: 0; box-sizing: border-box; padding: 2px 10px; border-radius: 999px; background: var(--surface-2); }
.client-traffic-cell-used, .client-traffic-cell-limit { flex: 0 0 72px; min-width: 72px; font-size: 12px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.client-traffic-cell-used { text-align: end; color: var(--ink); }
.client-traffic-cell-limit { text-align: start; color: var(--muted); }
.client-traffic-cell-bar { flex: 1 1 60px; min-width: 48px; }
.client-traffic-cell.is-unlimited .client-traffic-cell-bar > span { border: 1px solid rgba(114, 46, 209, 0.55); }
.client-traffic-cell-infinity { display: inline-flex; align-items: center; color: var(--tag-purple-ink); font-size: 14px; line-height: 1; }

.clients-empty { padding: 32px 0; text-align: center; color: var(--muted); }
.clients-empty .anticon { display: block; margin: 0 auto 8px; }

.apagination { align-items: center; }
.apagination-total { display: inline-flex; align-items: center; height: 32px; margin-inline-end: 8px; color: var(--ink); font-size: 14px; }
.apage.jump { border-color: transparent; background: transparent; letter-spacing: 2px; color: var(--faint); }
.apage-size { height: 32px; min-width: 100px; }
.apage-size select { height: 30px; }
.apage .anticon { font-size: 12px; }
</style>
