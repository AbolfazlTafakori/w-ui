<script setup>
// The question mark Ant puts after a form label that carries a tooltip: a
// 14px QuestionCircle in the faint colour, and on hover, focus or a tap a
// dark bubble above it -- rgba(0,0,0,.85), 6px radius, 6px 8px padding,
// 14px text on a 22px line, at most 250px wide, with a small arrow. It is
// positioned against the viewport, so a bubble near the edge of a phone
// screen is pushed back inside rather than cut off.
import { ref, nextTick, onBeforeUnmount } from 'vue'
import AntIcon from './AntIcon.vue'

defineProps({ text: { type: String, required: true } })

const open = ref(false)
const anchor = ref(null)
const bubble = ref(null)
const style = ref({})
const arrowStyle = ref({})
const placement = ref('top')

async function show() {
  open.value = true
  await nextTick()
  place()
}
function hide() {
  open.value = false
}
function toggle() {
  open.value ? hide() : show()
}
function place() {
  const a = anchor.value?.getBoundingClientRect()
  const b = bubble.value?.getBoundingClientRect()
  if (!a || !b) return
  const gap = 8
  const margin = 8
  const vw = window.innerWidth
  let top = a.top - b.height - gap
  placement.value = 'top'
  if (top < margin) {
    top = a.bottom + gap
    placement.value = 'bottom'
  }
  let left = a.left + a.width / 2 - b.width / 2
  left = Math.max(margin, Math.min(left, vw - b.width - margin))
  style.value = { top: `${top}px`, left: `${left}px` }
  // The arrow stays under the icon even when the bubble was pushed sideways.
  arrowStyle.value = { left: `${a.left + a.width / 2 - left}px` }
}
function onDoc(e) {
  if (open.value && anchor.value && !anchor.value.contains(e.target)) hide()
}
document.addEventListener('click', onDoc, true)
window.addEventListener('scroll', hide, true)
onBeforeUnmount(() => {
  document.removeEventListener('click', onDoc, true)
  window.removeEventListener('scroll', hide, true)
})
</script>

<template>
  <span
    ref="anchor"
    class="helptip"
    tabindex="0"
    role="button"
    :aria-label="text"
    @mouseenter="show"
    @mouseleave="hide"
    @focus="show"
    @blur="hide"
    @click.prevent.stop="toggle"
  >
    <AntIcon name="QuestionCircleOutlined" />
    <Teleport to="body">
      <div v-if="open" ref="bubble" class="helptip-bubble" :class="placement" :style="style" role="tooltip">
        {{ text }}
        <i class="helptip-arrow" :style="arrowStyle"></i>
      </div>
    </Teleport>
  </span>
</template>

<style>
.helptip {
  display: inline-flex;
  align-items: center;
  margin-inline-start: 4px;
  color: var(--faint);
  font-size: 14px;
  cursor: help;
  vertical-align: -1px;
  outline: none;
}
.helptip:hover, .helptip:focus-visible { color: var(--ink-2); }
.helptip-bubble {
  position: fixed;
  z-index: 1350;
  max-width: 250px;
  padding: 6px 8px;
  border-radius: 6px;
  background: rgba(0, 0, 0, 0.85);
  color: #fff;
  font-size: 14px;
  line-height: 22px;
  text-align: start;
  word-wrap: break-word;
  box-shadow: 0 6px 16px 0 rgba(0, 0, 0, 0.08), 0 3px 6px -4px rgba(0, 0, 0, 0.12), 0 9px 28px 8px rgba(0, 0, 0, 0.05);
  pointer-events: none;
}
.helptip-arrow {
  position: absolute;
  width: 8px;
  height: 8px;
  margin-left: -4px;
  background: rgba(0, 0, 0, 0.85);
  transform: rotate(45deg);
}
.helptip-bubble.top .helptip-arrow { bottom: -4px; }
.helptip-bubble.bottom .helptip-arrow { top: -4px; }
</style>
