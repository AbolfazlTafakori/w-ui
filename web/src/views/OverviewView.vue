<script setup>
import { ref, computed, watch, onMounted, onUnmounted, onBeforeUnmount } from 'vue'
import { RouterLink } from 'vue-router'
import { api, apiURL, getToken } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Sparkline from '../components/Sparkline.vue'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const data = ref(null)
const loading = ref(true)
const showIp = ref(false)
let timer = null

// loadError is only shown when there is nothing to show instead. A refresh
// that fails while the page already holds figures should not replace them with
// an error: stale numbers with a warning are more use than none.
const loadError = ref(null)

async function load(quiet = false) {
  try {
    data.value = await api.fullOverview({ background: quiet })
    loadError.value = null
  } catch (err) {
    loadError.value = err
    if (!quiet) notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await load()
  // The server samples on its own ticker and keeps the history; polling only
  // pulls the latest window. Failures stay quiet so a brief blip does not stack
  // toasts on an unattended dashboard.
  timer = setInterval(() => load(true), 3000)
})
onUnmounted(() => clearInterval(timer))

const sys = computed(() => data.value?.system)

// A first load only: the three-second refresh must never put a skeleton over a
// page that already has numbers on it.
//
// Declared after `sys`, not before it: useDelayed watches with `immediate`, so
// it reads the source during setup -- placed any earlier it reaches a `const`
// that does not exist yet and the whole page fails to render.
const showSkeleton = useDelayed(computed(() => loading.value && !sys.value))
const panel = computed(() => data.value?.panel)
const clients = computed(() => data.value?.clients)

// The tiles an operator acts on, in the order they matter. The ones that mean
// work sit first; the reassuring totals sit last.
const customerTiles = computed(() => {
  const c = clients.value
  if (!c) return []
  return [
    { key: 'online', label: t('overview.onlineNow'), value: c.online, tone: 'ok', to: '/clients' },
    { key: 'depleting', label: t('stat.depleting'), value: c.depleting, tone: 'warn',
      urgent: true, to: '/clients?status=active' },
    { key: 'exhausted', label: t('status.exhausted'), value: c.exhausted, tone: 'bad',
      urgent: true, to: '/clients?status=exhausted' },
    { key: 'expired', label: t('status.expired'), value: c.expired, tone: 'bad',
      urgent: true, to: '/clients?status=expired' },
    { key: 'active', label: t('status.active'), value: c.active, tone: 'ok', to: '/clients?status=active' },
    { key: 'total', label: t('nav.clients'), value: c.clients, tone: '', to: '/clients' },
  ]
})
const ifaces = computed(() => data.value?.interfaces || [])
const hist = computed(() => sys.value?.history || {})

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const rate = (bps) => `${bytes(bps, store.locale)}/s`

function duration(sec) {
  const s = Number(sec || 0)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  return `${m}m`
}

const stat = (arr) => {
  const a = (arr || []).filter(Number.isFinite)
  if (!a.length) return { min: 0, max: 0, mean: 0 }
  return {
    min: Math.min(...a),
    max: Math.max(...a),
    mean: a.reduce((x, y) => x + y, 0) / a.length,
  }
}

// Four vitals, in 3x-ui's order. Each carries its own recent history so the
// number is read against where it has been, not on its own.
const vitals = computed(() => {
  const s = sys.value
  if (!s) return []
  const mk = (key, icon, label, usage, detail, series) => {
    const st = stat(series)
    return {
      key,
      icon,
      label,
      percent: usage.percent,
      detail,
      footLeft: `${t('overview.min')} ${st.min.toFixed(0)}%`,
      footRight: `${t('overview.max')} ${st.max.toFixed(0)}%`,
      data: series || [],
      mean: st.mean,
    }
  }
  return [
    mk('cpu', 'dashboard', 'CPU', s.cpu, `${nf(s.cpu.cores)} ${t('overview.cores')}`, hist.value.cpu),
    mk(
      'mem',
      'database',
      'RAM',
      s.memory,
      `${bytes(s.memory.used, store.locale)} / ${bytes(s.memory.total, store.locale)}`,
      hist.value.memory,
    ),
    mk(
      'swap',
      'swap',
      t('overview.swap'),
      s.swap,
      s.swap.total
        ? `${bytes(s.swap.used, store.locale)} / ${bytes(s.swap.total, store.locale)}`
        : t('overview.noSwap'),
      hist.value.swap,
    ),
    mk(
      'disk',
      'hdd',
      t('overview.storage'),
      s.disk,
      `${bytes(s.disk.used, store.locale)} / ${bytes(s.disk.total, store.locale)}`,
      hist.value.disk,
    ),
  ]
})

// The number stays neutral until it means something. On a red-accented panel an
// accent-coloured reading looks like an alarm at every level, which would leave
// nothing to say when one is warranted. The line below it keeps the brand
// colour, where red is decoration rather than a warning.
function vitalColor(p) {
  if (p >= 92) return 'var(--bad)'
  if (p >= 75) return 'var(--warn)'
  return 'var(--ink)'
}
function vitalLine(p) {
  if (p >= 92) return 'var(--bad)'
  if (p >= 75) return 'var(--warn)'
  return 'var(--accent)'
}

const upStat = computed(() => stat(hist.value.up))
const downStat = computed(() => stat(hist.value.down))
const totalConns = computed(
  () => (sys.value?.network.tcpConns || 0) + (sys.value?.network.udpConns || 0),
)

const poolTotals = computed(() => ({
  allocated: ifaces.value.reduce((a, i) => a + (i.allocated || 0), 0),
  capacity: ifaces.value.reduce((a, i) => a + (i.capacity || 0), 0),
}))

const busy = ref(false)
const logs = ref(null)
const logLevel = ref('info')
const logLimit = ref(200)
const logQuery = ref('')
const logSource = ref('panel')
const logFollow = ref(false)
let logTimer = null

// Refreshing while the operator is reading is the point of following, so it has
// to be slow enough not to move the page under them and quick enough to be
// worth having on. Five seconds is what 3x-ui settled on and it is right.
const followInterval = 5000

// Both of these used to live a page away. When something is wrong, the log and
// a fresh backup are the first two things wanted, and this is the page an
// operator is already looking at.
async function openLogs() {
  if (!logs.value) logs.value = { loading: true, entries: [], notice: '' }
  await loadLogs()
}

async function loadLogs() {
  if (!logs.value) return
  logs.value = { ...logs.value, loading: true }
  try {
    const params = new URLSearchParams({
      limit: String(logLimit.value),
      level: logLevel.value,
      source: logSource.value,
    })
    // Only when there is something to search for: an empty parameter would be
    // a needle that matches everything and is one more thing on the wire.
    if (logQuery.value.trim()) params.set('q', logQuery.value.trim())

    const res = await api.get(`/api/logs?${params}`)
    // The endpoint wraps its rows; taking the response itself would give an
    // object where a list is expected and render nothing at all.
    logs.value = {
      loading: false,
      entries: res?.entries || [],
      // A source this server cannot read is worth saying out loud rather than
      // showing an empty list, which reads as "nothing happened".
      notice: res?.notice || '',
    }
  } catch (e) {
    logs.value = { loading: false, entries: [], notice: e.message }
  }
}

// Searching on every keystroke would be a request per letter. Waiting for the
// typing to stop is one request for the word.
let searchTimer = null
function onLogSearch() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(loadLogs, 250)
}

watch(logFollow, (on) => {
  clearInterval(logTimer)
  if (on) logTimer = setInterval(loadLogs, followInterval)
})

// Following a log after its window has gone is a request every five seconds for
// nothing, forever.
watch(logs, (v) => {
  if (!v) {
    clearInterval(logTimer)
    logFollow.value = false
  }
})

onBeforeUnmount(() => {
  clearInterval(logTimer)
  clearTimeout(searchTimer)
})

// Always the same clock, in Latin digits, whatever language the panel is in.
//
// The old viewer formatted this for the locale, which in Persian produced
// ۰۵:۱۹:۲۰ — correct as a time and wrong as a log column: it does not line up
// with the entry beside it, does not match what is in the file on the server,
// and cannot be searched for. A log is machine output and reads the same to
// everybody.
function logStamp(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// What a line looks like as text, which is what gets copied and downloaded.
function logLine(e) {
  const at = e.time ? new Date(e.time).toISOString() : ''
  const fields = e.fields
    ? Object.entries(e.fields).map(([k, v]) => `${k}=${v}`).join(' ')
    : ''
  return [at, e.level, e.message, fields].filter(Boolean).join(' ')
}

function logText() {
  return (logs.value?.entries || []).map(logLine).join('\n')
}

async function copyLogs() {
  try {
    await navigator.clipboard.writeText(logText())
    notify(t('logs.copied'), 'ok')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

// Saved rather than shown, because the useful thing to do with a log is send it
// to somebody, and selecting a thousand lines in a scrolling box is not that.
function downloadLogs() {
  const blob = new Blob([logText()], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `wui-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '')}.log`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// Backup, in one place, on the page an operator is already looking at.
//
// A button that silently wrote a file somewhere was the whole of it before.
// Taking one is the least of what somebody wants from this: the reasons to
// open it are to get the archive off the server, to put one back, and to move
// a panel to another machine.
const backup = ref(null)
const keepAddresses = ref(true)

function openBackup() {
  backup.value = { latest: null, working: '', done: '' }
  loadLatest()
}

async function loadLatest() {
  try {
    const list = await api.get('/api/backups')
    if (backup.value) backup.value.latest = list?.[0] || null
  } catch {
    // The list is a convenience; failing to read it must not close the dialog
    // an operator opened to take a backup.
  }
}

async function backupNow() {
  if (!backup.value) return
  backup.value.working = t('overview.backingUp')
  try {
    const a = await api.post('/api/backups')
    await loadLatest()
    backup.value.done = t('overview.backupTaken').replace('{name}', a.name)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    if (backup.value) backup.value.working = ''
  }
}

// Fetched rather than linked: the archive holds every key on the server, and a
// plain link carries no session.
async function exportBackup() {
  const name = backup.value?.latest?.name
  if (!name) return
  backup.value.working = t('overview.preparingDownload')
  try {
    const res = await fetch(apiURL(`/api/backups/${encodeURIComponent(name)}`), {
      headers: { Authorization: `Bearer ${getToken()}` },
      credentials: 'same-origin',
    })
    if (!res.ok) throw new Error(await res.text())
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    if (backup.value) backup.value.working = ''
  }
}

// Upload and restore in one action, because that is one intention: this
// archive, on this server, now. Two buttons would leave an operator with a file
// uploaded and nothing apparently changed.
async function importBackup(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file || !backup.value) return

  if (!window.confirm(t('overview.importConfirm'))) return

  backup.value.working = t('overview.uploading')
  try {
    const body = new FormData()
    body.append('archive', file)
    const up = await fetch(apiURL('/api/backups/upload'), {
      method: 'POST',
      headers: { Authorization: `Bearer ${getToken()}` },
      credentials: 'same-origin',
      body,
    })
    const stored = await up.json().catch(() => null)
    if (!up.ok) throw new Error(stored?.error || t('overview.importFailed'))

    backup.value.working = t('overview.restoring')
    const params = keepAddresses.value ? '' : '?keepAddresses=false'
    await api.post(`/api/backups/${encodeURIComponent(stored.name)}/restore${params}`)

    // The panel is on its way out. Saying so and then reloading is what turns
    // "the page stopped working" into "it came back with the restored data".
    backup.value.working = t('overview.restarting')
    setTimeout(() => window.location.reload(), 7000)
  } catch (e) {
    backup.value.working = ''
    notify(e.message, 'error')
  }
}

// How much of this server is actually carrying traffic, which is the state an
// operator wants at a glance and the thing the two controls below act on.
const tunnelsUp = computed(() => ifaces.value.filter((i) => i.enabled && i.running).length)

const planeBusy = ref(false)
const confirmStop = ref(false)

async function tunnelAction(path, message) {
  planeBusy.value = true
  try {
    const res = await api.post(`/api/tunnels/${path}`)
    // A tunnel that would not come up is the whole content of the answer, so it
    // is said rather than folded into a count that looks like success.
    const failed = Object.entries(res?.failures || {})
    if (failed.length) {
      notify(failed.map(([name, why]) => `${name}: ${why}`).join('\n'), 'error')
    } else {
      notify(message.replace('{n}', res?.interfaces ?? 0), 'ok')
    }
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    planeBusy.value = false
  }
}

const restartTunnels = () => tunnelAction('restart', t('overview.restartedAll'))
const startTunnels = () => tunnelAction('start', t('overview.startedAll'))

function stopTunnels() {
  confirmStop.value = false
  return tunnelAction('stop', t('overview.stoppedAll'))
}

// The long history, which the short window on this page cannot answer.
//
// Was it like this last night, when did the disk start filling, was that spike
// at the time the customers complained — none of which could be asked of the
// panel at all before.
const history = ref(null)
const historyRange = ref('1h')

const historyRanges = ['5m', '1h', '6h', '24h', '48h', '7d']

// What each chart is and how to read it. Percentages share a 0-100 axis so a
// quiet hour is not stretched to look like a busy one; rates and counts scale
// to themselves, because there is no meaningful ceiling to hold them against.
const historyCharts = [
  {
    key: 'cpu', title: 'history.cpu', unit: 'percent',
    lines: [{ metric: 'cpu', color: 'var(--accent)', label: 'CPU' }],
  },
  {
    key: 'mem', title: 'history.memory', unit: 'percent',
    lines: [
      { metric: 'memory', color: 'var(--accent)', label: 'RAM' },
      { metric: 'swap', color: 'var(--warn)', label: 'Swap' },
    ],
  },
  {
    key: 'net', title: 'history.network', unit: 'rate',
    lines: [
      { metric: 'netDown', color: 'var(--ok)', label: 'history.down' },
      { metric: 'netUp', color: 'var(--accent)', label: 'history.up' },
    ],
  },
  {
    key: 'conns', title: 'history.connections', unit: 'count',
    lines: [
      { metric: 'tcp', color: 'var(--accent)', label: 'TCP' },
      { metric: 'udp', color: 'var(--warn)', label: 'UDP' },
    ],
  },
  {
    key: 'disk', title: 'history.disk', unit: 'percent',
    lines: [{ metric: 'disk', color: 'var(--warn)', label: 'history.disk' }],
  },
  {
    key: 'loadavg', title: 'history.loadAverage', unit: 'count',
    lines: [
      { metric: 'load1', color: 'var(--accent)', label: '1m' },
      { metric: 'load5', color: 'var(--warn)', label: '5m' },
      { metric: 'load15', color: 'var(--ok)', label: '15m' },
    ],
  },
  {
    key: 'panel', title: 'history.panel', unit: 'bytes',
    lines: [{ metric: 'panelMemory', color: 'var(--accent)', label: 'history.panelMemory' }],
  },
]

async function openHistory() {
  if (!history.value) history.value = { loading: true, series: {}, notice: '' }
  await loadHistory()
}

async function loadHistory() {
  if (!history.value) return
  history.value = { ...history.value, loading: true }
  try {
    const res = await api.get(`/api/system/history?range=${historyRange.value}`)
    history.value = { loading: false, series: res?.series || {}, notice: res?.notice || '' }
  } catch (e) {
    history.value = { loading: false, series: {}, notice: e.message }
  }
}

function setHistoryRange(r) {
  historyRange.value = r
  loadHistory()
}

// The store hands back {t, v} so the axis can be labelled; the chart wants the
// values. Kept apart rather than flattened on the server, because the times are
// what the ends of the axis are drawn from.
const seriesValues = (metric) => (history.value?.series?.[metric] || []).map((p) => p.v)

function historyAxis(chart) {
  const points = history.value?.series?.[chart.lines[0].metric] || []
  if (!points.length) return { from: '', to: '' }
  const pad = (n) => String(n).padStart(2, '0')
  const fmt = (sec) => {
    const d = new Date(sec * 1000)
    // Beyond a day the hour alone is ambiguous, so the date comes with it.
    if (historyRange.value === '7d' || historyRange.value === '48h') {
      return `${pad(d.getDate())}/${pad(d.getMonth() + 1)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
    }
    return `${pad(d.getHours())}:${pad(d.getMinutes())}`
  }
  return { from: fmt(points[0].t), to: fmt(points[points.length - 1].t) }
}

function historyPeak(chart) {
  const all = chart.lines.flatMap((l) => seriesValues(l.metric))
  if (!all.length) return null
  return Math.max(...all)
}

function historyFormat(unit, v) {
  if (v == null) return '\u2014'
  if (unit === 'percent') return `${v.toFixed(0)}%`
  if (unit === 'rate') return rate(v)
  if (unit === 'bytes') return bytes(v, store.locale)
  return nf(Math.round(v))
}

function historyLabel(l) {
  return l.label.includes('.') ? t(l.label) : l.label
}

// Whether there is a newer release, and whether this build could install it.
//
// A build with no signing key cannot, and says so instead of offering a button
// that fails: refusing to install something nobody vouched for is the point,
// not an accident.
const update = ref(null)
const updateBusy = ref(false)

async function loadUpdate() {
  try {
    update.value = await api.get('/api/system/update')
  } catch {
    // Quiet. The release list being unreachable is not something to interrupt
    // an operator looking at their own server for.
    update.value = null
  }
}

function openUpdate() {
  loadUpdate()
  updateOpen.value = true
}

const updateOpen = ref(false)

async function applyUpdate() {
  updateBusy.value = true
  try {
    const res = await api.post('/api/system/update')
    if (res?.updated) {
      notify(t('update.installed').replace('{v}', res.to), 'ok')
      // The panel is replacing itself and will be gone for a moment. Reloading
      // straight away would land on a closed port.
      setTimeout(() => window.location.reload(), 6000)
    } else {
      notify(res?.notice || t('update.upToDate'), 'ok')
      updateOpen.value = false
    }
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    updateBusy.value = false
  }
}

onMounted(loadUpdate)

const ipv4 = computed(() => (sys.value?.ipv4 || [])[0] || '\u2014')
const ipv6 = computed(() => (sys.value?.ipv6 || [])[0] || '—')
</script>

<template>
  <!-- The shape of the page while it is still arriving, rather than a spinner
       where the page will be. Only once the load has run long enough to be
       worth admitting to: this reading refreshes every three seconds, and a
       skeleton that flashed on each of them would be unusable. -->
  <div v-if="showSkeleton" class="ov-page" aria-hidden="true">
    <div class="ov-vitals">
      <div v-for="n in 4" :key="n" class="card ov-tile">
        <span class="sk" style="width: 42%"></span>
        <span class="sk sk-lg" style="width: 58%"></span>
        <span class="sk" style="width: 70%"></span>
        <span class="sk sk-chart"></span>
      </div>
    </div>
    <div class="ov-mid">
      <div class="card ov-wide">
        <span class="sk" style="width: 26%"></span>
        <span class="sk sk-tall"></span>
      </div>
      <div class="card ov-side">
        <span class="sk" style="width: 40%"></span>
        <span class="sk sk-lg" style="width: 55%"></span>
      </div>
    </div>
  </div>

  <div v-else-if="loading" class="ov-page"></div>

  <ErrorState v-else-if="loadError && !sys" :error="loadError" @retry="load()" />

  <div v-else-if="sys" class="ov-page">
    <div class="ov-actionbar">
      <div>
        <h1>{{ t('nav.overview') }}</h1>
        <p>{{ t('overview.subtitle') }}</p>
      </div>
      <!-- The things an operator reaches for while looking at this page. They
           were all a page away before, which is one page too many when
           something is wrong. -->
      <div class="spacer row ov-actions">
        <!-- What this server is actually doing, and the two controls for it.
             A state that cannot be acted on from where it is shown sends an
             operator to an SSH session to do the obvious thing. -->
        <span class="tag" :class="tunnelsUp ? 'active' : 'exhausted'" :title="t('overview.tunnelsHint')">
          <i v-if="tunnelsUp" class="dot"></i>
          {{ t('overview.tunnels') }}: {{ tunnelsUp }} / {{ ifaces.length }}
        </span>
        <button class="ov-version ltr" :title="t('update.check')" @click="openUpdate">
          {{ panel.version }}
          <!-- Only when there is something to install. A dot that is always
               there is a dot nobody looks at. -->
          <i v-if="update?.available" class="ov-version-dot"></i>
        </button>

        <button class="btn sm ghost" :title="t('overview.restartAllHint')"
                :disabled="planeBusy || !ifaces.length" @click="restartTunnels">
          <Icon name="refresh" :size="14" /><span class="lbl">{{ t('overview.restartAll') }}</span>
        </button>
        <button v-if="tunnelsUp" class="btn sm ghost" :title="t('overview.stopAllHint')"
                :disabled="planeBusy" @click="confirmStop = true">
          <Icon name="power" :size="14" /><span class="lbl">{{ t('overview.stopAll') }}</span>
        </button>
        <button v-else-if="ifaces.length" class="btn sm ghost" :title="t('overview.startAllHint')"
                :disabled="planeBusy" @click="startTunnels">
          <Icon name="power" :size="14" /><span class="lbl">{{ t('overview.startAll') }}</span>
        </button>

        <button class="btn sm ghost" :title="t('history.hint')" @click="openHistory">
          <Icon name="clock" :size="14" /><span class="lbl">{{ t('history.title') }}</span>
        </button>
        <button class="btn sm ghost" :title="t('overview.viewLogs')" @click="openLogs">
          <Icon name="info" :size="14" /><span class="lbl">{{ t('settings.tab.logs') }}</span>
        </button>
        <button class="btn sm ghost" :title="t('overview.backupTitle')" @click="openBackup">
          <Icon name="database" :size="14" /><span class="lbl">{{ t('settings.backup') }}</span>
        </button>
        <!-- The system report is reached from the page about this server. It
             was in the settings menu, which is for settings. -->
        <RouterLink to="/settings/system" class="btn sm ghost" :title="t('settings.tab.system')">
          <Icon name="server" :size="14" /><span class="lbl">{{ t('settings.tab.system') }}</span>
        </RouterLink>
        <RouterLink to="/settings" class="btn sm ghost" :title="t('nav.settings')">
          <Icon name="settings" :size="14" /><span class="lbl">{{ t('nav.settings') }}</span>
        </RouterLink>
      </div>
    </div>

    <div v-if="!panel.enforcementActive" class="ov-health">
      <Icon name="alert" :size="16" />
      <span>{{ panel.enforcementMessage }}</span>
    </div>

    <!-- Customers first. This is the page an operator lands on, and the
         question they arrive with is whether anything needs doing today - not
         how the machine's memory is. Each tile leads to the list already
         filtered, so noticing a problem and acting on it is one click. -->
    <div v-if="clients" class="ov-customers">
      <RouterLink
        v-for="tile in customerTiles"
        :key="tile.key"
        :to="tile.to"
        class="card ov-cust"
        :class="{ quiet: !tile.value, urgent: tile.urgent && tile.value }"
      >
        <span class="ov-cust-label"><i class="dot" :class="tile.tone"></i>{{ tile.label }}</span>
        <strong class="ov-cust-value num ltr">{{ nf(tile.value) }}</strong>
      </RouterLink>
    </div>

    <hr class="ov-rule" />

    <!-- Four vitals, each a number read against its own recent history. -->
    <div class="ov-vitals">
      <article v-for="v in vitals" :key="v.key" class="card ov-tile">
        <div class="ov-tile-head">
          <span class="ov-tile-icon"><Icon :name="v.icon" :size="15" /></span>
          <span class="ov-kicker">{{ v.label }}</span>
        </div>
        <div class="ov-tile-value">
          <span class="ov-tile-number" :style="{ color: vitalColor(v.percent) }">
            {{ v.percent.toFixed(1) }}
          </span>
          <span class="ov-tile-unit">%</span>
        </div>
        <div class="ov-tile-detail">{{ v.detail }}</div>
        <div class="ov-tile-foot">
          <span>{{ v.footLeft }}</span>
          <span>{{ v.footRight }}</span>
        </div>
        <div class="ov-tile-chart">
          <!-- Left to scale itself: the big number already states the level, so
               the line's job is the shape of the last few minutes. Pinned to
               0–100 a steady 43% would draw a flat slab and say nothing. -->
          <Sparkline
            :series="[{ data: v.data, color: vitalLine(v.percent) }]"
            :reference="[{ value: v.mean }]"
            :height="62"
          />
        </div>
      </article>
    </div>

    <div class="ov-mid">
      <!-- Throughput -->
      <article class="card ov-wide">
        <div class="ov-wide-head">
          <div>
            <div class="ov-kicker">{{ t('overview.throughput') }}</div>
            <div class="ov-sub">{{ t('overview.throughputSub') }}</div>
          </div>
          <div class="ov-wide-legend">
            <span class="ov-legend-label">
              <i class="ov-swatch up"></i>{{ t('overview.upload') }}
              <b class="ov-legend-num">{{ rate(sys.network.sentRate) }}</b>
            </span>
            <span class="ov-legend-label">
              <i class="ov-swatch down"></i>{{ t('overview.download') }}
              <b class="ov-legend-num">{{ rate(sys.network.recvRate) }}</b>
            </span>
          </div>
        </div>
        <div class="ov-wide-chart">
          <Sparkline
            :series="[
              { data: hist.down, color: 'var(--ok)', fill: false },
              { data: hist.up, color: 'var(--accent)', fill: false },
            ]"
            :reference="[
              { value: sys.network.recvRate, color: 'var(--ok)' },
              { value: sys.network.sentRate, color: 'var(--accent)' },
            ]"
            :height="186"
            :min="0"
          />
        </div>
        <div class="ov-wide-foot">
          <div class="ov-foot-part">
            <span class="ov-kicker">{{ t('overview.sent') }}</span>
            <span class="ov-foot-value">{{ bytes(sys.network.bytesSent, store.locale) }}</span>
          </div>
          <div class="ov-foot-sep"></div>
          <div class="ov-foot-part">
            <span class="ov-kicker">{{ t('overview.received') }}</span>
            <span class="ov-foot-value">{{ bytes(sys.network.bytesRecv, store.locale) }}</span>
          </div>
          <div class="ov-foot-sep"></div>
          <div class="ov-foot-part">
            <span class="ov-kicker">{{ t('overview.peak') }}</span>
            <span class="ov-foot-value">↓ {{ rate(downStat.max) }} · ↑ {{ rate(upStat.max) }}</span>
          </div>
        </div>
      </article>

      <!-- Connections -->
      <article class="card ov-wide">
        <div class="ov-wide-head ov-wide-head-stack">
          <div class="ov-kicker">{{ t('overview.connections') }}</div>
          <div class="ov-conn-total">
            <span class="ov-tile-number">{{ nf(totalConns) }}</span>
            <span class="ov-tile-unit">{{ t('overview.open') }}</span>
          </div>
          <div class="ov-conn-legend">
            <span class="ov-legend-label">
              <i class="ov-swatch tcp"></i>TCP
              <b class="ov-legend-num">{{ nf(sys.network.tcpConns) }}</b>
            </span>
            <span class="ov-legend-label">
              <i class="ov-swatch udp"></i>UDP
              <b class="ov-legend-num">{{ nf(sys.network.udpConns) }}</b>
            </span>
          </div>
        </div>
        <div class="ov-wide-chart">
          <Sparkline
            :series="[
              { data: hist.tcp, color: 'var(--accent)', fill: false },
              { data: hist.udp, color: 'var(--warn)', fill: false },
            ]"
            :reference="[
              { value: sys.network.tcpConns, color: 'var(--accent)' },
              { value: sys.network.udpConns, color: 'var(--warn)' },
            ]"
            :height="186"
          />
        </div>
      </article>
    </div>

    <!-- The strip 3x-ui closes its overview with: uptime, panel, addresses. -->
    <article class="card ov-strip">
      <div class="ov-strip-grid">
        <div class="ov-strip-cell">
          <div class="ov-strip-head">
            <Icon name="clock" :size="15" /><span class="ov-kicker">{{ t('overview.uptime') }}</span>
          </div>
          <div class="ov-strip-pair">
            <div>
              <span class="ov-kicker">{{ t('overview.panelUptime') }}</span>
              <span class="ov-strip-value ltr">{{ duration(panel.uptimeSec) }}</span>
            </div>
            <div>
              <span class="ov-kicker">{{ t('overview.hostUptime') }}</span>
              <span class="ov-strip-value ltr">{{ duration(sys.host.uptimeSec) }}</span>
            </div>
          </div>
        </div>

        <div class="ov-strip-cell">
          <div class="ov-strip-head">
            <Icon name="database" :size="15" /><span class="ov-kicker">W-UI</span>
          </div>
          <div class="ov-strip-pair">
            <div>
              <span class="ov-kicker">RAM</span>
              <span class="ov-strip-value">{{ bytes(sys.panel.memoryBytes, store.locale) }}</span>
            </div>
            <div>
              <span class="ov-kicker">{{ t('overview.goroutines') }}</span>
              <span class="ov-strip-value">{{ nf(sys.panel.goroutines) }}</span>
            </div>
          </div>
        </div>

        <div class="ov-strip-cell">
          <div class="ov-strip-head">
            <Icon name="globe" :size="15" />
            <span class="ov-kicker">{{ t('overview.addresses') }}</span>
            <button
              class="ov-eye"
              :aria-label="t('overview.toggleAddresses')"
              :aria-pressed="showIp"
              @click="showIp = !showIp"
            >
              <Icon :name="showIp ? 'eyeOff' : 'eye'" :size="14" />
            </button>
          </div>
          <div class="ov-strip-pair ov-ip" :class="{ 'ip-hidden': !showIp }">
            <div>
              <span class="ov-kicker">IPv4</span>
              <span class="ov-strip-value ov-mono">{{ ipv4 }}</span>
            </div>
            <div>
              <span class="ov-kicker">IPv6</span>
              <span class="ov-strip-value ov-mono ov-ip-v6">{{ ipv6 }}</span>
            </div>
          </div>
        </div>
      </div>
    </article>

    <!-- What this panel manages, which 3x-ui puts under Inbounds instead. -->
    <article class="card">
      <div class="card-head">
        <h2>{{ t('nav.interfaces') }}</h2>
        <span class="muted small ltr num">
          {{ nf(poolTotals.allocated) }} / {{ nf(poolTotals.capacity) }}
        </span>
        <RouterLink to="/clients" class="spacer small">
          {{ t('nav.clients') }}: {{ nf(clients.active) }} / {{ nf(clients.clients) }}
        </RouterLink>
      </div>

      <div v-if="!ifaces.length" class="empty">
        <p>{{ t('interface.noneYet') }}</p>
        <RouterLink to="/interfaces" class="btn primary sm">
          <Icon name="plus" :size="14" />{{ t('interface.create') }}
        </RouterLink>
      </div>

      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{{ t('interface.name') }}</th>
              <th>{{ t('client.protocol') }}</th>
              <th>{{ t('interface.endpoint') }}</th>
              <th>{{ t('interface.mode') }}</th>
              <th>{{ t('interface.subnet') }}</th>
              <th style="min-width: 190px">{{ t('interface.capacity') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="i in ifaces" :key="i.id">
              <td class="mono">{{ i.name }}</td>
              <td><span class="tag proto">{{ i.protocol }}</span></td>
              <td class="mono small">{{ i.endpointHost }}:{{ i.listenPort }}</td>
              <td>
                <span v-if="i.mode === 'amnezia'" class="tag active">
                  <Icon name="shield" :size="12" />AmneziaWG
                </span>
                <span v-else class="muted small">{{ t('interface.mode.standard') }}</span>
              </td>
              <td class="mono small">{{ i.subnet }}</td>
              <td>
                <div class="row">
                  <div class="meter" style="flex: 1">
                    <span
                      :class="i.capacity && i.allocated / i.capacity > 0.9 ? 'bad' : ''"
                      :style="{ width: Math.max((i.allocated / (i.capacity || 1)) * 100, 1) + '%' }"
                    ></span>
                  </div>
                  <span class="num small muted ltr">
                    {{ nf(i.allocated) }} / {{ nf(i.capacity) }}
                  </span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </article>
  </div>

  <!-- What release is available, and what installing it involves. -->
  <div v-if="updateOpen" class="modal-backdrop" @click.self="updateOpen = false">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="up-title">
      <div class="card-head">
        <h2 id="up-title">{{ t('update.title') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')" @click="updateOpen = false">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <p class="muted small ltr">
          {{ t('update.running') }}: <b>{{ update?.current || panel.version }}</b>
          <template v-if="update?.latest"> · {{ t('update.newest') }}: <b>{{ update.latest }}</b></template>
        </p>

        <p v-if="update?.notice" class="log-notice">{{ update.notice }}</p>

        <!-- Said before the button rather than after pressing it. -->
        <p v-if="update && !update.signed" class="log-notice">{{ t('update.unsigned') }}</p>

        <p v-else-if="update && !update.available" class="muted">{{ t('update.upToDate') }}</p>

        <template v-else-if="update?.available">
          <p class="hint">{{ t('update.whatHappens') }}</p>
          <pre v-if="update.notes" class="update-notes ltr">{{ update.notes }}</pre>
        </template>
      </div>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('action.cancel') }}</button>
        <button
          class="btn primary"
          :disabled="updateBusy || !update?.available || !update?.signed"
          @click="applyUpdate"
        >
          <span v-if="updateBusy" class="spin"></span>
          <span v-else>{{ t('update.install') }}</span>
        </button>
      </div>
    </div>
  </div>

  <!-- What this server has been doing, rather than what it is doing now. The
       page itself holds a few minutes; these are the questions that need
       last night. -->
  <div v-if="history" class="modal-backdrop" @click.self="history = null">
    <div class="modal hsmodal" role="dialog" aria-modal="true" aria-labelledby="hs-title">
      <div class="card-head">
        <h2 id="hs-title">
          {{ t('history.title') }}
          <span v-if="history.loading" class="spin sm"></span>
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')" @click="history = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="hs-ranges">
        <button
          v-for="r in historyRanges"
          :key="r"
          class="hs-range ltr"
          :class="{ on: historyRange === r }"
          @click="setHistoryRange(r)"
        >
          {{ r }}
        </button>
        <span class="spacer hs-note">{{ t('history.resolution') }}</span>
      </div>

      <div class="card-body hs-body">
        <p v-if="history.notice" class="log-notice">{{ history.notice }}</p>
        <div class="hs-grid">
          <article v-for="chart in historyCharts" :key="chart.key" class="hs-chart">
            <div class="hs-chart-head">
              <span class="ov-kicker">{{ t(chart.title) }}</span>
              <span class="hs-legend">
                <span v-for="l in chart.lines" :key="l.metric" class="hs-legend-item">
                  <i class="hs-swatch" :style="{ background: l.color }"></i>{{ historyLabel(l) }}
                </span>
              </span>
            </div>
            <Sparkline
              :series="chart.lines.map((l) => ({ data: seriesValues(l.metric), color: l.color, fill: false }))"
              :height="110"
              :min="0"
              :max="chart.unit === 'percent' ? 100 : null"
            />
            <div class="hs-chart-foot ltr">
              <span>{{ historyAxis(chart).from }}</span>
              <span class="hs-peak">{{ t('overview.peak') }} {{ historyFormat(chart.unit, historyPeak(chart)) }}</span>
              <span>{{ historyAxis(chart).to }}</span>
            </div>
          </article>
        </div>
      </div>
    </div>
  </div>

  <!-- Stopping every tunnel takes every customer offline at once, so it is
       asked for. Restarting is not: it changes no records and the worst case is
       a few seconds of reconnecting. -->
  <ConfirmDialog
    :open="confirmStop"
    :title="t('overview.stopAllTitle')"
    :body="t('overview.stopAllBody')"
    :confirm-label="t('overview.stopAll')"
    :danger="true"
    :busy="planeBusy"
    @confirm="stopTunnels"
    @cancel="confirmStop = false"
  />

  <!-- Backup, on the page an operator is already looking at. Taking one is the
       least of it: the reasons to open this are to get the archive off the
       server, to put one back, and to move a panel to another machine. -->
  <div v-if="backup" class="modal-backdrop" @click.self="backup.working || (backup = null)">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="bk-title">
      <div class="card-head">
        <h2 id="bk-title">{{ t('overview.backupTitle') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')"
                :disabled="!!backup.working" @click="backup = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body bk-body">
        <p v-if="backup.working" class="bk-working">
          <span class="spin sm"></span>{{ backup.working }}
        </p>
        <p v-else-if="backup.done" class="bk-done">{{ backup.done }}</p>

        <div class="bk-item">
          <div class="bk-meta">
            <div class="bk-title">{{ t('overview.takeBackup') }}</div>
            <p class="bk-desc">{{ t('overview.takeBackupDesc') }}</p>
          </div>
          <button class="btn primary" :disabled="!!backup.working" @click="backupNow">
            <Icon name="database" :size="15" />
          </button>
        </div>

        <div class="bk-item">
          <div class="bk-meta">
            <div class="bk-title">{{ t('overview.exportBackup') }}</div>
            <p class="bk-desc">
              <template v-if="backup.latest">
                <span class="ltr">{{ backup.latest.name }}</span>
                — {{ bytes(backup.latest.size, store.locale) }}
              </template>
              <template v-else>{{ t('overview.noArchiveYet') }}</template>
            </p>
          </div>
          <button class="btn" :disabled="!backup.latest || !!backup.working" @click="exportBackup">
            <Icon name="download" :size="15" />
          </button>
        </div>

        <div class="bk-item">
          <div class="bk-meta">
            <div class="bk-title">{{ t('overview.importBackup') }}</div>
            <p class="bk-desc">{{ t('overview.importBackupDesc') }}</p>
          </div>
          <label class="btn upload-btn" :class="{ disabled: !!backup.working }">
            <Icon name="upload" :size="15" />
            <input type="file" accept=".gz,.tar.gz,application/gzip"
                   :disabled="!!backup.working" @change="importBackup" />
          </label>
        </div>

        <!-- Out to the older archives, from the dialog somebody who wants one
             is already in. -->
        <RouterLink to="/settings/backups" class="bk-all" @click="backup = null">
          {{ t('overview.allBackups') }}
        </RouterLink>

        <!-- The one that matters when a panel moves house: the addresses in an
             archive name the server it was taken on. -->
        <label class="bk-keep">
          <input v-model="keepAddresses" type="checkbox" :disabled="!!backup.working" />
          <span>
            <b>{{ t('overview.keepAddresses') }}</b>
            <span class="bk-desc">{{ t('overview.keepAddressesDesc') }}</span>
          </span>
        </label>
      </div>
    </div>
  </div>

  <!-- The recent log, without leaving the page or opening an SSH session. -->
  <div v-if="logs" class="modal-backdrop" @click.self="logs = null">
    <div class="modal logmodal" role="dialog" aria-modal="true" aria-labelledby="lg-title">
      <div class="card-head">
        <h2 id="lg-title">
          {{ t('settings.tab.logs') }}
          <span v-if="logs.loading" class="spin sm"></span>
          <span v-else class="muted small">{{ tn('logs.count', logs.entries.length) }}</span>
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')" @click="logs = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <!-- Everything an operator reaches for while reading a log, on one row,
           because each of these was previously a reason to leave the panel and
           open an SSH session. -->
      <div class="log-toolbar">
        <div class="log-search">
          <Icon name="search" :size="14" />
          <input
            v-model="logQuery"
            type="search"
            :placeholder="t('logs.searchHint')"
            :aria-label="t('logs.search')"
            @input="onLogSearch"
          />
        </div>

        <select v-model="logLevel" :aria-label="t('logs.level')" @change="loadLogs">
          <option value="debug">{{ t('logs.all') }}</option>
          <option value="info">{{ t('logs.info') }}</option>
          <option value="warn">{{ t('logs.warn') }}</option>
          <option value="error">{{ t('logs.error') }}</option>
        </select>

        <select v-model.number="logLimit" :aria-label="t('logs.rows')" class="ltr" @change="loadLogs">
          <option :value="50">50</option>
          <option :value="100">100</option>
          <option :value="200">200</option>
          <option :value="500">500</option>
          <option :value="1000">1000</option>
        </select>

        <!-- The buffer is this process's memory and is empty after a restart,
             which is usually the thing being asked about. The journal has it. -->
        <select v-model="logSource" :aria-label="t('logs.source')" @change="loadLogs">
          <option value="panel">{{ t('logs.sourcePanel') }}</option>
          <option value="journal">{{ t('logs.sourceJournal') }}</option>
        </select>

        <label class="log-follow">
          <input v-model="logFollow" type="checkbox" />
          <span>{{ t('logs.follow') }}</span>
        </label>

        <div class="spacer row">
          <button class="btn sm icon ghost" :aria-label="t('common.refresh')" @click="loadLogs">
            <Icon name="refresh" :size="15" />
          </button>
          <button class="btn sm icon ghost" :aria-label="t('action.copy')"
                  :disabled="!logs.entries.length" @click="copyLogs">
            <Icon name="copy" :size="15" />
          </button>
          <button class="btn sm icon ghost" :aria-label="t('logs.download')"
                  :disabled="!logs.entries.length" @click="downloadLogs">
            <Icon name="download" :size="15" />
          </button>
        </div>
      </div>

      <!-- Machine output, so it is laid out left to right whatever the page
           around it is doing. In a right-to-left interface the columns of a
           log line come out in the opposite order and the punctuation inside a
           timestamp or an address moves, which is unreadable and looks like a
           fault in the panel rather than in the text direction. -->
      <div class="card-body log-body ltr-block">
        <p v-if="logs.notice" class="log-notice">{{ logs.notice }}</p>
        <p v-if="logs.loading && !logs.entries.length" class="muted">{{ t('common.loading') }}</p>
        <p v-else-if="!logs.entries.length && !logs.notice" class="muted">
          {{ logQuery ? t('logs.noMatch') : t('logs.none') }}
        </p>
        <ol v-else class="loglist">
          <li v-for="(e, i) in logs.entries" :key="i" :class="'lvl-' + (e.level || '').toLowerCase()">
            <span class="log-time" :title="e.time">{{ logStamp(e.time) }}</span>
            <span class="log-level">{{ (e.level || '').toUpperCase() }}</span>
            <span class="log-text">
              <span class="log-msg">{{ e.message }}</span>
              <template v-if="e.fields">
                <span v-for="(v, k) in e.fields" :key="k" class="log-field">
                  <b>{{ k }}</b>={{ v }}
                </span>
              </template>
            </span>
          </li>
        </ol>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ov-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ov-actionbar {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}
.ov-actionbar h1 {
  font-size: var(--t-xl);
  font-weight: 700;
  letter-spacing: -0.01em;
}
.ov-actionbar p {
  margin: 5px 0 0;
  color: var(--muted);
  font-size: var(--t-sm);
  max-width: 68ch;
}

.ov-health {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 11px 15px;
  border-radius: var(--radius-sm);
  background: var(--warn-soft);
  border: 1px solid rgba(224, 171, 52, 0.4);
  font-size: var(--t-sm);
  color: var(--ink);
}
.ov-health svg {
  color: var(--warn);
  flex-shrink: 0;
  margin-top: 2px;
}

.ov-rule {
  border: none;
  border-top: 1px solid var(--line);
  margin: 0;
}

/* ---------- vitals ---------- */
.ov-vitals {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}
.ov-tile {
  padding: 16px 0 0;
  overflow: hidden;
  transition: border-color 0.15s;
}
.ov-tile:hover {
  border-color: var(--faint);
}
.ov-tile-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 18px;
}
.ov-tile-icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 7px;
  background: var(--surface-3);
  color: var(--muted);
}
.ov-kicker {
  font-size: var(--t-xs);
  font-weight: 600;
  color: var(--muted);
  letter-spacing: 0.02em;
}
.ov-tile-value {
  display: flex;
  align-items: baseline;
  gap: 3px;
  padding: 12px 18px 0;
}
.ov-tile-number {
  font-size: 2rem;
  font-weight: 700;
  line-height: 1;
  font-variant-numeric: tabular-nums;
  direction: ltr;
}
.ov-tile-unit {
  font-size: var(--t-sm);
  color: var(--muted);
  font-weight: 500;
}
.ov-tile-detail {
  padding: 6px 18px 0;
  font-size: var(--t-xs);
  color: var(--ink-2);
  font-family: var(--mono);
  direction: ltr;
  unicode-bidi: isolate;
}
.ov-tile-foot {
  display: flex;
  justify-content: space-between;
  padding: 12px 18px 8px;
  font-size: var(--t-xs);
  color: var(--faint);
  font-variant-numeric: tabular-nums;
}
.ov-tile-chart {
  margin-top: auto;
}

/* ---------- mid grid ---------- */
.ov-mid {
  display: grid;
  grid-template-columns: 1.6fr 1fr;
  gap: 16px;
}
@media (max-width: 1000px) {
  .ov-mid {
    grid-template-columns: 1fr;
  }
}
.ov-wide {
  padding: 16px 0 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.ov-wide-head {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
  padding: 0 18px 14px;
}
.ov-wide-head-stack {
  flex-direction: column;
  gap: 8px;
}
.ov-sub {
  font-size: var(--t-xs);
  color: var(--faint);
  margin-top: 3px;
}
.ov-wide-legend {
  display: flex;
  gap: 18px;
  margin-inline-start: auto;
  flex-wrap: wrap;
}
.ov-legend-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--t-xs);
  color: var(--muted);
}
.ov-legend-num {
  color: var(--ink);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  direction: ltr;
  unicode-bidi: isolate;
}
.ov-swatch {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  flex-shrink: 0;
}
.ov-swatch.up,
.ov-swatch.tcp {
  background: var(--accent);
}
.ov-swatch.down {
  background: var(--ok);
}
.ov-swatch.udp {
  background: var(--warn);
}
.ov-wide-chart {
  flex: 1;
  min-height: 0;
}
.ov-conn-total {
  display: flex;
  align-items: baseline;
  gap: 5px;
}
.ov-conn-legend {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
}
.ov-wide-foot {
  display: flex;
  align-items: stretch;
  border-top: 1px solid var(--line-soft);
  margin-top: 12px;
}
.ov-foot-part {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 18px;
  min-width: 0;
}
.ov-foot-value {
  font-size: var(--t-base);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  direction: ltr;
  unicode-bidi: isolate;
}
.ov-foot-sep {
  width: 1px;
  background: var(--line-soft);
}

/* ---------- strip ---------- */
.ov-strip-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 1px;
  background: var(--line-soft);
}
.ov-strip-cell {
  background: var(--surface);
  padding: 16px 18px;
}
.ov-strip-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 14px;
}
.ov-strip-head svg {
  color: var(--muted);
}
.ov-strip-pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
.ov-strip-pair > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.ov-strip-value {
  font-size: var(--t-base);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.ov-mono {
  font-family: var(--mono);
  font-size: var(--t-xs);
  direction: ltr;
  unicode-bidi: isolate;
}
.ov-ip.ip-hidden .ov-strip-value {
  /* Blurred rather than replaced: the shape stays, the value does not, which
     is what an overview shown on a shared screen needs. */
  filter: blur(5px);
  user-select: none;
}
.ov-eye {
  margin-inline-start: auto;
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.ov-eye:hover {
  background: var(--surface-3);
  color: var(--ink);
}

@media (max-width: 620px) {
  .ov-wide-foot {
    flex-wrap: wrap;
  }
  .ov-foot-sep {
    display: none;
  }
  .ov-foot-part {
    border-top: 1px solid var(--line-soft);
  }
}
</style>
