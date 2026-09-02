<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes, relative, dateTime, percent, gigabytesToBytes, isOnline } from '../lib/format.js'
import ClientForm from '../components/ClientForm.vue'
import ShareDialog from '../components/ShareDialog.vue'
import Toggle from '../components/Toggle.vue'
import Icon from '../components/Icon.vue'

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

async function load() {
  loading.value = true
  try {
    const [p, o] = await Promise.all([
      api.clients({
        search: search.value,
        status: statusFilter.value,
        group: groupFilter.value,
        sort: sort.value,
        page: currentPage.value,
        perPage: 25,
      }),
      api.overview(),
    ])
    page.value = p
    stats.value = o
    // Drop selections for rows no longer on screen, so a bulk action can never
    // reach a client the operator can no longer see.
    const visible = new Set(p.items.map((c) => c.id))
    selected.value = new Set([...selected.value].filter((id) => visible.has(id)))
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
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

const strip = computed(() => {
  const s = stats.value
  if (!s) return []
  return [
    { key: 'clients', value: s.clients, tone: 'ink', filter: null },
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

const setEnabled = (c, on) =>
  guard(() => api.updateClient(c.id, { status: on ? 'active' : 'disabled' }), 'client.updated')
const resetOne = (c) => guard(() => api.resetTraffic(c.id), 'client.trafficReset')
const removeOne = (c) => {
  if (!confirm(t('client.confirmDelete'))) return
  return guard(() => api.deleteClient(c.id), 'client.deleted')
}

const ids = () => [...selected.value]

const bulk = (action) => {
  if (!selected.value.size) return
  if (action === 'delete' && !confirm(t('client.confirmDeleteMany'))) return
  return guard(async () => {
    await api.bulkClients(action, ids())
    selected.value = new Set()
  }, 'client.bulkDone')
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
    window.location.href = api.exportUrl()
    return
  }
  if (key === 'resetAll') {
    if (!confirm(t('client.confirmResetAll'))) return
    return guard(() => api.resetAllTraffic(), 'client.bulkDone')
  }
  if (key === 'purgeExhausted') {
    if (!confirm(t('client.confirmPurge'))) return
    return guard(() => api.purgeClients('exhausted'), 'client.deleted')
  }
  if (key === 'purgeExpired') {
    if (!confirm(t('client.confirmPurge'))) return
    return guard(() => api.purgeClients('expired'), 'client.deleted')
  }
}

function openDialog(kind) {
  if (kind === 'group') form.value.group = ''
  if (kind === 'adjust') {
    form.value.addDays = ''
    form.value.quotaGB = ''
    form.value.resetCycle = ''
  }
  dialog.value = { kind }
}

async function submitDialog() {
  busy.value = true
  try {
    const d = dialog.value
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
      delete payload.interfaceId
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
    throw err
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
    <button
      v-for="s in strip"
      :key="s.key"
      class="strip-item"
      type="button"
      @click="statusFilter = s.filter || ''"
    >
      <span class="strip-label"><i class="dot" :class="s.tone"></i>{{ t(`stat.${s.key}`) }}</span>
      <span class="strip-value num">{{ nf(s.value) }}</span>
    </button>
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

    <div v-if="loading" class="empty"><span class="spin"></span></div>
    <div v-else-if="!page || !page.items.length" class="empty">{{ t('client.none') }}</div>

    <template v-else>
      <!-- Table on a desk, cards on a phone. A ten-column table forced into a
           375px viewport is a horizontal scroll nobody uses. -->
      <div class="table-wrap desk">
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
                  <button class="act" :title="t('action.resetTraffic')" @click="resetOne(c)">
                    <Icon name="refresh" :size="16" />
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
            <button class="act" :title="t('action.resetTraffic')" @click="resetOne(c)"><Icon name="refresh" :size="17" /></button>
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
            : dialog.kind === 'adjust' ? t('client.adjust') : t('client.batchAdd') }}
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
</template>

<style scoped>
.strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(118px, 1fr));
  gap: 1px;
  background: var(--line-soft);
  margin-bottom: 16px;
  overflow: hidden;
}
.strip-item {
  background: var(--surface);
  border: none;
  padding: 13px 16px;
  text-align: start;
  font: inherit;
  color: inherit;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.strip-item:hover {
  background: var(--surface-2);
}
.strip-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--t-xs);
  color: var(--muted);
}
.strip-value {
  font-size: var(--t-lg);
  font-weight: 600;
  line-height: 1.15;
}
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

.actions {
  display: flex;
  gap: 2px;
}
.act {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
}
.act:hover {
  background: var(--surface-3);
  color: var(--ink);
}
.act.danger:hover {
  background: var(--bad-soft);
  color: var(--bad);
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
