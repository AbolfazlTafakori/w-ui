<script setup>
import { computed } from 'vue'

// A hand-drawn SVG sparkline rather than a charting library: the whole need is
// one or two smoothed series with an optional reference line, and pulling in a
// chart package for that would cost more bundle than the rest of the app.
const props = defineProps({
  // One or two series: [{ data: number[], color: string, fill: boolean }]
  series: { type: Array, required: true },
  height: { type: Number, default: 62 },
  // Draws a dashed horizontal line, used for the mean or the current value.
  reference: { type: Array, default: () => [] },
  // Forces the vertical scale, so a percentage chart does not rescale itself
  // to its own noise and make 2% look like a spike.
  min: { type: Number, default: null },
  max: { type: Number, default: null },
})

const W = 300 // viewBox width; the SVG scales to its container

const bounds = computed(() => {
  const all = props.series.flatMap((s) => s.data || [])
  if (!all.length) return { lo: 0, hi: 1 }

  let lo = props.min ?? Math.min(...all)
  let hi = props.max ?? Math.max(...all)
  if (hi === lo) hi = lo + 1
  // A little headroom keeps the peak off the top edge.
  if (props.max === null) hi += (hi - lo) * 0.12
  return { lo, hi }
})

function scaleY(v) {
  const { lo, hi } = bounds.value
  const t = (v - lo) / (hi - lo)
  return props.height - t * props.height
}

function points(data) {
  if (!data || data.length === 0) return []
  if (data.length === 1) {
    return [
      [0, scaleY(data[0])],
      [W, scaleY(data[0])],
    ]
  }
  const step = W / (data.length - 1)
  return data.map((v, i) => [i * step, scaleY(v)])
}

// A monotone-ish cubic through the points: smoother than straight segments,
// and without the overshoot a naive spline gives on spiky data.
function pathFor(data) {
  const pts = points(data)
  if (!pts.length) return ''
  let d = `M ${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`
  for (let i = 1; i < pts.length; i++) {
    const [x0, y0] = pts[i - 1]
    const [x1, y1] = pts[i]
    const cx = (x0 + x1) / 2
    d += ` C ${cx.toFixed(2)} ${y0.toFixed(2)}, ${cx.toFixed(2)} ${y1.toFixed(2)}, ${x1.toFixed(2)} ${y1.toFixed(2)}`
  }
  return d
}

function areaFor(data) {
  const line = pathFor(data)
  if (!line) return ''
  const pts = points(data)
  const lastX = pts[pts.length - 1][0]
  return `${line} L ${lastX.toFixed(2)} ${props.height} L 0 ${props.height} Z`
}

const drawn = computed(() =>
  props.series
    .filter((s) => s.data && s.data.length)
    .map((s, i) => ({
      key: i,
      color: s.color || 'var(--accent)',
      fill: s.fill !== false,
      line: pathFor(s.data),
      area: areaFor(s.data),
      id: `sg-${Math.random().toString(36).slice(2, 8)}`,
    })),
)

const refLines = computed(() =>
  props.reference
    .filter((r) => Number.isFinite(r.value))
    .map((r, i) => ({ key: i, y: scaleY(r.value), color: r.color || 'var(--faint)' })),
)
</script>

<template>
  <svg
    class="spark"
    :viewBox="`0 0 ${W} ${height}`"
    :style="{ height: height + 'px' }"
    preserveAspectRatio="none"
    aria-hidden="true"
  >
    <defs>
      <linearGradient v-for="s in drawn" :id="s.id" :key="s.id" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" :stop-color="s.color" stop-opacity="0.28" />
        <stop offset="100%" :stop-color="s.color" stop-opacity="0" />
      </linearGradient>
    </defs>

    <line
      v-for="r in refLines"
      :key="`r${r.key}`"
      x1="0"
      :y1="r.y"
      :x2="W"
      :y2="r.y"
      :stroke="r.color"
      stroke-width="1"
      stroke-dasharray="3 3"
      opacity="0.55"
      vector-effect="non-scaling-stroke"
    />

    <template v-for="s in drawn" :key="s.key">
      <path v-if="s.fill" :d="s.area" :fill="`url(#${s.id})`" stroke="none" />
      <path
        :d="s.line"
        fill="none"
        :stroke="s.color"
        stroke-width="1.75"
        stroke-linecap="round"
        stroke-linejoin="round"
        vector-effect="non-scaling-stroke"
      />
    </template>
  </svg>
</template>

<style scoped>
.spark {
  display: block;
  width: 100%;
  overflow: visible;
}
</style>
