<script setup>
// A date, picked from a calendar.
//
// Drawn in the calendar the panel is set to -- Gregorian, or the Persian
// (Jalali) one the settings page offers -- and stored as a plain Gregorian
// day, YYYY-MM-DD, which is what the server keeps whichever one the reader
// sees. The conversion is the browser's own: Intl knows the Persian
// calendar, leap years and all, so there is no arithmetic of ours here to
// be a day out.
//
// Opens on a layer of its own over the page rather than inside the dialog,
// which scrolls: a calendar clipped by the dialog it was opened from is one
// whose last week cannot be reached.
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { store, t } from '../lib/store.js'
import { date as formatDate } from '../lib/format.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '' },
  // The first day that may be chosen, YYYY-MM-DD. Days before it are shown
  // and cannot be picked: an account that ended yesterday is not a plan.
  min: { type: String, default: '' },
  invalid: { type: Boolean, default: false },
  id: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

const DAY = 86400e3

const persian = computed(() => store.panel?.datepicker === 'jalalian')
const fa = computed(() => store.locale === 'fa')
// Latin digits either way: the rest of the panel writes numbers so, and a
// calendar in one set of digits beside a form in another reads as two tools.
const tag = computed(() => `${fa.value ? 'fa-IR' : 'en-GB'}-u-ca-${persian.value ? 'persian' : 'gregory'}-nu-latn`)
// Saturday-first where the Persian week starts; Monday-first otherwise.
const weekStart = computed(() => (persian.value || fa.value ? 6 : 1))

// --- Days as UTC midnights, so no timezone or clock change moves one. -----
function toUTC(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || '')
  return m ? Date.UTC(+m[1], +m[2] - 1, +m[3]) : null
}
function toISO(ms) {
  return new Date(ms).toISOString().slice(0, 10)
}
function todayUTC() {
  const n = new Date()
  return Date.UTC(n.getFullYear(), n.getMonth(), n.getDate())
}

// The calendar's own year, month and day for a UTC midnight.
const numbers = computed(
  () => new Intl.DateTimeFormat(`en-u-ca-${persian.value ? 'persian' : 'gregory'}-nu-latn`,
    { year: 'numeric', month: 'numeric', day: 'numeric', timeZone: 'UTC' }),
)
function ymd(ms) {
  const out = {}
  for (const p of numbers.value.formatToParts(new Date(ms))) {
    if (p.type === 'year' || p.type === 'month' || p.type === 'day') out[p.type] = parseInt(p.value, 10)
  }
  return out
}

// The UTC midnight on which the calendar's month (y, m) begins. For the
// Gregorian calendar that is arithmetic; for the Persian one it is found by
// starting near it and walking to it, asking the browser what each day is.
function monthStart(y, m) {
  if (!persian.value) return Date.UTC(y, m - 1, 1)
  // Farvardin 1 falls on 20 or 21 March; the first six months have 31 days
  // and the next five 30. Close enough to start from, never more than a few
  // days out.
  let at = Date.UTC(y + 621, 2, 21) + (m <= 7 ? (m - 1) * 31 : 186 + (m - 7) * 30) * DAY
  for (let i = 0; i < 400; i++) {
    const d = ymd(at)
    if (d.year === y && d.month === m) return at - (d.day - 1) * DAY
    const ahead = d.year > y || (d.year === y && d.month > m)
    at += ahead ? -DAY : DAY
  }
  return at
}
function nextMonth(y, m) {
  return m === 12 ? { y: y + 1, m: 1 } : { y, m: m + 1 }
}
function prevMonth(y, m) {
  return m === 1 ? { y: y - 1, m: 12 } : { y, m: m - 1 }
}

// --- What is on screen. ---------------------------------------------------
const open = ref(false)
const view = ref({ y: 0, m: 0 })
const field = ref(null)
const panel = ref(null)
const place = ref({ top: 0, left: 0, width: 280, above: false })

function showMonthOf(ms) {
  const d = ymd(ms)
  view.value = { y: d.year, m: d.month }
}

const title = computed(() => {
  if (!view.value.y) return ''
  const at = monthStart(view.value.y, view.value.m)
  return new Intl.DateTimeFormat(tag.value, { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(new Date(at))
})

const weekdays = computed(() => {
  const fmt = new Intl.DateTimeFormat(tag.value, { weekday: 'narrow', timeZone: 'UTC' })
  // 4 January 1970 was a Sunday.
  const sunday = Date.UTC(1970, 0, 4)
  return Array.from({ length: 7 }, (_, i) => fmt.format(new Date(sunday + ((weekStart.value + i) % 7) * DAY)))
})

const cells = computed(() => {
  if (!view.value.y) return []
  const first = monthStart(view.value.y, view.value.m)
  const n = nextMonth(view.value.y, view.value.m)
  const days = Math.round((monthStart(n.y, n.m) - first) / DAY)
  const lead = (new Date(first).getUTCDay() - weekStart.value + 7) % 7
  const today = todayUTC()
  const chosen = toUTC(props.modelValue)
  const floor = toUTC(props.min)
  const out = []
  for (let i = 0; i < lead; i++) out.push({ key: `pad-${i}`, pad: true })
  for (let d = 0; d < days; d++) {
    const ms = first + d * DAY
    out.push({
      key: ms,
      ms,
      day: d + 1,
      today: ms === today,
      chosen: ms === chosen,
      off: floor != null && ms < floor,
    })
  }
  return out
})

// --- Opening, placing, closing. -------------------------------------------
function placePanel() {
  const r = field.value?.getBoundingClientRect()
  if (!r) return
  const width = Math.min(296, window.innerWidth - 16)
  const height = panel.value?.offsetHeight || 340
  const below = window.innerHeight - r.bottom
  const above = below < height + 8 && r.top > below
  let left = fa.value ? r.right - width : r.left
  left = Math.max(8, Math.min(left, window.innerWidth - width - 8))
  place.value = { top: above ? Math.max(8, r.top - height - 4) : r.bottom + 4, left, width, above }
}

function show() {
  showMonthOf(toUTC(props.modelValue) ?? Math.max(todayUTC(), toUTC(props.min) ?? 0))
  open.value = true
  nextTick(() => {
    placePanel()
    panel.value?.querySelector('.dp-day.chosen, .dp-day.today, .dp-day:not(:disabled)')?.focus()
  })
}
function hide() {
  open.value = false
}
function toggle() {
  if (open.value) hide()
  else show()
}

function pick(cell) {
  if (cell.pad || cell.off) return
  emit('update:modelValue', toISO(cell.ms))
  hide()
  field.value?.querySelector('button.dp-trigger')?.focus()
}
function clear() {
  emit('update:modelValue', '')
  hide()
}
// A month, three or a year from today -- or from the date already chosen,
// when that is later: extending an account is the common case.
function addMonths(n) {
  const from = Math.max(todayUTC(), toUTC(props.modelValue) ?? 0)
  const d = new Date(from)
  d.setUTCMonth(d.getUTCMonth() + n)
  emit('update:modelValue', toISO(d.getTime()))
  hide()
}

function move(delta) {
  view.value = delta < 0 ? prevMonth(view.value.y, view.value.m) : nextMonth(view.value.y, view.value.m)
  nextTick(placePanel)
}

function onOutside(e) {
  if (!open.value) return
  if (field.value?.contains(e.target) || panel.value?.contains(e.target)) return
  hide()
}
function onKey(e) {
  if (open.value && e.key === 'Escape') {
    e.stopPropagation()
    hide()
  }
}
watch(open, (on) => {
  const act = on ? 'addEventListener' : 'removeEventListener'
  document[act]('mousedown', onOutside, true)
  document[act]('touchstart', onOutside, true)
  document[act]('keydown', onKey, true)
  window[act]('resize', placePanel)
  window[act]('scroll', placePanel, true)
})
onBeforeUnmount(() => {
  open.value = false
})

const shown = computed(() => (props.modelValue ? formatDate(`${props.modelValue}T12:00:00`, store.locale) : ''))
</script>

<template>
  <div ref="field" class="dp-field">
    <div class="ainput block dp-input" :class="{ invalid, focused: open }">
      <button
        :id="id || undefined"
        type="button"
        class="dp-trigger"
        :aria-expanded="open"
        aria-haspopup="dialog"
        @click="toggle"
      >
        <span v-if="shown" class="dp-value">{{ shown }}</span>
        <span v-else class="dp-placeholder">{{ placeholder }}</span>
      </button>
      <button v-if="modelValue" type="button" class="dp-clear" :aria-label="t('action.clear')" :title="t('action.clear')" @click="clear">
        <AntIcon name="CloseCircleFilled" />
      </button>
      <span class="dp-icon" @click="toggle"><AntIcon name="CalendarOutlined" /></span>
    </div>

    <Teleport to="body">
      <div
        v-if="open"
        ref="panel"
        class="dp-panel"
        role="dialog"
        :aria-label="title"
        :dir="fa ? 'rtl' : 'ltr'"
        :style="{ top: place.top + 'px', left: place.left + 'px', width: place.width + 'px' }"
      >
        <div class="dp-head">
          <button type="button" class="dp-nav prev" :aria-label="t('action.prev')" @click="move(-1)"><AntIcon name="LeftOutlined" /></button>
          <span class="dp-title">{{ title }}</span>
          <button type="button" class="dp-nav next" :aria-label="t('action.next')" @click="move(1)"><AntIcon name="RightOutlined" /></button>
        </div>
        <div class="dp-grid dp-week">
          <span v-for="(w, i) in weekdays" :key="i">{{ w }}</span>
        </div>
        <div class="dp-grid">
          <template v-for="c in cells" :key="c.key">
            <span v-if="c.pad"></span>
            <button
              v-else
              type="button"
              class="dp-day"
              :class="{ today: c.today, chosen: c.chosen }"
              :disabled="c.off"
              :aria-pressed="c.chosen"
              @click="pick(c)"
            >{{ c.day }}</button>
          </template>
        </div>
        <div class="dp-foot">
          <button type="button" class="abtn small" @click="addMonths(1)">{{ t('date.plusMonth') }}</button>
          <button type="button" class="abtn small" @click="addMonths(3)">{{ t('date.plusMonths', { n: 3 }) }}</button>
          <button type="button" class="abtn small" @click="addMonths(12)">{{ t('date.plusYear') }}</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.dp-field { position: relative; }
.dp-input { display: flex; align-items: center; gap: 4px; cursor: pointer; padding-inline-end: 8px; }
.dp-input.focused { border-color: var(--accent); }
.dp-input.invalid { border-color: var(--bad); }
.dp-trigger {
  flex: 1 1 auto;
  min-width: 0;
  height: 30px;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: start;
  cursor: pointer;
}
.dp-trigger:focus-visible { outline: none; }
.dp-value { color: var(--ink); }
.dp-placeholder { color: var(--faint); }
.dp-clear {
  display: inline-flex;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--faint);
  cursor: pointer;
}
.dp-clear:hover { color: var(--muted); }
.dp-icon { display: inline-flex; color: var(--faint); }
</style>

<style>
/* Teleported to the body, so these are not scoped to the field. */
.dp-panel {
  position: fixed;
  z-index: 1400;
  padding: 8px;
  border-radius: 8px;
  background: var(--surface);
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.12), 0 3px 6px -4px rgba(0, 0, 0, 0.12), 0 9px 28px 8px rgba(0, 0, 0, 0.05);
  border: 1px solid var(--border);
  color: var(--ink);
  font-size: 14px;
}
.dp-head { display: flex; align-items: center; justify-content: space-between; padding: 0 0 6px; }
.dp-title { font-weight: 600; }
.dp-nav {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.dp-nav:hover { background: var(--surface-2); color: var(--ink); }
/* The arrows point the way the months run, which is the other way in a
   right-to-left calendar. */
.dp-panel[dir='rtl'] .dp-nav .anticon { transform: scaleX(-1); }
.dp-grid { display: grid; grid-template-columns: repeat(7, 1fr); gap: 2px; }
.dp-week { margin-bottom: 2px; color: var(--faint); font-size: 12px; text-align: center; }
.dp-week span { line-height: 24px; }
.dp-day {
  height: 32px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--ink);
  font: inherit;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
}
.dp-day:hover:not(:disabled) { background: var(--surface-2); }
.dp-day:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.dp-day.today { border-color: var(--accent); }
.dp-day.chosen { background: var(--accent); border-color: var(--accent); color: #fff; }
.dp-day:disabled { color: var(--faint); opacity: 0.45; cursor: not-allowed; }
.dp-foot { display: flex; flex-wrap: wrap; gap: 6px; padding-top: 8px; margin-top: 6px; border-top: 1px solid var(--border); }
</style>
