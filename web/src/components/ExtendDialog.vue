<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, tn, notify, store } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Icon from './Icon.vue'

// Time or traffic for the selected customers, added or taken back.
//
// Three fields, as plans are sold: months, days and hours, or TB, GB and MB. A
// month is thirty days; sizes count in 1024s, as the panel shows them. What
// will happen is asked of the panel itself as the amount is typed -- a dry
// run over the same selection -- so the numbers shown are the ones that will
// be applied, customers without a limit and plans not started yet counted
// apart. Taking back asks for the count to be typed once it reaches five
// customers: it can end plans.
const props = defineProps({
  // 'time' or 'traffic'.
  kind: { type: String, required: true },
  ids: { type: Array, required: true },
})
const emit = defineEmits(['close', 'done'])

const subtract = ref(false)
const includeWaiting = ref(false)
const f = ref({ a: '', b: '', c: '' })
const typed = ref('')
const busy = ref(false)
const preview = ref(null)
const previewError = ref('')
const checking = ref(false)

const isTime = computed(() => props.kind === 'time')
const fields = computed(() =>
  isTime.value
    ? [
        { key: 'a', label: t('unit.months'), name: 'months' },
        { key: 'b', label: t('unit.days'), name: 'days' },
        { key: 'c', label: t('unit.hours'), name: 'hours' },
      ]
    : [
        { key: 'a', label: 'TB', name: 'tb' },
        { key: 'b', label: 'GB', name: 'gb' },
        { key: 'c', label: 'MB', name: 'mb' },
      ],
)

// A field left empty is nought; anything else has to be a number of zero or
// more, or the amount is not an amount.
function num(v) {
  if (v === '' || v == null) return 0
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 ? n : NaN
}
const values = computed(() => fields.value.map((fl) => num(f.value[fl.key])))
const valid = computed(() => values.value.every((v) => !Number.isNaN(v)) && values.value.some((v) => v > 0))

const input = computed(() => {
  const body = { ids: props.ids, kind: props.kind, subtract: subtract.value }
  fields.value.forEach((fl, i) => { body[fl.name] = values.value[i] })
  if (isTime.value) body.includeWaiting = includeWaiting.value
  return body
})

// The amount in words, from what the panel understood: "3 days 12 hours",
// "10.5 GB".
const amountText = computed(() => {
  const a = Math.abs(Number(preview.value?.amount || 0))
  if (!a) return ''
  if (!isTime.value) return bytes(a, store.locale)
  const hours = Math.round(a / 3600)
  const months = Math.floor(hours / (30 * 24))
  const days = Math.floor((hours % (30 * 24)) / 24)
  const h = hours % 24
  const parts = []
  if (months) parts.push(`${nf(months)} ${t('unit.months')}`)
  if (days) parts.push(`${nf(days)} ${t('unit.days')}`)
  if (h) parts.push(`${nf(h)} ${t('unit.hours')}`)
  return parts.join(' ') || `${nf(Math.round(a / 60))} min`
})
const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// The dry run, a moment after typing stops. The newest answer wins: one that
// arrives after a later request has been sent is dropped.
let timer = null
let seq = 0
function schedulePreview() {
  clearTimeout(timer)
  preview.value = null
  previewError.value = ''
  if (!valid.value) return
  timer = setTimeout(runPreview, 350)
}
async function runPreview() {
  const mine = ++seq
  checking.value = true
  try {
    const res = await api.extendClients({ ...input.value, dryRun: true })
    if (mine === seq) preview.value = res
  } catch (e) {
    if (mine === seq) previewError.value = e.message
  } finally {
    if (mine === seq) checking.value = false
  }
}
watch([f, subtract, includeWaiting], schedulePreview, { deep: true })
onBeforeUnmount(() => clearTimeout(timer))

const changed = computed(() => preview.value?.changed || 0)
const needsTyping = computed(() => subtract.value && changed.value >= 5)
const canApply = computed(
  () => valid.value && !busy.value && !checking.value && changed.value > 0 &&
    (!needsTyping.value || Number(typed.value) === changed.value),
)

const skippedLines = computed(() => {
  const s = preview.value?.skipped || {}
  return ['unlimited', 'waiting', 'tooShort', 'gone']
    .filter((k) => s[k])
    .map((k) => ({ key: k, text: tn(`xt.skip.${k}`, s[k]) }))
})
const effectLines = computed(() => {
  const p = preview.value
  if (!p) return []
  const out = []
  if (p.revived) out.push({ key: 'revived', tone: 'ok', text: tn('xt.revived', p.revived) })
  if (p.ended) out.push({ key: 'ended', tone: 'bad', text: tn('xt.ended', p.ended) })
  if (p.stillEnded) out.push({ key: 'stillEnded', tone: 'muted', text: tn('xt.stillEnded', p.stillEnded) })
  if (p.floored) out.push({ key: 'floored', tone: 'warn', text: tn('xt.floored', p.floored) })
  return out
})

async function apply() {
  if (!canApply.value) return
  busy.value = true
  try {
    const res = await api.extendClients(input.value)
    notify(
      t(subtract.value ? 'xt.doneTaken' : 'xt.doneAdded', { amount: amountText.value, n: res.changed }),
      'success',
    )
    emit('done', res)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="xt-title">
      <div class="card-head">
        <h2 id="xt-title">{{ isTime ? t('xt.titleTime') : t('xt.titleTraffic') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="xt-form" class="card-body" @submit.prevent="apply">
        <p class="target muted small">
          {{ t('action.selected') }}: <b>{{ nf(ids.length) }}</b>
        </p>

        <div class="aradio-group xt-dir" role="radiogroup" :aria-label="t('xt.direction')">
          <button type="button" class="aradio-btn" role="radio" :aria-checked="!subtract" :class="{ active: !subtract }" @click="subtract = false">
            {{ t('xt.add') }}
          </button>
          <button type="button" class="aradio-btn" role="radio" :aria-checked="subtract" :class="{ active: subtract }" @click="subtract = true">
            {{ t('xt.subtract') }}
          </button>
        </div>

        <div class="xt-grid">
          <div v-for="(fl, i) in fields" :key="fl.key" class="field">
            <label :for="`xt-${fl.key}`">{{ fl.label }}</label>
            <input
              :id="`xt-${fl.key}`"
              v-model="f[fl.key]"
              type="number"
              min="0"
              step="any"
              inputmode="decimal"
              placeholder="0"
              class="ltr"
              :aria-invalid="Number.isNaN(values[i])"
              :autofocus="i === 1"
            />
          </div>
        </div>
        <span class="hint">{{ isTime ? t('xt.timeHint') : t('xt.trafficHint') }}</span>

        <label v-if="isTime" class="xt-check">
          <input v-model="includeWaiting" type="checkbox" />
          <span>
            <b>{{ t('xt.includeWaiting') }}</b>
            <span class="hint">{{ t('xt.includeWaitingHint') }}</span>
          </span>
        </label>

        <!-- What will happen, as the panel works it out for this selection. -->
        <div class="xt-preview" role="status" aria-live="polite">
          <p v-if="!valid" class="muted small">{{ t('xt.enterAmount') }}</p>
          <p v-else-if="checking && !preview" class="muted small"><span class="spin sm"></span> {{ t('xt.checking') }}</p>
          <p v-else-if="previewError" class="xt-bad small">{{ previewError }}</p>
          <template v-else-if="preview">
            <p class="xt-main" :class="{ 'xt-bad': subtract }">
              {{ changed
                ? t(subtract ? 'xt.willLose' : changed === 1 ? 'xt.willGet.one' : 'xt.willGet', { n: changed, amount: amountText })
                : t('xt.noneChange') }}
            </p>
            <ul v-if="effectLines.length || skippedLines.length" class="xt-lines">
              <li v-for="l in effectLines" :key="l.key" :class="`xt-${l.tone}`">{{ l.text }}</li>
              <li v-for="l in skippedLines" :key="l.key" class="muted">{{ l.text }}</li>
            </ul>
          </template>
        </div>

        <div v-if="needsTyping" class="field">
          <label for="xt-typed">{{ t('xt.typeToConfirm', { n: changed }) }}</label>
          <input id="xt-typed" v-model="typed" type="text" inputmode="numeric" autocomplete="off" class="ltr" />
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="xt-form" class="btn" :class="subtract ? 'danger' : 'primary'" :disabled="!canApply">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ subtract ? t('xt.applySubtract') : t('xt.applyAdd') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.xt-dir {
  margin: 4px 0 14px;
}
.xt-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}
.xt-grid .field {
  margin-bottom: 0;
}
.xt-check {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid var(--line);
  border-radius: var(--r-sm);
  background: var(--surface-2);
  cursor: pointer;
}
.xt-check b {
  display: block;
  font-size: var(--t-sm);
  font-weight: 600;
  color: var(--ink);
}
.xt-check .hint {
  display: block;
  margin-top: 2px;
}
.xt-preview {
  margin-top: 14px;
  padding: 10px 12px;
  border-radius: var(--r-sm);
  background: var(--surface-2);
  min-height: 42px;
}
.xt-preview p {
  margin: 0;
}
.xt-main {
  font-size: var(--t-sm);
  font-weight: 600;
  color: var(--ink);
}
.xt-lines {
  margin: 6px 0 0;
  padding: 0;
  list-style: none;
  font-size: var(--t-xs);
  line-height: 1.6;
}
.xt-ok { color: var(--ok); }
.xt-bad { color: var(--bad); }
.xt-warn { color: var(--warn); }
.xt-muted { color: var(--muted); }
@media (max-width: 480px) {
  .xt-grid { gap: 6px; }
}
</style>
