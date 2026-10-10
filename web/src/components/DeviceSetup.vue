<script setup>
// One device at a time, in any protocol: the operator picks the device the
// customer has in front of them -- a phone, a computer, a router -- and then
// which of the customer's files to put on it, among those that device can
// dial; and reads off that device's own menu path and field names, each with
// the customer's own value. The device chosen is remembered, since an
// operator helping one router after another chooses it once.
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  // The customer's files: { key, label, guides } each, guides as the
  // profiles API hands them.
  files: { type: Array, required: true },
})

const devices = ref([])
onMounted(async () => {
  try {
    devices.value = await api.l2tpDevices()
  } catch {
    /* without the list there is nothing to choose from */
  }
})

const KEY = 'wui.device'
function remembered() {
  try {
    return localStorage.getItem(KEY) || localStorage.getItem('wui.l2tpDevice') || ''
  } catch {
    return ''
  }
}
const chosen = ref(remembered() || 'windows')
watch(chosen, (v) => {
  try {
    localStorage.setItem(KEY, v)
  } catch {
    /* private browsing: it is simply not remembered */
  }
})

const say = (text) => (text ? (store.locale === 'fa' && text.fa) || text.en : '')
const device = computed(() => devices.value.find((d) => d.id === chosen.value))
const groups = computed(() =>
  ['device', 'router', 'none']
    .map((key) => ({ key, items: devices.value.filter((d) => d.group === key) }))
    .filter((g) => g.items.length),
)

// Every file this device has a guide for, once per protocol: an L2TP file on
// a tunnel that also takes routers without IPsec is two choices.
const choices = computed(() => {
  const out = []
  for (const f of props.files) {
    for (const g of f.guides || []) {
      if (g.device !== chosen.value) continue
      out.push({ key: `${f.key}/${g.protocol}`, label: `${t(`setup.protocol.${g.protocol}`)} · ${f.label}`, guide: g, others: f.others || '' })
    }
  }
  return out
})
const pick = ref('')
watch(
  choices,
  (list) => {
    if (!list.some((c) => c.key === pick.value)) pick.value = list[0]?.key || ''
  },
  { immediate: true },
)
const current = computed(() => choices.value.find((c) => c.key === pick.value))

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
  <div class="device-setup">
    <div class="setup-picks">
      <label class="setup-pick">
        <span>{{ t('setup.device') }}</span>
        <span class="aselect">
          <select v-model="chosen" dir="ltr" :aria-label="t('setup.device')">
            <optgroup v-for="g in groups" :key="g.key" :label="t(`setup.group.${g.key}`)">
              <option v-for="d in g.items" :key="d.id" :value="d.id">{{ say(d.name) }}</option>
            </optgroup>
          </select>
        </span>
      </label>
      <label v-if="choices.length" class="setup-pick">
        <span>{{ t('setup.connection') }}</span>
        <span class="aselect">
          <select v-model="pick" :aria-label="t('setup.connection')">
            <option v-for="c in choices" :key="c.key" :value="c.key">{{ c.label }}</option>
          </select>
        </span>
      </label>
    </div>

    <template v-if="device">
      <p v-for="(n, i) in device.notes || []" :key="`dn-${i}`" class="setup-note">{{ say(n) }}</p>
      <p v-if="!choices.length && device.supports.length" class="setup-note">
        {{ t('setup.noneOfPlan') }} <span class="ltr">{{ device.supports.map((p) => t(`setup.protocol.${p}`)).join(' · ') }}</span>
      </p>
    </template>

    <template v-if="current">
      <p v-if="current.guide.weak" class="setup-warn">{{ t('setup.weak') }}</p>
      <p v-if="current.others" class="setup-note">{{ t('setup.others') }}: <span class="ltr">{{ current.others }}</span></p>
      <div v-if="current.guide.path" class="setup-path ltr">{{ current.guide.path }}</div>
      <dl v-if="current.guide.rows && current.guide.rows.length" class="setup-fields">
        <div v-for="r in current.guide.rows" :key="r.label" class="setup-row">
          <dt class="ltr">{{ r.label }}</dt>
          <dd>
            <span v-if="r.words">{{ say(r.words) }}</span>
            <code v-else class="ltr">{{ r.value }}</code>
            <button v-if="r.copy" type="button" class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(r.value)"><AntIcon name="CopyOutlined" /></button>
          </dd>
        </div>
      </dl>
      <p v-for="(n, i) in current.guide.notes || []" :key="`n-${i}`" class="setup-note">{{ say(n) }}</p>
      <div v-if="current.guide.command" class="setup-cmd">
        <code class="ltr">{{ current.guide.command }}</code>
        <button type="button" class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(current.guide.command)"><AntIcon name="CopyOutlined" /></button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.device-setup {
  display: grid;
  gap: 10px;
}
.setup-picks {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 10px;
}
.setup-pick {
  display: grid;
  gap: 4px;
  font-size: 12px;
  color: var(--muted);
}
.setup-pick select {
  width: 100%;
}
.setup-path {
  font-size: 13px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.setup-fields {
  display: grid;
  gap: 4px;
  margin: 0;
}
.setup-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 5px 10px;
  border-radius: 6px;
  background: var(--surface-2);
}
.setup-row dt {
  font-size: 12px;
  color: var(--muted);
}
.setup-row dd {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  margin: 0;
  font-size: 13px;
}
.setup-row code,
.setup-cmd code {
  font-family: var(--mono, ui-monospace, monospace);
  overflow-wrap: anywhere;
}
.setup-note {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}
.setup-warn {
  margin: 0;
  font-size: 12px;
  color: var(--tag-orange-ink, #d48806);
}
.setup-cmd {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--surface-2);
  font-size: 12px;
}
.setup-cmd code {
  flex: 1;
}
</style>
