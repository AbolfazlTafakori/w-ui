<script setup>
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import Icon from './Icon.vue'
import { t } from '../lib/store.js'

// A multiple-choice select, built to behave the way 3x-ui's does: chosen items
// sit in the control as removable tags, the ones that do not fit collapse into
// a count, typing filters the list, and the list opens upward so it does not
// fall off the bottom of a dialog it sits near the end of.
//
// It is a component rather than markup in one form because the same control
// belongs anywhere a customer is attached to several things at once.

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  // [{ value, label, tags?: [{ text, kind? }], note? }]
  options: { type: Array, default: () => [] },
  placeholder: { type: String, default: '' },
  listHeight: { type: Number, default: 220 },
  invalid: { type: Boolean, default: false },
})
const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const query = ref('')
const active = ref(-1)
const root = ref(null)
const search = ref(null)
const tagRow = ref(null)

const chosen = computed(() =>
  props.modelValue
    .map((v) => props.options.find((o) => o.value === v))
    .filter(Boolean),
)

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter((o) => String(o.label).toLowerCase().includes(q))
})

// How many tags fit on the control's single line. Ant Design calls this
// maxTagCount="responsive": rather than a fixed number, it shows what there is
// room for and turns the rest into "+2". Measured after each render, because
// the answer depends on the names and on how wide the dialog happens to be.
const visibleTags = ref(99)

async function measure() {
  visibleTags.value = 99
  await nextTick()
  const row = tagRow.value
  if (!row) return

  const limit = row.clientWidth - 44 // room for the "+N" chip
  let used = 0
  let fits = 0
  for (const el of row.querySelectorAll('.ms-tag')) {
    used += el.offsetWidth + 4
    if (used > limit) break
    fits++
  }
  // One tag always shows, even when it alone is too wide: a control that
  // collapses everything into "+1" tells the operator nothing.
  visibleTags.value = Math.max(1, fits)
}

// Watched as primitives, never as a fresh array literal. `watch(() => [a, b])`
// returns a new array on every evaluation, so with deep it fires on each
// re-render -- and this callback causes one, which is an infinite loop that
// locks the tab. It did.
watch(
  [
    () => props.modelValue.join('|'),
    () => props.options.map((o) => o.value).join('|'),
  ],
  measure,
  { immediate: true },
)

const hidden = computed(() => Math.max(0, chosen.value.length - visibleTags.value))

function toggle(value) {
  const next = new Set(props.modelValue)
  next.has(value) ? next.delete(value) : next.add(value)
  emit('update:modelValue', [...next])
}

function remove(value) {
  emit('update:modelValue', props.modelValue.filter((v) => v !== value))
}

async function openList() {
  if (open.value) return
  open.value = true
  active.value = -1
  await nextTick()
  search.value?.focus()
}

function close() {
  open.value = false
  query.value = ''
  active.value = -1
}

function onKey(e) {
  if (e.key === 'Escape') {
    close()
    return
  }
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    if (!open.value) return openList()
    const step = e.key === 'ArrowDown' ? 1 : -1
    const n = shown.value.length
    if (!n) return
    active.value = (active.value + step + n) % n
    return
  }
  if (e.key === 'Enter' && open.value && active.value >= 0) {
    e.preventDefault()
    toggle(shown.value[active.value].value)
    return
  }
  // Backspace on an empty query takes the last tag off, which is what every
  // control shaped like this does.
  if (e.key === 'Backspace' && !query.value && props.modelValue.length) {
    remove(props.modelValue[props.modelValue.length - 1])
  }
}

function onOutside(e) {
  if (root.value && !root.value.contains(e.target)) close()
}
watch(open, (isOpen) => {
  if (isOpen) document.addEventListener('mousedown', onOutside)
  else document.removeEventListener('mousedown', onOutside)
})
// The count also has to be recomputed when the control changes width, not only
// when the selection does. Without this the tags simply overflowed a narrower
// dialog: measured at 420px, four tags still shown and the row scrolling.
let ro = null
watch(tagRow, (el) => {
  ro?.disconnect()
  if (!el || typeof ResizeObserver === 'undefined') return
  let queued = false
  ro = new ResizeObserver(() => {
    // Coalesced to one measurement a frame: the observer fires again when the
    // measurement changes what is rendered, and measuring inside that would
    // be a loop with a reflow in it.
    if (queued) return
    queued = true
    requestAnimationFrame(() => {
      queued = false
      measure()
    })
  })
  ro.observe(el)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onOutside)
  ro?.disconnect()
})
</script>

<template>
  <div ref="root" class="ms" :class="{ open, invalid }">
    <div class="ms-control" @click="openList">
      <div ref="tagRow" class="ms-tags">
        <template v-if="chosen.length">
          <span
            v-for="(o, idx) in chosen"
            v-show="idx < visibleTags"
            :key="o.value"
            class="ms-tag"
          >
            {{ o.label }}
            <button
              type="button"
              class="ms-x"
              :aria-label="t('action.remove')"
              @click.stop="remove(o.value)"
            >
              <Icon name="close" :size="11" />
            </button>
          </span>
          <span v-if="hidden" class="ms-tag ms-more">+{{ hidden }}</span>
        </template>

        <input
          ref="search"
          v-model="query"
          class="ms-search"
          :placeholder="chosen.length ? '' : placeholder"
          :size="Math.max(1, query.length || (chosen.length ? 1 : placeholder.length))"
          autocomplete="off"
          spellcheck="false"
          @focus="openList"
          @keydown="onKey"
        />
      </div>
      <Icon name="chevronDown" :size="15" class="ms-caret" />
    </div>

    <!-- Upward, the way theirs is placed: this control sits near the bottom of
         a dialog, and a list dropping below it would open off the screen. -->
    <div v-if="open" class="ms-list" :style="{ maxHeight: listHeight + 'px' }" role="listbox">
      <button
        v-for="(o, idx) in shown"
        :key="o.value"
        type="button"
        class="ms-opt"
        :class="{ on: modelValue.includes(o.value), active: idx === active }"
        role="option"
        :aria-selected="modelValue.includes(o.value)"
        @mouseenter="active = idx"
        @click="toggle(o.value)"
      >
        <span class="ms-check"><Icon v-if="modelValue.includes(o.value)" name="check" :size="13" /></span>
        <span class="ms-label">{{ o.label }}</span>
        <span v-for="tag in o.tags || []" :key="tag.text" class="tag" :class="tag.kind">
          {{ tag.text }}
        </span>
        <span v-if="o.note" class="ms-note num ltr">{{ o.note }}</span>
      </button>

      <p v-if="!shown.length" class="ms-empty">{{ t('common.noResults') }}</p>
    </div>
  </div>
</template>

<style scoped>
.ms {
  position: relative;
}
.ms-control {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 38px;
  padding: 4px 8px 4px 10px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  cursor: text;
  transition: border-color 0.12s, box-shadow 0.12s;
}
.ms-control:hover {
  border-color: var(--faint);
}
.ms.open .ms-control {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ring);
}
.ms.invalid .ms-control {
  border-color: var(--danger, var(--accent));
}

/* One line. Tags past what fits are hidden and counted, rather than growing
   the control to three rows the moment somebody sells every tunnel. */
.ms-tags {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}
.ms-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: none;
  max-width: 100%;
  padding: 2px 4px 2px 8px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-3);
  font-size: var(--t-sm);
  white-space: nowrap;
}
.ms-more {
  padding-inline: 8px;
  color: var(--muted);
}
.ms-x {
  display: inline-flex;
  padding: 2px;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--faint);
  cursor: pointer;
}
.ms-x:hover {
  color: var(--ink);
  background: var(--hover, rgba(127, 127, 127, 0.14));
}
.ms-search {
  flex: 1;
  min-width: 40px;
  width: auto;
  min-height: 0;
  padding: 0;
  border: 0;
  background: none;
  font-size: var(--t-base);
}
.ms-search:focus {
  outline: none;
  box-shadow: none;
}
.ms-caret {
  flex: none;
  color: var(--faint);
  transition: transform 0.14s var(--ease);
}
.ms.open .ms-caret {
  transform: rotate(180deg);
}

.ms-list {
  position: absolute;
  z-index: 20;
  inset-inline: 0;
  bottom: calc(100% + 4px);
  overflow-y: auto;
  padding: 4px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface);
  box-shadow: var(--elev-3);
}
.ms-opt {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 7px 8px;
  border: 0;
  border-radius: 6px;
  background: none;
  color: inherit;
  font: inherit;
  text-align: start;
  cursor: pointer;
}
.ms-opt.active {
  background: var(--hover, rgba(127, 127, 127, 0.12));
}
.ms-opt.on {
  background: var(--accent-soft);
}
.ms-check {
  display: inline-flex;
  flex: none;
  width: 13px;
  color: var(--accent);
}
.ms-label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 550;
}
.ms-opt .tag {
  flex: none;
}
.ms-note {
  flex: none;
  font-size: var(--t-sm);
  color: var(--muted);
}
.ms-empty {
  margin: 0;
  padding: 12px 8px;
  color: var(--muted);
  font-size: var(--t-sm);
  text-align: center;
}
</style>
