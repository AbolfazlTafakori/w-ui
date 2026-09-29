<script setup>
import { ref, computed, watch, onMounted, onUnmounted, onBeforeUnmount } from 'vue'
import { RouterLink } from 'vue-router'
import { api, apiURL, getToken } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Sparkline from '../components/Sparkline.vue'
import Icon from '../components/Icon.vue'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'

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

// Four vitals, in the classic panel's order. Each carries its own recent history so the
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

const logs = ref(null)
const logLevel = ref('info')
const logLimit = ref(200)
const logQuery = ref('')
const logSource = ref('panel')
const logFollow = ref(false)
let logTimer = null

// Refreshing while the operator is reading is the point of following, so it has
// to be slow enough not to move the page under them and quick enough to be
// worth having on. Five seconds is what the classic panel settled on and it is right.
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

// Backup and restore, laid out as the classic panel's dialog: two lines, each
// one button. "Back up" takes a fresh archive and hands it to the browser in
// the same click -- it used to take one on the server and offer the newest
// archive as a second step, which could be an older, automatic one. "Restore"
// picks a file, says what it is and what will happen, and only then replaces
// anything; it follows the panel through its restart and reloads once it is
// back, rather than after a guess of seven seconds.
//
// backup.step is 'choose' (the two lines), 'confirm' (a file picked),
// 'working' (backing up, uploading or restoring) or 'failed'.
const backup = ref(null)
const keepAddresses = ref(true)
const restoreInput = ref(null)

function openBackup() {
  keepAddresses.value = true
  backup.value = { step: 'choose', line: '', share: null, file: null, error: '', done: '' }
}

function closeBackup() {
  if (backup.value?.step === 'working') return
  backup.value = null
}

// Fetched rather than linked: the archive holds every key on the server, and a
// plain link carries no session.
async function backupNow() {
  if (!backup.value) return
  backup.value = { ...backup.value, step: 'working', line: t('overview.backingUp'), share: null, done: '', error: '' }
  try {
    const a = await api.post('/api/backups')
    backup.value.line = t('overview.preparingDownload')
    const res = await fetch(apiURL(`/api/backups/${encodeURIComponent(a.name)}`), {
      headers: { Authorization: `Bearer ${getToken()}` },
      credentials: 'same-origin',
    })
    if (!res.ok) throw new Error((await res.text()) || res.statusText)
    const url = URL.createObjectURL(await res.blob())
    const link = document.createElement('a')
    link.href = url
    link.download = a.name
    document.body.appendChild(link)
    link.click()
    link.remove()
    setTimeout(() => URL.revokeObjectURL(url), 0)
    if (backup.value) backup.value = { ...backup.value, step: 'choose', done: t('overview.backupDownloaded').replace('{name}', a.name) }
  } catch (e) {
    if (backup.value) backup.value = { ...backup.value, step: 'choose', error: e.message }
  }
}

function pickRestore() {
  restoreInput.value?.click()
}

function restoreChosen(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file || !backup.value) return
  backup.value = { ...backup.value, step: 'confirm', file, error: '', done: '' }
}

// Sent with XMLHttpRequest for its upload progress, which fetch does not
// report: an archive of a hundred megabytes over a slow link is minutes of
// a bar that moves, or minutes of wondering.
function upload(file, onShare) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', apiURL('/api/backups/upload'))
    xhr.setRequestHeader('Authorization', `Bearer ${getToken()}`)
    xhr.withCredentials = true
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onShare(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      let body = null
      try { body = JSON.parse(xhr.responseText) } catch { body = null }
      if (xhr.status >= 200 && xhr.status < 300 && body?.name) resolve(body)
      else reject(new Error(body?.error || t('overview.importFailed')))
    }
    xhr.onerror = () => reject(new Error(t('overview.uploadCut')))
    const form = new FormData()
    form.append('archive', file)
    xhr.send(form)
  })
}

async function restoreNow() {
  const file = backup.value?.file
  if (!file) return
  backup.value = { ...backup.value, step: 'working', line: t('overview.uploading'), share: 0, error: '' }
  try {
    const stored = await upload(file, (share) => {
      if (backup.value) backup.value.share = share
    })
    backup.value = { ...backup.value, line: t('overview.restoring'), share: null }
    const params = keepAddresses.value ? '' : '?keepAddresses=false'
    await api.post(`/api/backups/${encodeURIComponent(stored.name)}/restore${params}`)
  } catch (e) {
    if (backup.value) backup.value = { ...backup.value, step: 'failed', error: e.message }
    return
  }
  // Restored. The panel ends itself to start again on the restored files.
  backup.value = { ...backup.value, line: t('overview.restarting') }
  waitForRestart()
}

// Waits for the panel to go away and come back, then reloads onto it. Any
// answer counts as back -- a sign-in refused included, since the restored
// archive brings its own accounts.
async function waitForRestart() {
  const started = Date.now()
  let wentAway = false
  while (Date.now() - started < 3 * 60 * 1000) {
    await new Promise((r) => setTimeout(r, 1000))
    let answered = false
    try {
      const res = await fetch(apiURL('/api/meta'), { credentials: 'same-origin', cache: 'no-store' })
      answered = res.status < 500
    } catch {
      answered = false
    }
    if (!answered) wentAway = true
    else if (wentAway) {
      window.location.reload()
      return
    }
  }
  if (backup.value) backup.value = { ...backup.value, step: 'failed', error: t('overview.restoreNotBack') }
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

async function loadUpdate(fresh = false) {
  try {
    update.value = await api.get(fresh ? '/api/system/update?fresh=1' : '/api/system/update')
  } catch {
    // Quiet. The release list being unreachable is not something to interrupt
    // an operator looking at their own server for.
    update.value = null
  }
}

function openUpdate() {
  loadUpdate(true)
  // An install started before this page was loaded -- in another tab, or
  // before a reload -- is picked up where it is rather than offered again.
  if (!installing.value) followInstall(true)
  updateOpen.value = true
}

const updateOpen = ref(false)

// An install under way. The panel downloads the release in the background and
// answers at once, because fetching it can outlast a request; the page then
// asks every second how far it has got. Once the new binary is in place the
// panel restarts, is briefly not there at all, and comes back reporting the
// new version -- which is when the page reloads onto it.
//
// installing: { stage, from, to, received, total, error } while followed.
const installing = ref(null)
// v2.3.0 however the build was stamped; a development build says so as is.
const versionLabel = computed(() => {
  const v = String(panel.value?.version || '')
  return /^v?[0-9]/.test(v) ? 'v' + v.replace(/^v/, '') : v
})
let followTimer = null
let followStarted = 0
// How long a panel may take to come back before the page stops waiting and
// says so. A restart takes seconds; this is for one that did not happen.
const COME_BACK_MS = 3 * 60 * 1000

async function applyUpdate() {
  updateBusy.value = true
  try {
    const res = await api.post('/api/system/update')
    if (res?.started) {
      installing.value = { stage: 'downloading', from: res.from, to: res.to, received: 0, total: 0 }
      followInstall()
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

// followInstall asks how the install is going until it ends. probe is a
// single look on opening the dialog: it follows only if one is under way.
async function followInstall(probe = false) {
  clearTimeout(followTimer)
  if (!probe && !followStarted) followStarted = Date.now()
  let res = null
  try {
    res = await api.get('/api/system/update/progress')
  } catch {
    // Not answering is expected once the install is in place: the panel is
    // restarting. Before that it is a network hiccup, and asking again is
    // the answer to both.
    res = null
  }
  if (probe) {
    const p = res?.progress
    if (!p || !['downloading', 'verifying', 'installing', 'restarting'].includes(p.stage)) return
    installing.value = { ...p }
    followStarted = Date.now()
    updateOpen.value = true
  } else if (res) {
    const from = installing.value?.from
    if (from && res.current && res.current !== from) {
      // Back, on the new build.
      installing.value = { ...installing.value, stage: 'back' }
      notify(t('update.back').replace('{v}', res.current), 'ok')
      setTimeout(() => window.location.reload(), 1200)
      return
    }
    const p = res.progress || {}
    if (p.stage === 'failed') {
      installing.value = { ...installing.value, stage: 'failed', error: p.error }
      followStarted = 0
      return
    }
    // A panel that answers with no install under way and the old version has
    // restarted without taking the new build -- or is another process.
    if (!p.stage && installing.value?.stage === 'restarting') {
      installing.value = { ...installing.value, stage: 'failed', error: t('update.cameBackOld') }
      followStarted = 0
      return
    }
    if (p.stage) installing.value = { ...installing.value, ...p }
  } else if (installing.value && installing.value.stage !== 'downloading') {
    installing.value = { ...installing.value, stage: 'restarting' }
  }
  if (Date.now() - followStarted > COME_BACK_MS && installing.value?.stage === 'restarting') {
    installing.value = { ...installing.value, stage: 'failed', error: t('update.notBack') }
    followStarted = 0
    return
  }
  followTimer = setTimeout(() => followInstall(), 1000)
}

// Back on the new build, told by the version the overview reads every three
// seconds anyway. Every panel reports that, where the progress address exists
// only from this release on: a panel that came back on a build without it
// would otherwise leave this page waiting.
watch(() => panel.value?.version, (v) => {
  const p = installing.value
  if (!p || !p.from || !v || v === p.from || p.stage === 'back' || p.stage === 'failed') return
  clearTimeout(followTimer)
  installing.value = { ...p, stage: 'back' }
  notify(t('update.back').replace('{v}', v), 'ok')
  setTimeout(() => window.location.reload(), 1200)
})

// The share of the release downloaded, for the bar; null when the server did
// not say how big it is.
const installShare = computed(() => {
  const p = installing.value
  if (!p || !p.total) return null
  return Math.min(100, Math.round((p.received / p.total) * 100))
})

const installLine = computed(() => {
  const p = installing.value
  if (!p) return ''
  switch (p.stage) {
    case 'downloading':
      return p.total
        ? t('update.downloading').replace('{r}', bytes(p.received)).replace('{t}', bytes(p.total))
        : t('update.downloadingNoSize').replace('{r}', bytes(p.received))
    case 'verifying': return t('update.verifying')
    case 'installing': return t('update.installing')
    case 'restarting': return t('update.restarting').replace('{v}', p.to || '')
    case 'back': return t('update.back').replace('{v}', p.to || '')
    default: return ''
  }
})

function retryInstall() {
  installing.value = null
  loadUpdate(true)
}

onMounted(() => {
  loadUpdate()
  followInstall(true)
})
onBeforeUnmount(() => clearTimeout(followTimer))

const ipv4 = computed(() => (sys.value?.ipv4 || [])[0] || '\u2014')
const ipv6 = computed(() => (sys.value?.ipv6 || [])[0] || '—')
</script>

<template>
  <!-- The shape of the page while it is still arriving, rather than a spinner
       where the page will be. Only once the load has run long enough to be
       worth admitting to: this reading refreshes every three seconds, and a
       skeleton that flashed on each of them would be unusable. -->
  <PageSpin v-if="showSkeleton" />

  <div v-else-if="loading" class="ov-page"></div>

  <ErrorState v-else-if="loadError && !sys" :error="loadError" @retry="load()" />

  <div v-else-if="sys" class="ov-page">
    <!-- Laid out as the classic panel's overview bar: at the start, what this
         server is doing and which version it runs, and beside it the newer
         release when there is one; at the end, the things an operator reaches
         for while looking at this page. The page's name is for screen readers:
         the sidebar already says where you are. -->
    <div class="ov-actionbar">
      <h1 class="sr-only">{{ t('nav.overview') }}</h1>
      <div class="ov-state">
        <span class="ov-state-pill" :class="tunnelsUp ? 'up' : 'down'" :title="t('overview.tunnelsHint')">
          <i class="ov-state-dot"></i>
          <span>{{ t('overview.tunnels') }} · {{ tunnelsUp }} / {{ ifaces.length }}</span>
          <button type="button" class="ov-version ltr" :title="t('update.check')" @click="openUpdate">{{ versionLabel }}</button>
        </span>
        <!-- An install under way, or a newer release to install. Only an
             operator who can install it is told: the check is theirs alone. -->
        <button v-if="installing && installing.stage !== 'failed'" type="button" class="ov-update" @click="updateOpen = true">
          <span class="spin sm"></span>
          <span>{{ t('update.updating') }}</span>
        </button>
        <button v-else-if="update?.available && update?.latest" type="button" class="ov-update" @click="openUpdate">
          <AntIcon name="CloudUploadOutlined" />
          <span>{{ t('update.cta') }} <bdi class="ltr">v{{ update.latest }}</bdi></span>
        </button>
      </div>
      <div class="spacer row ov-actions">
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
        <button class="btn sm ghost" :title="t('overview.backupRestore')" @click="openBackup">
          <Icon name="database" :size="14" /><span class="lbl">{{ t('overview.backupRestore') }}</span>
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

    <!-- The strip the classic panel closes its overview with: uptime, panel, addresses. -->
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

    <!-- What this panel manages, which the classic panel puts under Inbounds instead. -->
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

        <template v-else-if="update?.available && !installing">
          <p class="hint">{{ t('update.whatHappens') }}</p>
          <pre v-if="update.notes" class="update-notes ltr">{{ update.notes }}</pre>
        </template>

        <!-- The install, followed to the panel coming back on the new build.
             Closing the dialog does not stop it; the page goes on following
             and reloads when it is done. -->
        <div v-if="installing" class="update-progress" role="status" aria-live="polite">
          <template v-if="installing.stage === 'failed'">
            <p class="log-notice">{{ t('update.failed') }}</p>
            <p class="update-error ltr">{{ installing.error }}</p>
          </template>
          <template v-else>
            <div class="update-bar" :class="{ busy: installShare === null || installing.stage !== 'downloading' }"
                 role="progressbar" aria-valuemin="0" aria-valuemax="100"
                 :aria-valuenow="installing.stage === 'downloading' ? installShare : null">
              <i :style="{ width: (installing.stage === 'downloading' && installShare !== null ? installShare : 100) + '%' }"></i>
            </div>
            <p class="muted small">{{ installLine }}</p>
          </template>
        </div>
      </div>

      <div class="modal-foot">
        <template v-if="installing && installing.stage === 'failed'">
          <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('common.close') }}</button>
          <button type="button" class="btn primary" @click="retryInstall">{{ t('update.tryAgain') }}</button>
        </template>
        <template v-else-if="installing">
          <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('update.hide') }}</button>
        </template>
        <template v-else>
          <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('action.cancel') }}</button>
          <button
            class="btn primary"
            :disabled="updateBusy || !update?.available || !update?.signed"
            @click="applyUpdate"
          >
            <span v-if="updateBusy" class="spin"></span>
            <span v-else>{{ t('update.install') }}</span>
          </button>
        </template>
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

  <!-- Backup and restore, as the classic panel's dialog: one line to back up,
       one to restore, each a single button. -->
  <div v-if="backup" class="modal-backdrop" @click.self="closeBackup">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="bk-title">
      <div class="card-head">
        <h2 id="bk-title">{{ t('overview.backupRestore') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')"
                :disabled="backup.step === 'working'" @click="closeBackup">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body bk-body">
        <template v-if="backup.step === 'choose'">
          <p v-if="backup.done" class="bk-done" role="status">{{ backup.done }}</p>
          <p v-if="backup.error" class="bk-error" role="alert">{{ backup.error }}</p>

          <div class="bk-item">
            <div class="bk-meta">
              <div class="bk-title">{{ t('overview.backupLine') }}</div>
              <p class="bk-desc">{{ t('overview.backupLineDesc') }}</p>
            </div>
            <button type="button" class="btn primary" @click="backupNow">
              <Icon name="download" :size="15" /><span>{{ t('overview.backupButton') }}</span>
            </button>
          </div>

          <div class="bk-item">
            <div class="bk-meta">
              <div class="bk-title">{{ t('overview.restoreLine') }}</div>
              <p class="bk-desc">{{ t('overview.restoreLineDesc') }}</p>
            </div>
            <button type="button" class="btn" @click="pickRestore">
              <Icon name="upload" :size="15" /><span>{{ t('overview.restoreButton') }}</span>
            </button>
            <input ref="restoreInput" type="file" class="sr-only" tabindex="-1" aria-hidden="true"
                   accept=".gz,.tar.gz,application/gzip" @change="restoreChosen" />
          </div>

          <RouterLink to="/settings/backups" class="bk-all" @click="backup = null">
            {{ t('overview.allBackups') }}
          </RouterLink>
        </template>

        <!-- A file picked: what it is, what happens, and the one choice that
             matters when a panel moves to another machine. Nothing is sent
             until Restore is pressed. -->
        <template v-else-if="backup.step === 'confirm'">
          <div class="bk-file">
            <Icon name="database" :size="16" />
            <span class="ltr bk-file-name">{{ backup.file.name }}</span>
            <span class="muted">{{ bytes(backup.file.size, store.locale) }}</span>
          </div>
          <p class="bk-warn">{{ t('overview.restoreWarn') }}</p>
          <label class="bk-keep">
            <input v-model="keepAddresses" type="checkbox" />
            <span>
              <b>{{ t('overview.keepAddresses') }}</b>
              <span class="bk-desc">{{ t('overview.keepAddressesDesc') }}</span>
            </span>
          </label>
        </template>

        <template v-else-if="backup.step === 'working'">
          <div class="update-progress" role="status" aria-live="polite">
            <div class="update-bar" :class="{ busy: backup.share === null }" role="progressbar"
                 aria-valuemin="0" aria-valuemax="100" :aria-valuenow="backup.share">
              <i :style="{ width: (backup.share === null ? 100 : backup.share) + '%' }"></i>
            </div>
            <p class="muted small">
              {{ backup.line }}<template v-if="backup.share !== null"> {{ backup.share }}%</template>
            </p>
          </div>
        </template>

        <template v-else-if="backup.step === 'failed'">
          <p class="bk-error" role="alert">{{ backup.error }}</p>
        </template>
      </div>

      <div v-if="backup.step === 'confirm'" class="modal-foot">
        <button type="button" class="btn ghost" @click="backup.step = 'choose'">{{ t('action.cancel') }}</button>
        <button type="button" class="btn danger" @click="restoreNow">{{ t('overview.restoreNow') }}</button>
      </div>
      <div v-else-if="backup.step === 'failed'" class="modal-foot">
        <button type="button" class="btn ghost" @click="closeBackup">{{ t('common.close') }}</button>
        <button type="button" class="btn" @click="backup.step = 'choose'">{{ t('update.tryAgain') }}</button>
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
  --ov-gap: 12px;
  display: flex;
  flex-direction: column;
  gap: var(--ov-gap);
}

.ov-actionbar {
  display: flex;
  align-items: center;
  gap: 12px 16px;
  flex-wrap: wrap;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--line);
}
.ov-state {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
/* "● Tunnels · 3 / 3  v2.3.0": the state and the version in one pill. */
.ov-state-pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--ink);
  font-size: var(--t-sm);
  white-space: nowrap;
}
.ov-state-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ok);
  flex: none;
}
.ov-state-pill.down .ov-state-dot { background: var(--bad); }
.ov-state-pill .ov-version { font-size: var(--t-sm); color: var(--muted); }

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
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--ov-gap, 12px);
}
/* IndexPage.css: four tiles, two under 1100, one under 560. */
@media (max-width: 1100px) {
  .ov-vitals { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 560px) {
  .ov-vitals { grid-template-columns: minmax(0, 1fr); }
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
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
  gap: var(--ov-gap, 12px);
}
@media (max-width: 1100px) {
  .ov-mid {
    grid-template-columns: minmax(0, 1fr);
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
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  background: var(--line-soft);
}
@media (max-width: 1100px) {
  .ov-strip-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 560px) {
  .ov-strip-grid { grid-template-columns: minmax(0, 1fr); }
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

@media (max-width: 768px) {
  /* IndexPage.css: the action bar's buttons take the whole width, spread. */
  .ov-actionbar .ov-actions {
    margin-inline-start: 0;
    width: 100%;
    justify-content: space-between;
  }
  .ov-page { --ov-gap: 8px; }
}
@media (max-width: 560px) {
  .ov-wide-foot {
    flex-direction: column;
    flex-wrap: wrap;
    gap: 10px;
  }
  .ov-foot-sep {
    display: none;
  }
  .ov-foot-part {
    border-top: 1px solid var(--line-soft);
  }
}
</style>
