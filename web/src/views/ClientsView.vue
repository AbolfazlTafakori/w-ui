<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api } from '../lib/api.js'
import { useLive, mergeRows, useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes, relative, dateTime, percent, gigabytesToBytes, isOnline } from '../lib/format.js'
import ClientForm from '../components/ClientForm.vue'
import ShareDialog from '../components/ShareDialog.vue'
import Toggle from '../components/Toggle.vue'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import FilterDrawer, { emptyFilters, activeFilterCount } from '../components/FilterDrawer.vue'

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

const formFor = ref(null)
const shareFor = ref(null)
const dialog = ref(null) // { kind }
const form = ref({ group: '', addDays: '', quotaGB: '', resetCycle: '', prefix: '', count: 10 })
const selected = ref(new Set())
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
          perPage: 25,
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
  every: 5000,
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
}
function onKey(e) {
  if (e.key === 'Escape') moreOpen.value = null
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
const activeFilters = computed(
  () => [search.value, statusFilter.value, groupFilter.value].filter(Boolean).length,
)

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
const INBOUND_CHIP_LIMIT = 3
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

const strip = computed(() => {
  const s = stats.value
  if (!s) return []
  // `filter` is the status this figure narrows the table to. An empty string
  // means "all of them"; null means the figure is not a filter at all.
  //
  // Online and running-low are the two that are not: neither is a stored
  // status -- one is derived from recent handshakes, the other from usage
  // against quota -- so there is nothing to ask the server for. They used to
  // be buttons anyway, which meant clicking either of them silently cleared
  // whatever filter you had set.
  return [
    { key: 'clients', value: s.clients, tone: 'ink', filter: '' },
    { key: 'online', value: s.online, tone: 'ok', filter: null },
    { key: 'depleting', value: s.depleting, tone: 'warn', filter: null },
    { key: 'exhausted', value: s.exhausted, tone: 'bad', filter: 'exhausted' },
    { key: 'expired', value: s.expired, tone: 'bad', filter: 'expired' },
    { key: 'disabled', value: s.disabled, tone: 'muted', filter: 'disabled' },
    { key: 'active', value: s.active, tone: 'ok', filter: 'active' },
  ]
})

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

const clientOnline = (c) => (c.accounts || []).some((a) => isOnline(a.lastHandshake))
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

function meterClass(p) {
  if (p == null) return ''
  if (p >= 100) return 'bad'
  if (p >= 85) return 'warn'
  return ''
}

// The three cells below use 3x-ui's own colour rules, so a row reads at a
// glance: purple is unlimited, green healthy, orange running low, red stopped.

function statusTag(c) {
  if (c.status === 'exhausted' || c.status === 'expired') return { color: 'red', label: t('stat.depleted') }
  if (c.status !== 'disabled' && clientOnline(c)) return { color: 'green', label: t('status.online'), dot: true }
  if (c.status === 'disabled') return { color: 'grey', label: t('status.disabled') }
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
  moreOpen.value = { x: r.left, y: r.bottom + 4 }
}

// Their two menus: one for the page, one for a selection. Same items, same
// order, same dividers.
const moreItems = computed(() =>
  selected.value.size
    ? [
        { key: 'attach', label: t('client.menu.attach'), icon: 'users' },
        { key: 'detach', label: t('client.menu.detach'), icon: 'users', danger: true },
        { key: 'group', label: t('client.addToGroup'), icon: 'tag' },
        { key: 'ungroup', label: t('client.ungroup'), icon: 'tag', danger: true },
        { divider: true },
        { key: 'enable', label: t('action.enable'), icon: 'check' },
        { key: 'disable', label: t('action.disable'), icon: 'close', danger: true },
        { key: 'adjust', label: t('client.adjust'), icon: 'clock' },
        { key: 'subLinks', label: t('client.menu.subLinks'), icon: 'link' },
      ]
    : [
        { key: 'batch', label: t('client.menu.bulk'), icon: 'users' },
        { key: 'export', label: t('client.export'), icon: 'download' },
        { key: 'import', label: t('client.menu.import'), icon: 'upload' },
        { key: 'resetAll', label: t('client.resetAll'), icon: 'refresh' },
        { divider: true },
        { key: 'purgeDepleted', label: t('client.menu.delDepleted'), icon: 'trash', danger: true },
        { key: 'purgeUnattached', label: t('client.menu.delOrphans'), icon: 'trash', danger: true },
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
      if (form.value.addDays !== '') payload.addDays = Number(form.value.addDays)
      if (form.value.quotaGB !== '') payload.quotaBytes = gigabytesToBytes(form.value.quotaGB)
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
        quotaBytes: gigabytesToBytes(form.value.quotaGB),
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
  const payload = { ...input, quotaBytes: gigabytesToBytes(input.quotaGB), quotaGB: undefined }
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

  <!-- Their summary Card: six figures with a title over each. -->
  <div v-if="stats" class="card summary-card">
    <div class="summary-grid">
      <div class="stat">
        <div class="stat-title">{{ t('nav.clients') }}</div>
        <div class="stat-value"><Icon name="users" :size="18" class="stat-icon" />{{ nf(stats.clients) }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">{{ t('status.online') }}</div>
        <div class="stat-value"><i class="dot dot-blue"></i>{{ nf(stats.online) }}</div>
      </div>
      <button type="button" class="stat pressable" :class="{ on: statusFilter === 'depleted' }" @click="statusFilter = statusFilter === 'depleted' ? '' : 'depleted'">
        <div class="stat-title">{{ t('stat.depleted') }}</div>
        <div class="stat-value"><i class="dot dot-red"></i>{{ nf((stats.exhausted || 0) + (stats.expired || 0)) }}</div>
      </button>
      <div class="stat">
        <div class="stat-title">{{ t('stat.depleting') }}</div>
        <div class="stat-value"><i class="dot dot-orange"></i>{{ nf(stats.depleting) }}</div>
      </div>
      <button type="button" class="stat pressable" :class="{ on: statusFilter === 'disabled' }" @click="statusFilter = statusFilter === 'disabled' ? '' : 'disabled'">
        <div class="stat-title">{{ t('status.disabled') }}</div>
        <div class="stat-value"><i class="dot dot-gray"></i>{{ nf(stats.disabled) }}</div>
      </button>
      <button type="button" class="stat pressable" :class="{ on: statusFilter === 'active' }" @click="statusFilter = statusFilter === 'active' ? '' : 'active'">
        <div class="stat-title">{{ t('status.active') }}</div>
        <div class="stat-value"><i class="dot dot-green"></i>{{ nf(stats.active) }}</div>
      </button>
    </div>
  </div>

  <div v-if="!interfaces.length" class="banner warn">
    <Icon name="alert" :size="17" />
    <span>
      {{ t('interface.noneYet') }}
      <a href="#" @click.prevent="router.push('/interfaces')">{{ t('interface.create') }}</a>
    </span>
  </div>

  <div class="card">
    <!-- Their Card title: Add Clients and more, or the selection count, more
         and a Delete pushed to the right. -->
    <div class="card-head">
      <div class="card-toolbar">
        <button
          v-if="!selected.size"
          class="btn primary"
          :disabled="!interfaces.length"
          :title="interfaces.length ? '' : t('interface.noneYet')"
          @click="formFor = {}"
        >
          <Icon name="plus" :size="14" />
          <span>{{ t('client.menu.addClients') }}</span>
        </button>
        <span v-else class="tag blue selchip">
          {{ t('client.menu.selectedCount').replace('{count}', nf(selected.size)) }}
          <button type="button" class="chip-x" :aria-label="t('action.cancel')" @click="selected = new Set()">
            <Icon name="close" :size="11" />
          </button>
        </span>
        <button class="btn more-btn" :aria-expanded="!!moreOpen" @click="openMore">
          <Icon name="more" :size="14" />
          <span>{{ t('outbound.more') }}</span>
        </button>
        <button v-if="selected.size" class="btn danger-ghost spacer" @click="bulk('delete')">
          <Icon name="trash" :size="14" />
          <span>{{ t('action.delete') }}</span>
        </button>
      </div>
    </div>

    <div class="card-body clients-body">
      <!-- Their filter bar: search, Filter with its count, Sort, Clear all,
           and how many of the total are shown. -->
      <div class="filter-bar">
        <div class="search">
          <Icon name="search" :size="14" />
          <input v-model="search" type="search" :placeholder="t('client.menu.searchPlaceholder')" :aria-label="t('action.search')" />
        </div>
        <span class="badge-wrap">
          <button class="btn" :class="{ primary: filterCount > 0 }" @click="filterOpen = true">
            <Icon name="filter" :size="14" />
            <span>{{ t('filter.button') }}</span>
          </button>
          <span v-if="filterCount" class="badge">{{ filterCount }}</span>
        </span>
        <select v-model="sort" class="sort" :aria-label="t('client.sort.label')">
          <option v-for="o in SORT_OPTIONS" :key="o.value" :value="o.value">{{ t(o.key) }}</option>
        </select>
        <button v-if="filterCount || search || statusFilter || groupFilter" class="btn" @click="clearFilters">
          {{ t('client.menu.clearAllFilters') }}
        </button>
        <span v-if="page" class="muted small filter-count">
          {{ t('client.menu.showingCount').replace('{shown}', nf(page.items.length)).replace('{total}', nf(page.total)) }}
        </span>
      </div>
      <div v-if="filterChips.length" class="chips">
        <span v-for="(chip, i) in filterChips" :key="i" class="tag closable" :class="chip.color">
          {{ chip.text }}
          <button type="button" class="chip-x" :aria-label="t('action.remove')" @click="clearChip(chip)"><Icon name="close" :size="10" /></button>
        </span>
      </div>

      <table v-if="showSkeleton" class="skeleton" aria-hidden="true">
        <tbody>
          <tr v-for="n in 8" :key="n">
            <td v-for="c in 10" :key="c"><span class="sk"></span></td>
          </tr>
        </tbody>
      </table>
      <div v-else-if="loading && !page" class="empty"></div>

      <template v-else>
        <div class="table-wrap desk" :class="{ stale: refiltering }">
          <table>
            <thead>
              <tr>
                <th class="tick">
                  <input type="checkbox" :checked="allSelected" :aria-label="t('action.selectAll')" @change="toggleAll($event.target.checked)" />
                </th>
                <th class="w-cactions">{{ t('table.actions') }}</th>
                <th class="w-enabled">{{ t('table.enabled') }}</th>
                <th class="w-online">{{ t('status.online') }}</th>
                <th class="w-client">{{ t('client.menu.client') }}</th>
                <th v-if="hasGroups" class="w-group">{{ t('client.group') }}</th>
                <th class="w-inbounds">{{ t('client.attachedInbounds') }}</th>
                <th class="w-ctraffic">{{ t('client.traffic') }}</th>
                <th class="w-speed center">{{ t('client.speed') }}</th>
                <th class="w-remaining">{{ t('client.remaining') }}</th>
                <th class="w-duration">{{ t('client.menu.duration') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!page || !page.items.length" class="empty-row">
                <td :colspan="hasGroups ? 11 : 10">
                  <div class="card-empty">
                    <Icon name="users" :size="32" />
                    <div>{{ t('common.nothingYet') }}</div>
                    <button v-if="search || statusFilter || groupFilter || filterCount" class="btn sm" @click="clearFilters">{{ t('client.menu.clearAllFilters') }}</button>
                  </div>
                </td>
              </tr>
              <tr v-for="c in page.items" :key="c.id" :class="{ picked: selected.has(c.id) }">
                <td class="tick">
                  <input type="checkbox" :checked="selected.has(c.id)" :aria-label="c.name" @change="toggleOne(c.id, $event.target.checked)" />
                </td>

                <td class="w-cactions">
                  <div class="actions">
                    <button class="act text" :title="t('device.showQR')" @click="shareFor = c"><Icon name="qr" :size="16" /></button>
                    <button class="act text" :title="t('client.menu.clientInfo')" @click="router.push(`/clients/${c.id}`)"><Icon name="info" :size="16" /></button>
                    <button class="act text" :title="t('outbound.resetTraffic')" :disabled="isPending(c.id)" @click="resetOne(c)">
                      <span v-if="isPending(c.id)" class="spin sm"></span>
                      <Icon v-else name="refresh" :size="16" />
                    </button>
                    <button class="act text" :title="t('action.edit')" @click="formFor = { client: c }"><Icon name="edit" :size="16" /></button>
                    <button class="act text danger" :title="t('action.delete')" @click="removeOne(c)"><Icon name="trash" :size="16" /></button>
                  </div>
                </td>

                <td>
                  <Toggle
                    :model-value="c.status === 'active'"
                    :label="c.name"
                    :disabled="c.status === 'expired' || c.status === 'exhausted'"
                    :loading="isPending(c.id)"
                    @update:model-value="(v) => setEnabled(c, v)"
                  />
                </td>

                <td>
                  <span class="tag" :class="statusTag(c).color" :title="lastOnlineTitle(c)">
                    <i v-if="statusTag(c).dot" class="online-dot"></i>{{ statusTag(c).label }}
                  </span>
                </td>

                <td>
                  <div class="email-cell">
                    <a class="email" href="#" @click.prevent="router.push(`/clients/${c.id}`)">{{ c.name }}</a>
                    <span class="sub ltr">{{ c.accounts?.length ?? 0 }} / {{ c.deviceLimit }}</span>
                    <span v-if="c.note" class="sub">{{ c.note }}</span>
                  </div>
                </td>

                <td v-if="hasGroups">
                  <button v-if="c.group" class="tag geekblue grouptag" :class="{ dim: groupFilter === c.group }" @click="groupFilter = c.group">{{ c.group }}</button>
                  <span v-else class="muted">—</span>
                </td>

                <td>
                  <template v-if="inboundChips(c).length">
                    <span v-for="ib in inboundChips(c).slice(0, INBOUND_CHIP_LIMIT)" :key="ib.name" class="tag chip" :class="ib.protocol === 'openvpn' ? 'orange' : 'geekblue'" :title="ib.name">{{ ib.name }}</span>
                    <span v-if="inboundChips(c).length > INBOUND_CHIP_LIMIT" class="tag chip" :title="inboundChips(c).slice(INBOUND_CHIP_LIMIT).map((x) => x.name).join(', ')">+{{ inboundChips(c).length - INBOUND_CHIP_LIMIT }}</span>
                  </template>
                  <span v-else class="muted">—</span>
                </td>

                <td>
                  <div class="traffic-cell" :title="`↑ ${bytes(c.upBytes || 0, store.locale)}  ↓ ${bytes(c.downBytes || 0, store.locale)}`">
                    <span class="traffic-used num ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
                    <div class="meter traffic-bar"><span :class="meterClass(usedPercent(c))" :style="{ width: (usedPercent(c) ?? 0) + '%' }"></span></div>
                    <span class="traffic-limit num ltr">{{ c.quotaBytes ? bytes(c.quotaBytes, store.locale) : '∞' }}</span>
                  </div>
                </td>

                <td class="center">
                  <span class="tag num ltr speed-tag">{{ speedOf(c) || '—' }}</span>
                </td>

                <td>
                  <span class="tag num ltr" :class="remainingTag(c).color">{{ remainingTag(c).label }}</span>
                </td>

                <td>
                  <span class="tag ltr" :class="expiryTag(c).color" :title="expiryTitle(c)">{{ expiryTag(c).label }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

      <div class="cards">
        <article v-for="c in page.items" :key="c.id" class="ccard" :class="{ picked: selected.has(c.id) }">
          <div class="crow">
            <input
              type="checkbox"
              :checked="selected.has(c.id)"
              :aria-label="c.name"
              @change="toggleOne(c.id, $event.target.checked)"
            />
            <a class="name" href="#" @click.prevent="router.push(`/clients/${c.id}`)">{{ c.name }}</a>
            <Toggle
              class="spacer"
              :model-value="c.status === 'active'"
              :label="c.name"
              :disabled="c.status === 'expired' || c.status === 'exhausted'"
              :loading="isPending(c.id)"
              @update:model-value="(v) => setEnabled(c, v)"
            />
          </div>

          <div class="crow tags">
            <span class="tag" :class="statusTag(c).color">
              <i v-if="statusTag(c).dot" class="dot"></i>{{ statusTag(c).label }}
            </span>
            <span class="tag proto">{{ c.protocol }}</span>
            <span v-if="c.group" class="tag geekblue">{{ c.group }}</span>
            <span class="muted small ltr">{{ c.accounts?.length ?? 0 }} / {{ c.deviceLimit }}</span>
          </div>

          <div class="meter"><span :class="meterClass(usedPercent(c))" :style="{ width: (usedPercent(c) ?? 0) + '%' }"></span></div>
          <div class="crow muted small">
            <span class="num ltr">
              {{ bytes(c.usedBytes, store.locale) }} /
              {{ c.quotaBytes ? bytes(c.quotaBytes, store.locale) : '∞' }}
            </span>
            <span class="spacer tag ltr" :class="expiryTag(c).color">{{ expiryTag(c).label }}</span>
          </div>

          <div class="crow actions">
            <button class="act" :title="t('device.showQR')" @click="shareFor = c"><Icon name="qr" :size="17" /></button>
            <button class="act" :title="t('action.details')" @click="router.push(`/clients/${c.id}`)"><Icon name="info" :size="17" /></button>
            <button class="act" :title="t('action.resetTraffic')" :disabled="isPending(c.id)" @click="resetOne(c)"><span v-if="isPending(c.id)" class="spin sm"></span><Icon v-else name="refresh" :size="17" /></button>
            <button class="act" :title="t('action.edit')" @click="formFor = { client: c }"><Icon name="edit" :size="17" /></button>
            <button class="act danger spacer" :title="t('action.delete')" @click="removeOne(c)"><Icon name="trash" :size="17" /></button>
          </div>
        </article>
      </div>
      </template>

      <div v-if="page && totalPages > 1" class="pager">
        <button class="btn sm" :disabled="currentPage <= 1" @click="currentPage--; load()">‹</button>
        <span class="muted small num ltr">{{ currentPage }} / {{ totalPages }}</span>
        <button class="btn sm" :disabled="currentPage >= totalPages" @click="currentPage++; load()">›</button>
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
    <div v-if="moreOpen" class="rowmenu" role="menu" :style="{ top: moreOpen.y + 'px', left: moreOpen.x + 'px' }">
      <template v-for="(m, i) in moreItems" :key="m.key || `d${i}`">
        <hr v-if="m.divider" class="menu-divider" />
        <button v-else class="menu-item" :class="{ danger: m.danger }" role="menuitem" @click="pickMore(m.key)">
          <Icon :name="m.icon" :size="14" />{{ m.label }}
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
            <label for="cd-days">{{ t('group.extendDays') }}</label>
            <input id="cd-days" v-model="form.addDays" type="number" :placeholder="t('client.leaveBlank')" autofocus />
            <span class="hint">{{ t('group.extendHint') }}</span>
          </div>
          <div class="field">
            <label for="cd-quota">{{ t('client.quota') }} (GB)</label>
            <input id="cd-quota" v-model="form.quotaGB" type="number" min="0" step="0.5" :placeholder="t('client.leaveBlank')" />
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
            <label for="cd-bquota">{{ t('client.quota') }} (GB)</label>
            <input id="cd-bquota" v-model="form.quotaGB" type="number" min="0" step="0.5" :placeholder="t('client.unlimited')" />
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
  <ShareDialog v-if="shareFor" :client="shareFor" @close="shareFor = null" />

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
/* Their summary card: six Statistics in a row, title over value. */
.summary-card {
  padding: 12px 16px;
  margin-bottom: 12px;
}
.summary-grid {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 12px 16px;
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 0;
  border: 0;
  background: none;
  text-align: start;
  color: inherit;
  font: inherit;
}
.stat.pressable {
  cursor: pointer;
  border-radius: 6px;
}
.stat.pressable.on .stat-value {
  color: var(--accent);
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
  font-variant-numeric: tabular-nums;
}
.stat-icon {
  color: var(--muted);
}
.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-inline-end: 4px;
}
.dot-green { background: var(--ok); }
.dot-blue { background: #1677ff; }
.dot-red { background: var(--bad); }
.dot-orange { background: #faad14; }
.dot-gray { background: var(--faint); }
@media (max-width: 992px) {
  .summary-grid { grid-template-columns: repeat(3, 1fr); }
}
@media (max-width: 560px) {
  .summary-grid { grid-template-columns: repeat(2, 1fr); }
}

/* Their toolbar in the card title, and the filter bar under it. */
.card-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  width: 100%;
  padding: 6px 0;
}
.selchip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  font-size: 13px;
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
.chip-x:hover { opacity: 1; }
.clients-body {
  padding: 12px 16px 16px;
}
.filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.filter-bar .search {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1 1 200px;
  max-width: 320px;
  height: 32px;
  padding: 0 11px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--faint);
}
.filter-bar .search input {
  flex: 1;
  min-width: 0;
  height: 100%;
  border: 0;
  background: none;
  color: var(--ink);
  font-size: 14px;
}
.filter-bar .search input:focus { outline: none; box-shadow: none; }
.filter-bar .sort {
  min-width: 200px;
  height: 32px;
}
.badge-wrap {
  position: relative;
  display: inline-flex;
}
.badge {
  position: absolute;
  top: -6px;
  right: -6px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--bad);
  color: #fff;
  font-size: 11px;
  line-height: 16px;
  text-align: center;
}
.filter-count {
  margin-inline-start: auto;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 12px;
}
.tag.closable {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

/* Their columns and widths. */
.w-cactions { width: 200px; }
.w-enabled { width: 80px; }
.w-online { width: 90px; }
.w-client { width: 220px; }
.w-group { width: 130px; }
.w-inbounds { width: 170px; }
.w-ctraffic { width: 300px; }
.w-speed { width: 110px; }
.w-remaining { width: 130px; }
.w-duration { width: 130px; }
th.center, td.center { text-align: center; }
.act.text {
  width: 24px;
  height: 24px;
  color: var(--ink);
}
.act.text.danger { color: var(--bad); }
.online-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  margin-inline-end: 4px;
}
.email-cell {
  display: flex;
  flex-direction: column;
}
.email-cell .email {
  font-weight: 500;
  color: inherit;
  text-decoration: none;
}
.email-cell .sub {
  font-size: 11px;
  opacity: 0.55;
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tag.chip { margin: 2px; }
.grouptag.dim { opacity: 0.6; }
.traffic-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-width: 0;
}
.traffic-used,
.traffic-limit {
  flex: 0 0 72px;
  min-width: 72px;
  font-size: 12px;
  white-space: nowrap;
}
.traffic-used { text-align: end; }
.traffic-limit { text-align: start; color: var(--muted); }
.traffic-bar {
  flex: 1 1 60px;
  min-width: 48px;
  margin: 0;
}
.speed-tag { min-width: 72px; }

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.dot.ok { background: var(--tag-green-ink); }
.dot.warn { background: var(--tag-orange-ink); }
.dot.bad { background: var(--tag-red-ink); }
.dot.muted { background: var(--faint); }
.dot.ink { background: var(--accent); }

.actionbar {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.selchip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 8px 5px 12px;
  border-radius: 100px;
  background: var(--accent-soft);
  border: 1px solid var(--accent-line);
  color: var(--accent-hover);
  font-size: var(--t-sm);
  font-weight: 600;
}
.selchip button {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.selchip button:hover {
  background: var(--accent);
  color: var(--accent-ink);
}
.filterbadge {
  font-size: var(--t-xs);
  color: var(--muted);
}
.filterbadge b {
  color: var(--accent-hover);
}

.search {
  position: relative;
  display: flex;
  align-items: center;
  flex: 1 1 200px;
  max-width: 280px;
}
.search svg {
  position: absolute;
  inset-inline-start: 11px;
  color: var(--faint);
  pointer-events: none;
}
.search input {
  padding-inline-start: 34px;
}
.ctl {
  width: auto;
  min-width: 130px;
}

th.tick,
td.tick {
  width: 42px;
  padding-inline-end: 0;
}

/* The declared widths only hold if the table is allowed to reach its natural
   size; squeezed into a narrower wrapper the browser ignores them and wraps
   every cell instead. Giving it a floor lets the wrapper scroll sideways —
   which is what 3x-ui's own client table does — and keeps rows one line tall. */
.desk table {
  min-width: 1260px;
}
.desk td,
.desk th {
  white-space: nowrap;
}
.desk td .sub {
  white-space: normal;
}

/* Explicit widths, the way 3x-ui sizes its columns. Left to itself the browser
   gives the icon row more space than the customer's name and wraps it onto
   three lines, tripling the row height. */
.w-actions { width: 172px; }
.w-sm { width: 78px; }
.w-md { width: 112px; }
.w-name { min-width: 190px; }
.w-traffic { width: 200px; }
.w-exp { width: 132px; }

.name {
  white-space: nowrap;
}
input[type='checkbox'] {
  width: 16px;
  height: 16px;
  min-height: 0;
  padding: 0;
  accent-color: var(--accent);
  cursor: pointer;
}


tr.picked,
.ccard.picked {
  background: var(--accent-soft);
}
.name {
  color: var(--ink);
  font-weight: 600;
}
.cards .name {
  white-space: normal;
}
.name:hover {
  color: var(--accent-hover);
}
.sub {
  margin-top: 1px;
}
.grouptag {
  border: 1px solid var(--accent-line);
  cursor: pointer;
  font: inherit;
  font-family: var(--mono);
  font-size: var(--t-xs);
  font-weight: 600;
}
.grouptag:hover {
  background: var(--accent);
  color: var(--accent-ink);
}
.soon {
  color: var(--warn);
  font-weight: 600;
}

.rowmenu {
  position: fixed;
  z-index: 40;
  min-width: 222px;
  padding: 5px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--surface-2);
  box-shadow: var(--shadow);
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.menu-item {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 11px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--ink-2);
  font: inherit;
  font-size: var(--t-sm);
  text-align: start;
  cursor: pointer;
  white-space: nowrap;
}
.menu-item:hover {
  background: var(--surface-3);
  color: var(--ink);
}
.menu-item.danger {
  color: var(--bad);
}
.menu-item.danger:hover {
  background: var(--bad-soft);
}

/* ---------- mobile cards ---------- */
.cards {
  display: none;
}
.ccard {
  padding: 14px 16px;
  border-bottom: 1px solid var(--line-soft);
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.ccard:last-child {
  border-bottom: none;
}
.crow {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.crow.tags {
  gap: 6px;
}

.modal.narrow {
  max-width: 440px;
}
.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.target {
  margin: 0;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--line-soft);
}

.pager {
  display: flex;
  align-items: center;
  gap: 12px;
  justify-content: center;
  padding: 13px;
  border-top: 1px solid var(--line-soft);
}

@media (max-width: 860px) {
  .desk {
    display: none;
  }
  .cards {
    display: block;
  }
}
</style>
