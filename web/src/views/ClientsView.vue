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
async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const [p, o] = await Promise.all([
      api.clients(
        {
          search: search.value,
          status: statusFilter.value,
          group: groupFilter.value,
          sort: sort.value,
          page: currentPage.value,
          perPage: 25,
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

// Clearing every filter at once. Undoing three separately, to find out whether
// the list is empty or merely filtered, is work the panel should do.
function clearFilters() {
  search.value = ''
  statusFilter.value = ''
  groupFilter.value = ''
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
  if (c.status === 'exhausted') return { color: 'red', label: t('status.exhausted') }
  if (c.status === 'expired') return { color: 'red', label: t('status.expired') }
  if (c.status === 'disabled') return { color: 'grey', label: t('status.disabled') }
  if (clientOnline(c)) return { color: 'green', label: t('status.online'), dot: true }
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
  moreOpen.value = { x: Math.max(8, r.right - 230), y: r.bottom + 4 }
}

const moreItems = computed(() => [
  { key: 'batch', label: t('client.batchAdd'), icon: 'plus' },
  { key: 'export', label: t('client.export'), icon: 'download' },
  { key: 'resetAll', label: t('client.resetAll'), icon: 'refresh', danger: true },
  { key: 'purgeExhausted', label: t('client.purgeExhausted'), icon: 'trash', danger: true },
  { key: 'purgeExpired', label: t('client.purgeExpired'), icon: 'trash', danger: true },
])

function pickMore(key) {
  moreOpen.value = null
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
  <div class="page-head">
    <div>
      <h1>{{ t('nav.clients') }}</h1>
      <p>{{ t('client.subtitle') }}</p>
    </div>
  </div>

  <div v-if="stats" class="strip card">
    <!-- A figure is a button only when it can actually narrow the table. The
         two that cannot are rendered as plain figures, so nothing here looks
         pressable and then does something else. -->
    <component
      :is="s.filter === null ? 'div' : 'button'"
      v-for="s in strip"
      :key="s.key"
      class="strip-item"
      :class="{ on: s.filter !== null && s.filter === statusFilter, plain: s.filter === null }"
      :type="s.filter === null ? undefined : 'button'"
      :aria-pressed="s.filter === null ? undefined : s.filter === statusFilter"
      @click="s.filter === null || (statusFilter = s.filter)"
    >
      <span class="strip-label"><i class="dot" :class="s.tone"></i>{{ t(`stat.${s.key}`) }}</span>
      <span class="strip-value num">{{ nf(s.value) }}</span>
    </component>
  </div>

  <div class="actionbar">
    <template v-if="!selected.size">
      <button
        class="btn primary"
        :disabled="!interfaces.length"
        :title="interfaces.length ? '' : t('interface.noneYet')"
        @click="formFor = {}"
      >
        <Icon name="plus" :size="15" />{{ t('client.create') }}
      </button>
      <button class="btn more-btn" @click="openMore">
        <Icon name="more" :size="15" />{{ t('action.more') }}
      </button>
      <span v-if="activeFilters" class="filterbadge">
        {{ t('action.filters') }} <b>{{ activeFilters }}</b>
      </span>
    </template>

    <template v-else>
      <span class="selchip">
        {{ t('action.selected') }}: {{ nf(selected.size) }}
        <button :aria-label="t('action.cancel')" @click="selected = new Set()">
          <Icon name="close" :size="12" />
        </button>
      </span>
      <button class="btn sm" @click="openDialog('group')">
        <Icon name="tag" :size="13" />{{ t('client.addToGroup') }}
      </button>
      <button class="btn sm" @click="ungroup">{{ t('client.ungroup') }}</button>
      <button class="btn sm" @click="openDialog('adjust')">
        <Icon name="clock" :size="13" />{{ t('client.adjust') }}
      </button>
      <!-- The operation having several servers makes necessary: a node is
           rented, and the customers who already exist have to be given it. -->
      <button class="btn sm" @click="openDialog('attach')">
        <Icon name="server" :size="13" />{{ t('client.attachServers') }}
      </button>
      <button class="btn sm" @click="openDialog('detach')">
        <Icon name="swap" :size="13" />{{ t('client.detachServers') }}
      </button>
      <button class="btn sm" @click="bulk('enable')">{{ t('action.enable') }}</button>
      <button class="btn sm" @click="bulk('disable')">{{ t('action.disable') }}</button>
      <button class="btn sm" @click="bulk('reset')">{{ t('action.resetTraffic') }}</button>
      <button class="btn sm danger spacer" @click="bulk('delete')">
        <Icon name="trash" :size="13" />{{ t('action.delete') }}
      </button>
    </template>
  </div>

  <div v-if="!interfaces.length" class="banner warn">
    <Icon name="alert" :size="17" />
    <span>
      {{ t('interface.noneYet') }}
      <a href="#" @click.prevent="router.push('/interfaces')">{{ t('interface.create') }}</a>
    </span>
  </div>

  <div class="card">
    <div class="card-head">
      <div class="search">
        <Icon name="search" :size="15" />
        <input v-model="search" type="search" :placeholder="t('client.searchHint')" />
      </div>
      <select v-model="statusFilter" class="ctl">
        <option value="">{{ t('filter.allStatuses') }}</option>
        <option value="active">{{ t('status.active') }}</option>
        <option value="disabled">{{ t('status.disabled') }}</option>
        <option value="expired">{{ t('status.expired') }}</option>
        <option value="exhausted">{{ t('status.exhausted') }}</option>
      </select>
      <select v-if="hasGroups" v-model="groupFilter" class="ctl">
        <option value="">{{ t('filter.allGroups') }}</option>
        <option v-for="g in groupNames" :key="g" :value="g">{{ g }}</option>
      </select>
      <select v-model="sort" class="ctl">
        <option value="newest">{{ t('sort.newest') }}</option>
        <option value="oldest">{{ t('sort.oldest') }}</option>
        <option value="name">{{ t('sort.name') }}</option>
        <option value="traffic">{{ t('sort.traffic') }}</option>
        <option value="expiry">{{ t('sort.expiry') }}</option>
      </select>
      <span v-if="page" class="spacer muted small num">{{ nf(page.total) }}</span>
    </div>

    <!-- A skeleton in the table's own shape, so the page does not collapse to a
         spinner and spring back to full height a moment later. -->
    <table v-if="showSkeleton" class="skeleton" aria-hidden="true">
      <tbody>
        <tr v-for="n in 8" :key="n">
          <td v-for="c in 9" :key="c"><span class="sk"></span></td>
        </tr>
      </tbody>
    </table>
    <div v-else-if="loading && !page" class="empty"></div>
    <div v-else-if="!page || !page.items.length" class="empty empty-cta">
      <template v-if="!interfaces.length">
        <p>{{ t('client.noneNoInterface') }}</p>
        <p class="small muted">{{ t('client.noneNoInterfaceHint') }}</p>
        <RouterLink to="/interfaces" class="btn">
          <Icon name="server" :size="15" />
          <span>{{ t('interface.create') }}</span>
        </RouterLink>
      </template>
      <template v-else-if="search || statusFilter || groupFilter">
        <!-- Nothing matched, which is a different thing from having nothing. -->
        <p>{{ t('client.noneMatch') }}</p>
        <button class="btn ghost" @click="clearFilters">
          <Icon name="close" :size="15" />
          <span>{{ t('client.clearFilters') }}</span>
        </button>
      </template>
      <template v-else>
        <p>{{ t('client.none') }}</p>
        <p class="small muted">{{ t('client.noneHint') }}</p>
        <button class="btn" @click="formFor = {}">
          <Icon name="plus" :size="15" />
          <span>{{ t('client.add') }}</span>
        </button>
      </template>
    </div>

    <template v-else>
      <!-- Table on a desk, cards on a phone. A ten-column table forced into a
           375px viewport is a horizontal scroll nobody uses.

           While a refilter is in flight the rows stay and simply recede. The
           answer is about to replace them either way, and a table that empties
           itself to fetch the same twenty-five rows is a worse wait than one
           that holds still. -->
      <div class="table-wrap desk" :class="{ stale: refiltering }">
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
              <!-- Who the row is about comes first. It used to sit fifth,
                   behind a checkbox, six action buttons, a toggle and a status
                   badge: finding a customer by name meant the eye jumping past
                   four columns of controls on every one of them. -->
              <th class="w-actions">{{ t('table.actions') }}</th>
              <th class="w-sm">{{ t('table.enabled') }}</th>
              <th class="w-md">{{ t('table.online') }}</th>
              <th class="w-name">{{ t('nav.clients') }}</th>
              <th v-if="hasGroups" class="w-md">{{ t('group.name') }}</th>
              <th class="w-md">{{ t('interface.name') }}</th>
              <th class="w-traffic">{{ t('client.traffic') }}</th>
              <th class="w-md">{{ t('client.speed') }}</th>
              <th class="w-md">{{ t('client.remaining') }}</th>
              <th class="w-exp">{{ t('client.expires') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in page.items" :key="c.id" :class="{ picked: selected.has(c.id) }">
              <td class="tick">
                <input
                  type="checkbox"
                  :checked="selected.has(c.id)"
                  :aria-label="c.name"
                  @change="toggleOne(c.id, $event.target.checked)"
                />
              </td>

              <td class="w-actions">
                <div class="actions">
                  <button class="act" :title="t('device.showQR')" @click="shareFor = c">
                    <Icon name="qr" :size="16" />
                  </button>
                  <button class="act" :title="t('action.details')" @click="router.push(`/clients/${c.id}`)">
                    <Icon name="info" :size="16" />
                  </button>
                  <button
                    class="act"
                    :title="t('action.resetTraffic')"
                    :disabled="isPending(c.id)"
                    @click="resetOne(c)"
                  >
                    <span v-if="isPending(c.id)" class="spin sm"></span>
                    <Icon v-else name="refresh" :size="16" />
                  </button>
                  <button class="act" :title="t('action.edit')" @click="formFor = { client: c }">
                    <Icon name="edit" :size="16" />
                  </button>
                  <button class="act danger" :title="t('action.delete')" @click="removeOne(c)">
                    <Icon name="trash" :size="16" />
                  </button>
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
                <span class="tag" :class="statusTag(c).color">
                  <i v-if="statusTag(c).dot" class="dot"></i>{{ statusTag(c).label }}
                </span>
              </td>

              <td>
                <a class="name" href="#" @click.prevent="router.push(`/clients/${c.id}`)">{{ c.name }}</a>
                <div class="sub muted small">
                  <span class="ltr">{{ c.accounts?.length ?? 0 }} / {{ c.deviceLimit }}</span>
                  <template v-if="c.note"> · {{ c.note }}</template>
                </div>
              </td>

              <td v-if="hasGroups">
                <button v-if="c.group" class="tag geekblue grouptag" @click="groupFilter = c.group">
                  {{ c.group }}
                </button>
                <span v-else class="muted">—</span>
              </td>

              <td><span class="tag proto">{{ c.protocol }}</span></td>

              <td>
                <div class="meter" style="margin-bottom: 6px">
                  <span :class="meterClass(usedPercent(c))" :style="{ width: (usedPercent(c) ?? 0) + '%' }"></span>
                </div>
                <span class="muted small num ltr">
                  {{ bytes(c.usedBytes, store.locale) }} /
                  {{ c.quotaBytes ? bytes(c.quotaBytes, store.locale) : '∞' }}
                </span>
              </td>

              <td>
                <span class="num ltr muted small">{{ speedOf(c) }}</span>
              </td>

              <td>
                <span class="tag num ltr" :class="remainingTag(c).color">{{ remainingTag(c).label }}</span>
              </td>

              <td>
                <span
                  class="tag ltr"
                  :class="expiryTag(c).color"
                  :title="expiryTitle(c)"
                >
                  {{ expiryTag(c).label }}
                </span>
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

  <div v-if="moreOpen" class="rowmenu" role="menu" :style="{ top: moreOpen.y + 'px', left: moreOpen.x + 'px' }">
    <button
      v-for="m in moreItems"
      :key="m.key"
      class="menu-item"
      :class="{ danger: m.danger }"
      role="menuitem"
      @click="pickMore(m.key)"
    >
      <Icon :name="m.icon" :size="14" />{{ m.label }}
    </button>
  </div>

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
