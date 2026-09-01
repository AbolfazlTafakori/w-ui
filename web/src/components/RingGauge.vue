<script setup>
import { computed } from 'vue'

// The ring 3x-ui puts across the top of its overview. The percentage is the
// point, so it sits in the middle of the arc rather than beside it.
const props = defineProps({
  percent: { type: Number, default: 0 },
  size: { type: Number, default: 52 },
  thickness: { type: Number, default: 4 },
  // Above these thresholds the ring changes colour, so a pool filling up or a
  // customer near their ceiling reads at a glance without being read at all.
  warnAt: { type: Number, default: 75 },
  badAt: { type: Number, default: 92 },

  // Which direction is bad. "capacity" measures something you run out of, so a
  // full ring is the warning. "health" measures something you want high — the
  // share of customers still working — where a full ring is the good outcome
  // and an empty one is the alarm. On a red-accented panel this distinction
  // matters: without it, a perfectly healthy server shows a red ring at 100%.
  tone: { type: String, default: 'capacity' },
})

const clamped = computed(() => Math.max(0, Math.min(100, props.percent || 0)))
const radius = computed(() => (props.size - props.thickness) / 2)
const circumference = computed(() => 2 * Math.PI * radius.value)
const dash = computed(() => (clamped.value / 100) * circumference.value)

const stroke = computed(() => {
  if (props.tone === 'health') {
    if (clamped.value >= 90) return 'var(--ok)'
    if (clamped.value >= 60) return 'var(--warn)'
    return 'var(--bad)'
  }
  if (clamped.value >= props.badAt) return 'var(--bad)'
  if (clamped.value >= props.warnAt) return 'var(--warn)'
  // A normal reading is drawn in a neutral, not in the brand red. On a
  // red-accented panel an accent-coloured ring reads as an alarm at every
  // level, which would leave nothing left to say when one is warranted.
  return 'var(--ink-2)'
})
</script>

<template>
  <div class="ring" :style="{ width: size + 'px', height: size + 'px' }">
    <svg :width="size" :height="size" aria-hidden="true">
      <circle
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        fill="none"
        stroke="var(--surface-3)"
        :stroke-width="thickness"
      />
      <!-- Below half a percent the rounded cap would draw a stray dot on an
           otherwise empty ring, which reads as a fault rather than as zero. -->
      <circle
        v-if="clamped >= 0.5"
        :cx="size / 2"
        :cy="size / 2"
        :r="radius"
        fill="none"
        :stroke="stroke"
        :stroke-width="thickness"
        stroke-linecap="round"
        :stroke-dasharray="`${dash} ${circumference}`"
        :transform="`rotate(-90 ${size / 2} ${size / 2})`"
      />
    </svg>
    <span class="pct num">{{ Math.round(clamped) }}<i>%</i></span>
  </div>
</template>

<style scoped>
.ring {
  position: relative;
  flex-shrink: 0;
  display: grid;
  place-items: center;
}
.ring svg {
  position: absolute;
  inset: 0;
}
.ring circle {
  transition: stroke-dasharray 0.4s ease, stroke 0.2s;
}
.pct {
  font-size: 12.5px;
  font-weight: 700;
  color: var(--ink);
  direction: ltr;
  line-height: 1;
}
.pct i {
  font-style: normal;
  font-size: 8.5px;
  color: var(--muted);
  margin-inline-start: 1px;
}
@media (prefers-reduced-motion: reduce) {
  .ring circle {
    transition: none;
  }
}
</style>
