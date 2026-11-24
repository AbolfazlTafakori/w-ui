<script setup>
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import Icon from './Icon.vue'
import { t } from '../lib/store.js'
import { searchOptions } from '../lib/searchOptions.js'

// A single choice from a list long enough to want a search: the control
// shows the one chosen, and opening it puts a search box above the list.
// MultiSelect's look, one value instead of many.

const props = defineProps({
  modelValue: { type: [Number, String], default: null },
  // [{ value, label, tags?: [{ text, kind? }], note? }]
  options: { type: Array, default: () => [] },
  placeholder: { type: String, default: '' },
  searchPlaceholder: { type: String, required: true },
  listHeight: { type: Number, default: 260 },
  id: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const query = ref('')
const active = ref(-1)
const root = ref(null)
const search = ref(null)
const list = ref(null)
const pop = ref(null)

// The list floats over the page, not inside the dialog: a dialog's body
// scrolls and clips, and a list opened in it was cut off and scrolled the
// dialog away from what it was for. Placed under the control, or over it
// where there is no room below, and kept there while the page moves.
const place = ref({})
function measure() {
  const el = root.value?.querySelector('.ss-control')
  if (!el) return
  const r = el.getBoundingClientRect()
  const want = props.listHeight + 48
  const below = window.innerHeight - r.bottom
  const up = below < want && r.top > below
  place.value = {
    left: r.left + 'px',
    width: r.width + 'px',
    ...(up ? { bottom: window.innerHeight - r.top + 4 + 'px' } : { top: r.bottom + 4 + 'px' }),
  }
}

const chosen = computed(() => props.options.find((o) => o.value === props.modelValue) || null)

const shown = computed(() => searchOptions(props.options, query.value))
watch(query, () => (active.value = shown.value.length ? 0 : -1))

async function openList() {
  if (open.value) return
  measure()
  open.value = true
  active.value = Math.max(0, shown.value.findIndex((o) => o.value === props.modelValue))
  await nextTick()
  search.value?.focus()
  scrollActive()
}

function close(refocus = false) {
  open.value = false
  query.value = ''
  active.value = -1
  if (refocus) root.value?.querySelector('.ss-control')?.focus()
}

function pick(value) {
  emit('update:modelValue', value)
  close(true)
}

function scrollActive() {
  nextTick(() => list.value?.querySelector('.ss-opt.active')?.scrollIntoView({ block: 'nearest' }))
}

function onKey(e) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    close(true)
    return
  }
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    const n = shown.value.length
    if (!n) return
    active.value = (active.value + (e.key === 'ArrowDown' ? 1 : -1) + n) % n
    scrollActive()
    return
  }
  if (e.key === 'Enter') {
    e.preventDefault()
    if (active.value >= 0 && shown.value[active.value]) pick(shown.value[active.value].value)
  }
}

function onControlKey(e) {
  if (['Enter', ' ', 'ArrowDown', 'ArrowUp'].includes(e.key)) {
    e.preventDefault()
    openList()
  }
}

function onOutside(e) {
  if (root.value?.contains(e.target) || pop.value?.contains(e.target)) return
  close()
}
function follow() {
  measure()
}
watch(open, (isOpen) => {
  const on = isOpen ? 'addEventListener' : 'removeEventListener'
  document[on]('mousedown', onOutside)
  window[on]('resize', follow)
  window[on]('scroll', follow, true)
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onOutside)
  window.removeEventListener('resize', follow)
  window.removeEventListener('scroll', follow, true)
})
</script>

<template>
  <div ref="root" class="ss" :class="{ open }">
    <button
      :id="id || undefined"
      type="button"
      class="ss-control"
      aria-haspopup="listbox"
      :aria-expanded="open"
      @click="open ? close() : openList()"
      @keydown="onControlKey"
    >
      <span v-if="chosen" class="ss-value">
        <span class="ss-label">{{ chosen.label }}</span>
        <span v-for="tag in chosen.tags || []" :key="tag.text" class="atag" :class="tag.kind">{{ tag.text }}</span>
      </span>
      <span v-else class="ss-placeholder">{{ placeholder }}</span>
      <Icon name="chevronDown" :size="15" class="ss-caret" />
    </button>

    <Teleport to="body">
    <div v-if="open" ref="pop" class="ss-pop" :style="place">
      <div class="ss-search">
        <Icon name="search" :size="14" />
        <input
          ref="search"
          v-model="query"
          :placeholder="searchPlaceholder"
          autocomplete="off"
          spellcheck="false"
          @keydown="onKey"
        />
      </div>
      <div ref="list" class="ss-list" :style="{ maxHeight: listHeight + 'px' }" role="listbox">
        <button
          v-for="(o, idx) in shown"
          :key="o.value"
          type="button"
          class="ss-opt"
          :class="{ on: o.value === modelValue, active: idx === active }"
          role="option"
          :aria-selected="o.value === modelValue"
          @mouseenter="active = idx"
          @click="pick(o.value)"
        >
          <span class="ss-check"><Icon v-if="o.value === modelValue" name="check" :size="13" /></span>
          <span class="ss-label">{{ o.label }}</span>
          <span v-for="tag in o.tags || []" :key="tag.text" class="atag" :class="tag.kind">{{ tag.text }}</span>
          <span v-if="o.note" class="ss-note">{{ o.note }}</span>
        </button>
        <p v-if="!shown.length" class="ss-empty">{{ t('common.noResults') }}</p>
      </div>
    </div>
    </Teleport>
  </div>
</template>

<style scoped>
.ss {
  position: relative;
}
.ss-control {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 36px;
  padding: 4px 10px 4px 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: inherit;
  font: inherit;
  text-align: start;
  cursor: pointer;
  transition: border-color 0.12s, box-shadow 0.12s;
}
.ss-control:hover {
  border-color: var(--faint);
}
.ss-control:focus-visible,
.ss.open .ss-control {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.ss-value {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}
.ss-placeholder {
  flex: 1;
  color: var(--faint);
}
.ss-caret {
  flex: none;
  color: var(--faint);
  transition: transform 0.14s var(--ease);
}
.ss.open .ss-caret {
  transform: rotate(180deg);
}
.ss-pop {
  position: fixed;
  z-index: 1400; /* over the dialog it opens from, as DateField's panel */
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface);
  box-shadow: var(--elev-3);
  overflow: hidden;
}
.ss-search {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  border-bottom: 1px solid var(--line);
  color: var(--faint);
}
.ss-search input {
  flex: 1;
  min-width: 0;
  min-height: 38px;
  padding: 0;
  border: 0;
  background: none;
  font-size: var(--t-base);
}
.ss-search input:focus {
  outline: none;
  box-shadow: none;
}
.ss-list {
  overflow-y: auto;
  padding: 4px;
}
.ss-opt {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px;
  border: 0;
  border-radius: 6px;
  background: none;
  color: inherit;
  font: inherit;
  text-align: start;
  cursor: pointer;
}
.ss-opt.active {
  background: var(--hover, rgba(127, 127, 127, 0.12));
}
.ss-opt.on {
  background: var(--accent-soft);
}
.ss-check {
  display: inline-flex;
  flex: none;
  width: 13px;
  color: var(--accent-text);
}
.ss-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 550;
}
.ss-opt .ss-label {
  flex: 1 1 auto;
}
.atag {
  flex: none;
  margin-inline-end: 0;
}
.ss-note {
  flex: none;
  font-size: var(--t-sm);
  color: var(--muted);
}
.ss-empty {
  margin: 0;
  padding: 12px 8px;
  color: var(--muted);
  font-size: var(--t-sm);
  text-align: center;
}
</style>
