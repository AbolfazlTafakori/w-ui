<script setup>
// One L2TP sign-in, one device at a time: the customer's page shows the
// same list, and here an operator reads off what the customer on the phone
// has in front of them -- the device's own menu path and field names, each
// with the customer's own value. The device chosen is remembered, since an
// operator helping one router after another chooses it once.
import { computed, ref, watch } from 'vue'
import { store, t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  guides: { type: Array, required: true },
})

const KEY = 'wui.l2tpDevice'
function remembered() {
  try {
    return localStorage.getItem(KEY) || ''
  } catch {
    return ''
  }
}
function initial() {
  const r = remembered()
  if (props.guides.some((g) => g.id === r)) return r
  return (props.guides.find((g) => g.id === 'windows') || props.guides[0] || {}).id
}
const chosen = ref(initial())
watch(chosen, (v) => {
  try {
    localStorage.setItem(KEY, v)
  } catch {
    /* private browsing: it is simply not remembered */
  }
})

const current = computed(() => props.guides.find((g) => g.id === chosen.value))
// The guide carries both languages; the panel's own is chosen here.
const say = (text) => (text ? (store.locale === 'fa' && text.fa) || text.en : '')
const groups = computed(() =>
  ['device', 'router', 'plain', 'none']
    .map((key) => ({ key, items: props.guides.filter((g) => g.group === key) }))
    .filter((g) => g.items.length),
)

async function copy(v) {
  try {
    await navigator.clipboard.writeText(v)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
</script>

<template>
  <div class="l2tp-guide">
    <label class="guide-pick">
      <span>{{ t('l2tp.device') }}</span>
      <span class="aselect">
        <select v-model="chosen" dir="ltr" :aria-label="t('l2tp.device')">
          <optgroup v-for="g in groups" :key="g.key" :label="t(`l2tp.group.${g.key}`)">
            <option v-for="d in g.items" :key="d.id" :value="d.id">{{ say(d.name) }}</option>
          </optgroup>
        </select>
      </span>
    </label>
    <template v-if="current">
      <p v-if="current.group === 'plain'" class="guide-warn">{{ t('l2tp.plainWarn') }}</p>
      <div v-if="current.path" class="guide-path ltr">{{ current.path }}</div>
      <dl v-if="current.rows && current.rows.length" class="guide-fields">
        <div v-for="r in current.rows" :key="r.label" class="guide-row">
          <dt class="ltr">{{ r.label }}</dt>
          <dd>
            <span v-if="r.words">{{ say(r.words) }}</span>
            <code v-else class="ltr">{{ r.value }}</code>
            <button v-if="r.copy" type="button" class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(r.value)"><AntIcon name="CopyOutlined" /></button>
          </dd>
        </div>
      </dl>
      <p v-for="(n, i) in current.notes || []" :key="i" class="guide-note">{{ say(n) }}</p>
      <div v-if="current.command" class="guide-cmd">
        <code class="ltr">{{ current.command }}</code>
        <button type="button" class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(current.command)"><AntIcon name="CopyOutlined" /></button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.l2tp-guide {
  display: grid;
  gap: 8px;
}
.guide-pick {
  display: grid;
  gap: 4px;
  font-size: var(--t-xs, 12px);
  color: var(--muted);
}
.guide-pick select {
  width: 100%;
}
.guide-path {
  font-size: 13px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.guide-fields {
  display: grid;
  gap: 4px;
  margin: 0;
}
.guide-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 5px 10px;
  border-radius: 6px;
  background: var(--surface-2);
}
.guide-row dt {
  font-size: 12px;
  color: var(--muted);
}
.guide-row dd {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  margin: 0;
  font-size: 13px;
}
.guide-row code,
.guide-cmd code {
  font-family: var(--mono, ui-monospace, monospace);
  overflow-wrap: anywhere;
}
.guide-note {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}
.guide-warn {
  margin: 0;
  font-size: 12px;
  color: var(--warn, #d48806);
}
.guide-cmd {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--surface-2);
  font-size: 12px;
}
.guide-cmd code {
  flex: 1;
}
</style>
