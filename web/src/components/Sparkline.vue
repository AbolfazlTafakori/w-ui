<script setup>
import { computed, getCurrentInstance } from 'vue'

// A hand-drawn SVG sparkline rather than a charting library: the whole need is
// one or two smoothed series with an optional reference line, and pulling in a
// chart package for that would cost more bundle than the rest of the app.
// Gradient ids have to be unique to this chart and stable for its lifetime.
//
// They used to be Math.random() called inside a computed, so they were
// regenerated on every recompute -- and the page they sit on polls every three
// seconds, which meant six gradients being torn down and rebuilt continuously.
//
// The instance id rather than a module counter: a counter declared at the top
// of `<script setup>` is per-instance and resets to zero for every chart, which
// gives all of them the same id and has them all reading one gradient.
const uid = `sg${getCurrentInstance()?.uid ?? 0}`

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

// Where the series currently sits: the right-hand end of the line.
function tipFor(data) {
  const pts = points(data)
  if (!pts.length) return null
  const [x, y] = pts[pts.length - 1]
  return { x, y }
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
      // The last point is where the series is now, which on a live chart is the
      // one value anybody is actually reading.
      tip: tipFor(s.data),
      id: `${uid}-${i}`,
    })),
)

const refLines = computed(() =>
  props.reference
    .filter((r) => Number.isFinite(r.value))
    .map((r, i) => ({ key: i, y: scaleY(r.value), color: r.color || 'var(--faint)' })),
)
</script>

<template>
  <div class="spark-wrap" :style="{ height: height + 'px' }">
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

    <!-- The current value, marked in HTML rather than in the SVG. The viewBox is
         stretched to the container width, so a circle drawn inside it comes out
         as an ellipse; positioned by percentage out here it is always round.
         This is the one point on a live chart anybody is actually reading. -->
    <span
      v-for="s in drawn"
      v-show="s.tip"
      :key="`tip${s.key}`"
      class="tip"
      :style="{
        left: `${(s.tip.x / W) * 100}%`,
        top: `${(s.tip.y / height) * 100}%`,
        '--tip-color': s.color,
      }"
    ></span>
  </div>
</template>

<style scoped>
.spark-wrap {
  position: relative;
  width: 100%;
}
.spark {
  display: block;
  width: 100%;
  overflow: visible;
}
.tip {
  position: absolute;
  width: 5px;
  height: 5px;
  margin: -2.5px 0 0 -2.5px;
  border-radius: 50%;
  background: var(--tip-color, var(--accent));
  /* The halo is a spread shadow rather than a second element: one node, and it
     cannot fall out of step with the dot it belongs to. */
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--tip-color, var(--accent)) 22%, transparent);
  pointer-events: none;
}
</style>
