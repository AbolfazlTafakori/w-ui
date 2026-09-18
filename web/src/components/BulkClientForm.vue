<script setup>
// The classic panel's Add Bulk dialog, in the client dialog's shape: the
// same wide modal, the same grid, the same labels with a question mark
// beside them. Its own rows are how many, how they are named -- a random
// handle, a prefix with a running number, or prefix, handle and postfix,
// with a live example of the first name -- and then the plan they all
// share, which is the client dialog's plan rows.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/store.js'
import { quotaToUnit, unitToBytes, unitToHours } from '../lib/format.js'
import AntIcon from './AntIcon.vue'
import Toggle from './Toggle.vue'
import MultiSelect from './MultiSelect.vue'
import HelpTip from './HelpTip.vue'
import AutoComplete from './AutoComplete.vue'

const props = defineProps({ interfaces: { type: Array, required: true } })
const emit = defineEmits(['close', 'submit'])

const form = ref({
  count: 10,
  method: 'prefixNumber',
  prefix: '',
  postfix: '',
  start: 1,
  interfaceIds: props.interfaces[0] ? [props.interfaces[0].id] : [],
  quota: '',
  quotaUnit: 'GB',
  expiresIn: '',
  expiresUnit: 'days',
  deviceLimit: 1,
  rateMbit: '',
  startOnFirstUse: false,
  resetCycle: 'none',
  group: '',
  enabled: true,
})
const busy = ref(false)
const fieldError = ref({})

// The batch starts from the defaults on the settings page, as one client does.
onMounted(async () => {
  try {
    const d = (await api.get('/api/settings')).settings
    if (d.defaultQuotaBytes) {
      const q = quotaToUnit(d.defaultQuotaBytes)
      form.value.quota = q.value
      form.value.quotaUnit = q.unit
    }
    if (d.defaultExpiryDays) form.value.expiresIn = d.defaultExpiryDays
    if (d.defaultDeviceLimit) form.value.deviceLimit = d.defaultDeviceLimit
    if (d.defaultRateBitsPerSec) form.value.rateMbit = d.defaultRateBitsPerSec / 1e6
    if (d.defaultResetCycle) form.value.resetCycle = d.defaultResetCycle
  } catch {
    /* the defaults are a convenience */
  }
})
const groupNames = ref([])
onMounted(async () => {
  try {
    groupNames.value = await api.groupNames()
  } catch {
    /* suggestions are optional */
  }
})

const serverOptions = computed(() =>
  props.interfaces.map((i) => ({
    value: i.id,
    label: i.name,
    tags: [{ text: t(`protocol.${i.protocol}`), kind: 'proto' }, ...(i.mode === 'amnezia' ? [{ text: 'AmneziaWG' }] : [])],
    note: i.capacity ? (i.capacity - i.allocated).toLocaleString() : '',
  })),
)
const chosen = computed(() => props.interfaces.filter((i) => form.value.interfaceIds.includes(i.id)))
// The tightest pool among the chosen servers, against how many are asked for.
const poolLeft = computed(() => {
  const left = chosen.value.filter((i) => i.capacity).map((i) => i.capacity - i.allocated)
  return left.length ? Math.min(...left) : null
})
const tooMany = computed(() => poolLeft.value !== null && Number(form.value.count) * Math.max(1, Number(form.value.deviceLimit) || 1) > poolLeft.value)

// What the first and last will be called, so the method is understood
// before two hundred are made.
const example = computed(() => {
  const p = form.value.prefix.trim()
  const q = form.value.postfix.trim()
  const n = Math.max(1, Number(form.value.count) || 1)
  const s = Math.max(1, Number(form.value.start) || 1)
  switch (form.value.method) {
    case 'random':
      return 'k3v9xq2mzp'
    case 'prefixRandom':
      return `${p}a8f2kx9q${q}`
    default:
      return n > 1 ? `${p}${s}${q} … ${p}${s + n - 1}${q}` : `${p}${s}${q}`
  }
})

function planDays() {
  const h = unitToHours(form.value.expiresIn, form.value.expiresUnit)
  return h > 0 ? Math.ceil(h / 24) : 0
}

function validate() {
  const e = {}
  const n = Number(form.value.count)
  if (!Number.isInteger(n) || n < 1 || n > 200) e.count = t('client.batchCountRange')
  if (form.value.method !== 'random' && !form.value.prefix.trim()) e.prefix = t('client.batchPrefixRequired')
  if (!form.value.interfaceIds.length) e.servers = t('client.selectServers')
  fieldError.value = e
  return !Object.keys(e).length
}

async function submit() {
  if (!validate()) return
  busy.value = true
  try {
    const hours = form.value.startOnFirstUse ? 0 : unitToHours(form.value.expiresIn, form.value.expiresUnit)
    const expiresAt = hours > 0 ? new Date(Date.now() + hours * 3600e3).toISOString() : null
    await emit('submit', {
      count: Number(form.value.count),
      method: form.value.method,
      prefix: form.value.prefix.trim(),
      postfix: form.value.postfix.trim(),
      start: Math.max(1, Number(form.value.start) || 1),
      interfaceIds: form.value.interfaceIds,
      quotaBytes: unitToBytes(form.value.quota, form.value.quotaUnit),
      expiresAt,
      deviceLimit: Number(form.value.deviceLimit) || 0,
      rateBitsPerSec: Math.max(0, Math.round(Number(form.value.rateMbit) * 1e6)) || 0,
      startOnFirstUse: form.value.startOnFirstUse,
      durationDays: form.value.startOnFirstUse ? planDays() : 0,
      resetCycle: form.value.resetCycle,
      group: form.value.group.trim(),
      enabled: form.value.enabled,
      deviceNames: [],
    })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w720" role="dialog" aria-modal="true" aria-labelledby="bf-title">
      <div class="amodal-head">
        <h2 id="bf-title" class="amodal-title">{{ t('client.batchAdd') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body cf-body">
        <form id="bulk-form" class="cf-form" @submit.prevent="submit">
          <div class="arow16">
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label required" for="bf-count">{{ t('client.batchCount') }} <HelpTip :text="t('client.batchCountHint')" /></label>
                <label class="ainput number" :class="{ invalid: fieldError.count }"><input id="bf-count" v-model="form.count" type="number" min="1" max="200" class="ltr" autofocus /></label>
                <p v-if="fieldError.count" class="field-error">{{ fieldError.count }}</p>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="bf-method">{{ t('client.batchMethod') }} <HelpTip :text="t('client.batchMethodHint')" /></label>
                <div class="aselect"><select id="bf-method" v-model="form.method">
                  <option value="random">{{ t('client.batchRandom') }}</option>
                  <option value="prefixNumber">{{ t('client.batchPrefixNumber') }}</option>
                  <option value="prefixRandom">{{ t('client.batchPrefixRandom') }}</option>
                </select></div>
              </div>
            </div>
            <div v-if="form.method === 'prefixNumber'" class="acol6">
              <div class="aform-item">
                <label class="aform-label" for="bf-start">{{ t('client.batchStart') }}</label>
                <label class="ainput number"><input id="bf-start" v-model="form.start" type="number" min="1" class="ltr" /></label>
              </div>
            </div>
          </div>

          <div v-if="form.method !== 'random'" class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="bf-prefix">{{ t('client.batchPrefix') }}</label>
                <label class="ainput block" :class="{ invalid: fieldError.prefix }"><input id="bf-prefix" v-model="form.prefix" maxlength="40" class="ltr" placeholder="shop-" /></label>
                <p v-if="fieldError.prefix" class="field-error">{{ fieldError.prefix }}</p>
              </div>
            </div>
            <div v-if="form.method === 'prefixRandom'" class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="bf-postfix">{{ t('client.batchPostfix') }}</label>
                <label class="ainput block"><input id="bf-postfix" v-model="form.postfix" maxlength="40" class="ltr" /></label>
              </div>
            </div>
          </div>
          <p class="hint example">{{ t('client.batchExample') }}: <code class="ltr">{{ example }}</code></p>

          <div class="aform-item">
            <label class="aform-label required">{{ t('client.chooseServers') }} <HelpTip :text="t('client.serversHint')" /></label>
            <MultiSelect v-model="form.interfaceIds" :options="serverOptions" :placeholder="t('client.selectServers')" :invalid="!!fieldError.servers" />
            <p v-if="fieldError.servers" class="field-error">{{ fieldError.servers }}</p>
            <p v-else-if="poolLeft !== null" class="hint" :class="{ warn: tooMany }">{{ t('interface.addressesLeft') }}: <span class="ltr">{{ poolLeft.toLocaleString() }}</span></p>
          </div>

          <div class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="bf-quota">{{ t('client.quota') }} <HelpTip :text="t('client.quotaHint')" /></label>
                <div class="acompact">
                  <label class="ainput number"><input id="bf-quota" v-model="form.quota" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="t('client.unlimited')" /></label>
                  <div class="aselect unit"><select v-model="form.quotaUnit" :aria-label="t('client.quotaUnit')"><option value="MB">MB</option><option value="GB">GB</option><option value="TB">TB</option></select></div>
                </div>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label" for="bf-devices">{{ t('client.deviceLimit') }} <HelpTip :text="t('client.deviceLimitHint')" /></label>
                <label class="ainput number"><input id="bf-devices" v-model="form.deviceLimit" type="number" min="0" max="50" class="ltr" :placeholder="t('client.unlimited')" /></label>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="bf-expires">{{ form.startOnFirstUse ? t('client.expireDays') : t('client.expiresIn') }} <HelpTip :text="form.startOnFirstUse ? t('client.durationHint') : t('client.expiresHint')" /></label>
                <div class="acompact">
                  <label class="ainput number"><input id="bf-expires" v-model="form.expiresIn" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="form.startOnFirstUse ? '30' : t('client.neverExpires')" /></label>
                  <div class="aselect unit"><select v-model="form.expiresUnit" :aria-label="t('client.expiresUnit')"><option value="hours">{{ t('unit.hours') }}</option><option value="days">{{ t('unit.days') }}</option><option value="months">{{ t('unit.months') }}</option></select></div>
                </div>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label">{{ t('client.delayedStart') }} <HelpTip :text="t('client.startOnFirstUseHint')" /></label>
                <div class="switch-line"><Toggle v-model="form.startOnFirstUse" :label="t('client.startOnFirstUse')" /></div>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label" for="bf-rate">{{ t('client.rate') }} <HelpTip :text="t('client.rateHint')" /></label>
                <label class="ainput number"><input id="bf-rate" v-model="form.rateMbit" type="number" min="0" step="1" class="ltr" placeholder="0" /><span class="ainput-suffix">Mbit/s</span></label>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label" for="bf-reset">{{ t('client.resetCycle') }}</label>
                <div class="aselect"><select id="bf-reset" v-model="form.resetCycle"><option value="none">{{ t('reset.none') }}</option><option value="daily">{{ t('reset.daily') }}</option><option value="weekly">{{ t('reset.weekly') }}</option><option value="monthly">{{ t('reset.monthly') }}</option></select></div>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="bf-group">{{ t('client.group') }} <HelpTip :text="t('client.groupHint')" /></label>
                <AutoComplete id="bf-group" v-model="form.group" :options="groupNames" :placeholder="t('client.groupPlaceholder')" maxlength="64" />
              </div>
            </div>
          </div>

          <div class="aform-item">
            <div class="switch-line"><Toggle v-model="form.enabled" :label="t('client.enabled')" /></div>
          </div>
        </form>
      </div>

      <div class="amodal-foot">
        <button type="button" class="abtn" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="bulk-form" class="abtn primary" :disabled="busy">
          <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
          <span>{{ t('client.batchCreate', { n: Number(form.count) || 0 }) }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w720 { width: min(720px, calc(100vw - 32px)); }
.cf-body { overflow-x: hidden; }
.cf-form { padding-top: 4px; }
.arow16 { display: grid; grid-template-columns: repeat(24, minmax(0, 1fr)); margin-inline: -8px; }
.arow16 > [class^='acol'] { padding-inline: 8px; min-width: 0; }
.acol12 { grid-column: span 12; }
.acol6 { grid-column: span 6; }
@media (max-width: 768px) {
  .acol12 { grid-column: 1 / -1; }
  .acol6 { grid-column: span 12; }
  .amodal.w720 { width: calc(100vw - 16px); padding: 16px; max-height: calc(100dvh - 32px); }
  .amodal-backdrop { padding: 16px 8px; align-items: flex-start; }
}
.aform-label.required::before { content: '*'; margin-inline-end: 4px; color: var(--bad); }
.ainput.invalid, .ainput.invalid:hover { border-color: var(--bad); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.acompact .aselect.unit { flex: 0 0 auto; width: auto; min-width: 84px; }
.switch-line { display: flex; align-items: center; min-height: 32px; }
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.hint.warn { color: var(--bad); }
.example { margin: -12px 0 20px; }
.example code { font-size: 13px; color: var(--ink); }
.field-error { margin: 4px 0 0; font-size: 12px; color: var(--bad); }
.spin { animation: aspin 1s linear infinite; }
</style>
