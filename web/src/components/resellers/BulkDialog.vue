<script setup>
// The selection's actions that need a value: how many days to add, how much
// traffic, how many customers, which servers. Checked here, then handed up
// as the request to send.
import { computed, ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { unitToBytes } from '../../lib/format.js'
import AntIcon from '../AntIcon.vue'
import MultiSelect from '../MultiSelect.vue'

const props = defineProps({
  // extend, setQuota, setClientLimit, addServers or removeServers.
  kind: { type: String, required: true },
  count: { type: Number, required: true },
  servers: { type: Array, required: true },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['close', 'submit'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

const days = ref(30)
const quota = ref('')
const quotaUnit = ref('GB')
const limit = ref('')
const interfaceIds = ref([])
const error = ref('')

const serverOptions = computed(() =>
  props.servers.map((i) => ({
    value: i.id,
    label: i.name,
    tags: [{ text: t(`protocol.${i.protocol}`), kind: 'proto' }, ...(i.mode === 'amnezia' ? [{ text: 'AmneziaWG' }] : [])],
  })),
)

const title = computed(() => ({
  extend: t('admins.bulk.extend'),
  setQuota: t('admins.bulk.setQuota'),
  setClientLimit: t('admins.bulk.setLimit'),
  addServers: t('admins.bulk.addServers'),
  removeServers: t('admins.bulk.removeServers'),
})[props.kind] || '')

function submit() {
  error.value = ''
  const input = { action: props.kind }
  if (props.kind === 'extend') {
    const n = Number(days.value)
    if (!Number.isInteger(n) || n < 1 || n > 3650) {
      error.value = t('admins.daysRequired')
      return
    }
    input.days = n
  } else if (props.kind === 'setQuota') {
    input.quotaBytes = unitToBytes(quota.value, quotaUnit.value)
  } else if (props.kind === 'setClientLimit') {
    input.clientLimit = Math.max(0, Math.floor(Number(limit.value) || 0))
  } else if (props.kind === 'addServers' || props.kind === 'removeServers') {
    if (!interfaceIds.value.length) {
      error.value = t('client.pickAServer')
      return
    }
    input.interfaceIds = interfaceIds.value
  }
  emit('submit', input)
}
</script>

<template>
  <div class="amodal-backdrop centered" @click.self="$emit('close')">
    <div class="amodal" role="dialog" aria-modal="true" aria-labelledby="bd-title">
      <div class="amodal-head">
        <h2 id="bd-title" class="amodal-title">{{ title }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="$emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <form id="bulk-form" class="amodal-body" @submit.prevent="submit">
        <p class="bd-target">{{ t('admins.bulk.appliesTo', { n: nf(count) }) }}</p>

        <div v-if="kind === 'extend'" class="aform-item">
          <label class="aform-label" for="bd-days">{{ t('admins.bulk.days') }}</label>
          <label class="ainput number"><input id="bd-days" v-model="days" type="number" min="1" max="3650" step="1" class="ltr" autofocus /><span class="ainput-suffix">{{ t('unit.days') }}</span></label>
          <p class="hint">{{ t('admins.bulk.extendHint') }}</p>
        </div>

        <div v-else-if="kind === 'setQuota'" class="aform-item">
          <label class="aform-label" for="bd-quota">{{ t('admins.traffic') }}</label>
          <div class="acompact">
            <label class="ainput number"><input id="bd-quota" v-model="quota" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="t('client.unlimited')" autofocus /></label>
            <div class="aselect unit"><select v-model="quotaUnit" :aria-label="t('client.quotaUnit')"><option value="GB">GB</option><option value="TB">TB</option></select></div>
          </div>
          <p class="hint">{{ t('admins.bulk.emptyUnlimited') }}</p>
        </div>

        <div v-else-if="kind === 'setClientLimit'" class="aform-item">
          <label class="aform-label" for="bd-limit">{{ t('admins.clientLimit') }}</label>
          <label class="ainput number"><input id="bd-limit" v-model="limit" type="number" min="0" step="1" class="ltr" :placeholder="t('client.unlimited')" autofocus /></label>
          <p class="hint">{{ t('admins.bulk.emptyUnlimited') }}</p>
        </div>

        <div v-else class="aform-item">
          <label class="aform-label">{{ t('admins.servers') }}</label>
          <MultiSelect v-model="interfaceIds" :options="serverOptions" :placeholder="t('client.selectServers')" direction="down" />
          <p class="hint">{{ kind === 'addServers' ? t('admins.bulk.addServersHint') : t('admins.bulk.removeServersHint') }}</p>
        </div>

        <p v-if="error" class="field-error">{{ error }}</p>
      </form>
      <div class="amodal-foot">
        <button class="abtn" type="button" @click="$emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="submit" form="bulk-form" :disabled="busy">{{ t('action.save') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.bd-target { margin: 0 0 16px; color: var(--muted); }
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.field-error { margin: 8px 0 0; font-size: 12px; color: var(--bad); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.acompact .aselect.unit { flex: 0 0 auto; width: auto; min-width: 72px; }
</style>
