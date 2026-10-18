<script setup>
// The overview's four vitals -- CPU, memory, swap, storage -- each a number
// read against its own recent history, and the network and connections
// beside them.
import { computed } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import Icon from '../Icon.vue'
import Sparkline from '../Sparkline.vue'

const props = defineProps({
  // The host's readings, with their recent history.
  system: { type: Object, required: true },
})
const sys = computed(() => props.system)
const hist = computed(() => sys.value?.history || {})
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const rate = (bps) => `${bytes(bps, store.locale)}/s`

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
</script>

<template>
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
</template>

<style scoped>
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
