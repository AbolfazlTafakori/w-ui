<script setup>
// Ant's AutoComplete: a text box that offers what already exists. Focus it
// and the known values drop down; type and they narrow; pick one or keep
// typing a new one; the small cross clears. A group comes into being by
// being typed, so the box never refuses a value that is not on the list.
import { ref, computed, onBeforeUnmount } from 'vue'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  options: { type: Array, default: () => [] },
  placeholder: { type: String, default: '' },
  id: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const active = ref(-1)
const root = ref(null)

const matches = computed(() => {
  const q = (props.modelValue || '').trim().toLowerCase()
  const all = props.options.filter(Boolean)
  return q ? all.filter((o) => o.toLowerCase().includes(q)) : all
})

function set(v) {
  emit('update:modelValue', v)
}
function pick(v) {
  set(v)
  open.value = false
  active.value = -1
}
function onKey(e) {
  if (!open.value && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
    open.value = true
    return
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    active.value = Math.min(active.value + 1, matches.value.length - 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    active.value = Math.max(active.value - 1, 0)
  } else if (e.key === 'Enter' && open.value && active.value >= 0) {
    e.preventDefault()
    pick(matches.value[active.value])
  } else if (e.key === 'Escape') {
    open.value = false
  }
}
function onDoc(e) {
  if (root.value && !root.value.contains(e.target)) open.value = false
}
document.addEventListener('mousedown', onDoc)
onBeforeUnmount(() => document.removeEventListener('mousedown', onDoc))
</script>

<template>
  <div ref="root" class="acomplete">
    <label class="ainput block">
      <input
        :id="id"
        :value="modelValue"
        :placeholder="placeholder"
        autocomplete="off"
        role="combobox"
        :aria-expanded="open"
        @input="set($event.target.value); open = true; active = -1"
        @focus="open = true"
        @keydown="onKey"
      />
      <button v-if="modelValue" type="button" class="acomplete-clear" :aria-label="'×'" @mousedown.prevent @click="pick('')"><AntIcon name="CloseCircleFilled" /></button>
    </label>
    <div v-if="open && matches.length" class="acomplete-menu" role="listbox">
      <div
        v-for="(o, i) in matches"
        :key="o"
        class="acomplete-item"
        :class="{ active: i === active, chosen: o === modelValue }"
        role="option"
        :aria-selected="o === modelValue"
        @mousedown.prevent
        @mouseenter="active = i"
        @click="pick(o)"
      >{{ o }}</div>
    </div>
  </div>
</template>

<style>
.acomplete { position: relative; }
.acomplete-clear {
  display: inline-flex;
  margin-inline-start: 4px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--faint);
  font-size: 12px;
  cursor: pointer;
}
.acomplete-clear:hover { color: var(--ink-2); }
/* Ant's dropdown: 4px under the box, 4px padding, 8px radius, items on a
   32px line with the hovered one tinted and the chosen one in bold. */
.acomplete-menu {
  position: absolute;
  z-index: 30;
  top: calc(100% + 4px);
  inset-inline: 0;
  max-height: 256px;
  overflow-y: auto;
  padding: 4px;
  border-radius: 8px;
  background: var(--surface);
  box-shadow: 0 6px 16px 0 rgba(0, 0, 0, 0.08), 0 3px 6px -4px rgba(0, 0, 0, 0.12), 0 9px 28px 8px rgba(0, 0, 0, 0.05);
  border: 1px solid var(--line-soft);
}
.acomplete-item {
  padding: 5px 12px;
  border-radius: 4px;
  font-size: 14px;
  line-height: 22px;
  color: var(--ink);
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.acomplete-item.active { background: var(--surface-3); }
.acomplete-item.chosen { background: var(--accent-soft, var(--surface-3)); font-weight: 600; }
</style>
