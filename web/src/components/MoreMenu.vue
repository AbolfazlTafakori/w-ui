<script setup>
// A button and the menu it opens under itself: items with an icon,
// dividers, some dangerous, some off with a reason. Closed by a choice, a
// click anywhere else, Escape, or scrolling the page from under it.
import { onMounted, onUnmounted, ref } from 'vue'
import { t } from '../lib/store.js'
import AntIcon from './AntIcon.vue'

defineProps({
  // { key, label, icon, danger, disabled, title } or { divider: true }.
  items: { type: Array, required: true },
  // The button shows its icon alone, with the label for screen readers.
  compact: { type: Boolean, default: false },
  // The button's look, icon and words; "More" by default.
  buttonClass: { type: String, default: 'abtn more-btn' },
  icon: { type: String, default: 'MoreOutlined' },
  label: { type: String, default: '' },
})
const emit = defineEmits(['pick'])

const open = ref(null) // { rect, x, y }
const button = ref(null)
const menu = ref(null)

function toggle(e) {
  if (open.value) {
    open.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  open.value = { rect: r, x: r.left, y: r.bottom + 4 }
}
function pick(key) {
  open.value = null
  emit('pick', key)
}
function onDocClick(e) {
  if (open.value && !menu.value?.contains(e.target) && !button.value?.contains(e.target)) open.value = null
}
function onKey(e) {
  if (e.key === 'Escape') open.value = null
}
function onScroll(e) {
  if (open.value && !menu.value?.contains(e.target)) open.value = null
}
onMounted(() => {
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('scroll', onScroll, true)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', onScroll, true)
})
</script>

<template>
  <button ref="button" type="button" :class="buttonClass" :aria-label="compact ? (label || t('outbound.more')) : null" :title="compact ? (label || t('outbound.more')) : null" aria-haspopup="menu" :aria-expanded="!!open" @click="toggle">
    <AntIcon :name="icon" /><span v-if="!compact">{{ label || t('outbound.more') }}</span>
  </button>
  <Teleport to="body">
    <div v-if="open" ref="menu" v-fit="open.rect" class="amenu" role="menu" :style="{ top: open.y + 'px', left: open.x + 'px' }">
      <template v-for="(m, i) in items" :key="m.key || `d${i}`">
        <hr v-if="m.divider" class="amenu-divider" />
        <button v-else class="amenu-item" :class="{ danger: m.danger }" role="menuitem" :disabled="m.disabled" :aria-disabled="m.disabled ? 'true' : null" :title="m.title || null" @click="pick(m.key)">
          <AntIcon :name="m.icon" /><span>{{ m.label }}</span>
        </button>
      </template>
    </div>
  </Teleport>
</template>
