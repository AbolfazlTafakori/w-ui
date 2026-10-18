<script setup>
// What this server has been doing, from the overview: the long history the
// page's few minutes cannot answer, at a range of resolutions.
import { ref, onMounted } from 'vue'
import { api } from '../../lib/api.js'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import Icon from '../Icon.vue'
import Sparkline from '../Sparkline.vue'

const emit = defineEmits(['close'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const rate = (bps) => `${bytes(bps, store.locale)}/s`

// The long history, which the short window on this page cannot answer.
//
// Was it like this last night, when did the disk start filling, was that spike
// at the time the customers complained — none of which could be asked of the
// panel at all before.
const history = ref({ loading: true, series: {}, notice: '' })
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

onMounted(loadHistory)

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
</script>

<template>
  <!-- What this server has been doing, rather than what it is doing now. The
       page itself holds a few minutes; these are the questions that need
       last night. -->
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal hsmodal" role="dialog" aria-modal="true" aria-labelledby="hs-title">
      <div class="card-head">
        <h2 id="hs-title">
          {{ t('history.title') }}
          <span v-if="history.loading" class="spin sm"></span>
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')" @click="emit('close')">
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
</template>
