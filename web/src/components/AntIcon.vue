<script setup>
// One of Ant Design's icons, drawn the way Ant's own <Icon> draws it: a
// 1em square, filled with the current colour, sitting on the text baseline.
import { computed } from 'vue'
import { antIcons } from '../lib/anticons.js'

const props = defineProps({
  name: { type: String, required: true },
  size: { type: [Number, String], default: '1em' },
})
const icon = computed(() => antIcons[props.name])
// Unsized, it takes the font size of what it sits in, as Ant's does.
const px = computed(() => (typeof props.size === 'number' ? `${props.size}px` : props.size === '1em' ? null : props.size))
</script>

<template>
  <span class="anticon" :style="px ? { fontSize: px } : null" aria-hidden="true">
    <svg v-if="icon" :viewBox="icon.viewBox" width="1em" height="1em" fill="currentColor" focusable="false">
      <path v-for="(p, i) in icon.paths" :key="i" :d="p.d" :fill="p.fill" />
    </svg>
  </span>
</template>

<style>
.anticon {
  display: inline-flex;
  align-items: center;
  color: inherit;
  font-style: normal;
  line-height: 0;
  text-align: center;
  text-transform: none;
  vertical-align: -0.125em;
  text-rendering: optimizeLegibility;
}
.anticon > svg {
  display: inline-block;
  line-height: 1;
}
</style>
