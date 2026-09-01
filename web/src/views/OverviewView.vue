<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
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
    data.value = await api.fullOverview()
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

const ipv4 = computed(() => (sys.value?.ipv4 || [])[0] || '—')
const ipv6 = computed(() => (sys.value?.ipv6 || [])[0] || '—')
</script>

<template>
  <div v-if="loading" class="card"><div class="empty"><span class="spin"></span></div></div>

  <ErrorState v-else-if="loadError && !sys" :error="loadError" @retry="load()" />

  <div v-else-if="sys" class="ov-page">
    <div class="ov-actionbar">
      <div>
        <h1>{{ t('nav.overview') }}</h1>
        <p>{{ t('overview.subtitle') }}</p>
      </div>
      <div class="spacer row">
        <span class="tag" :class="panel.enforcementActive ? 'active' : 'exhausted'">
          <i v-if="panel.enforcementActive" class="dot"></i>
          {{ t('overview.enforcement') }}:
          {{ panel.enforcementActive ? t('overview.running') : t('overview.stopped') }}
        </span>
        <RouterLink to="/settings" class="btn sm">
          <Icon name="settings" :size="14" />{{ t('nav.settings') }}
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
