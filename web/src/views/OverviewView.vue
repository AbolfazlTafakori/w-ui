<script setup>
import { ref, computed, watch, onMounted, onUnmounted, onBeforeUnmount } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Sparkline from '../components/Sparkline.vue'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'

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

async function backupNow() {
  busy.value = true
  try {
    const a = await api.post('/api/backups')
    notify(t('overview.backupTaken').replace('{name}', a.name), 'ok')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

const ipv4 = computed(() => (sys.value?.ipv4 || [])[0] || '—')
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
        <span class="tag" :class="panel.enforcementActive ? 'active' : 'exhausted'">
          <i v-if="panel.enforcementActive" class="dot"></i>
          {{ t('overview.enforcement') }}:
          {{ panel.enforcementActive ? t('overview.running') : t('overview.stopped') }}
        </span>

        <button class="btn sm ghost" :title="t('overview.viewLogs')" @click="openLogs">
          <Icon name="info" :size="14" /><span class="lbl">{{ t('settings.tab.logs') }}</span>
        </button>
        <button class="btn sm ghost" :title="t('overview.backupNow')" :disabled="busy" @click="backupNow">
          <Icon name="database" :size="14" /><span class="lbl">{{ t('settings.backup') }}</span>
        </button>
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
