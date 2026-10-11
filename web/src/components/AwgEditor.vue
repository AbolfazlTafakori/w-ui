<script setup>
// An AmneziaWG tunnel's obfuscation profile, every value of it editable.
// The server checks each against AmneziaWG's own rules and names the field
// it refuses; the form only shapes the input. Every change means every
// customer on the tunnel needs their file again, which is said once, above
// the fields, rather than after the fact.
import { computed, watch } from 'vue'
import { store, t } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import HelpTip from './HelpTip.vue'

const props = defineProps({
  modelValue: { type: Object, required: true },
  // modern (ranges and a packet before the handshake) or classic.
  level: { type: String, default: 'modern' },
  drawing: { type: Boolean, default: false },
  // An existing tunnel: a change means new files for its customers.
  editing: { type: Boolean, default: false },
  fieldName: { type: String, default: '' },
  fieldError: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue', 'update:level', 'draw', 'clear'])

const p = computed(() => props.modelValue)
// Each change is made to the latest value, not to the prop: two changes in
// one tick (a preset picked, a field typed) would otherwise each start from
// the same old copy, and the second would undo the first.
let latest = { ...props.modelValue }
watch(
  () => props.modelValue,
  (v) => {
    latest = { ...v }
  },
)
function set(key, value) {
  latest = { ...latest, [key]: value }
  emit('update:modelValue', latest)
  emit('clear', `awg.${key}`)
}
const num = (key) => (e) => {
  const v = e.target.value.trim()
  set(key, v === '' ? 0 : Number(v))
}
const str = (key) => (e) => set(key, e.target.value)

const NUMBERS = [
  { key: 'jc', label: 'Jc' },
  { key: 'jmin', label: 'Jmin' },
  { key: 'jmax', label: 'Jmax' },
  { key: 's1', label: 'S1' },
  { key: 's2', label: 'S2' },
  { key: 's3', label: 'S3' },
  { key: 's4', label: 'S4' },
]
const HEADERS = [1, 2, 3, 4]
const SPECIALS = ['i1', 'i2', 'i3', 'i4', 'i5']

const presets = computed(() => store.meta?.awgPresets || [])
function applyPreset(key, id) {
  const preset = presets.value.find((x) => x.id === id)
  if (preset) set(key, preset.value)
}
// Which ready packet a field holds: its id, "custom" for one typed by
// hand, nothing for an empty field.
function presetOf(v) {
  const s = (v || '').trim()
  if (!s) return ''
  return presets.value.find((x) => x.value === s)?.id || 'custom'
}
const invalid = (key) => props.fieldName === `awg.${key}`
</script>

<template>
  <section class="awg-box" aria-labelledby="awg-head">
    <div id="awg-head" class="awg-head">
      <AntIcon name="SafetyCertificateOutlined" />{{ t('interface.awgSection') }}
      <button type="button" class="abtn small awg-regen" :disabled="drawing" @click="emit('draw')">
        <AntIcon name="ReloadOutlined" :class="{ spin: drawing }" />{{ t('interface.awgRegen') }}
      </button>
    </div>
    <!-- The kind of profile the panel draws: what every app reads, or the
         stronger one the current apps read. -->
    <div class="aradio-group awg-level" role="radiogroup" :aria-label="t('interface.awgLevel')">
      <button v-for="lv in ['modern', 'classic']" :key="lv" type="button" role="radio" class="aradio-btn" :class="{ active: level === lv }" :aria-checked="level === lv" :disabled="drawing" @click="level !== lv && (emit('update:level', lv), emit('draw', lv))">
        {{ t(`interface.awgLevel.${lv}`) }}
      </button>
    </div>
    <p class="awg-level-note">{{ t(`interface.awgLevelNote.${level}`) }}</p>
    <div v-if="editing" class="aalert warning slim"><AntIcon name="WarningOutlined" /><span>{{ t('interface.awgEditWarn') }}</span></div>
    <p v-if="fieldName === 'awg'" class="field-error" role="alert">{{ fieldError }}</p>

    <fieldset :disabled="drawing" class="awg-fields">
      <div class="awg-grid">
        <div v-for="f in NUMBERS" :key="f.key" class="aform-item">
          <label class="aform-label" :for="`awg-${f.key}`">{{ f.label }} <HelpTip :text="t(`interface.awgHint.${f.key}`)" /></label>
          <label class="ainput block" :class="{ invalid: invalid(f.key) }">
            <input :id="`awg-${f.key}`" :data-field="`awg.${f.key}`" class="ltr mono" type="number" min="0" :value="p[f.key]" @input="num(f.key)($event)" />
          </label>
          <p v-if="invalid(f.key)" class="field-error" role="alert">{{ fieldError }}</p>
        </div>
      </div>

      <div class="awg-grid headers">
        <div v-for="n in HEADERS" :key="n" class="aform-item">
          <label class="aform-label" :for="`awg-h${n}`">H{{ n }} <HelpTip v-if="n === 1" :text="t('interface.awgHint.h')" /></label>
          <div class="awg-range" :class="{ invalid: invalid(`h${n}`) }">
            <label class="ainput block"><input :id="`awg-h${n}`" :data-field="`awg.h${n}`" class="ltr mono" type="number" min="5" :value="p[`h${n}`]" @input="num(`h${n}`)($event)" /></label>
            <span class="awg-dash">–</span>
            <label class="ainput block"><input :aria-label="`H${n} ${t('interface.awgRangeMax')}`" class="ltr mono" type="number" min="0" :placeholder="t('interface.awgRangeMax')" :value="p[`h${n}Max`] || ''" @input="num(`h${n}Max`)($event)" /></label>
          </div>
          <p v-if="invalid(`h${n}`)" class="field-error" role="alert">{{ fieldError }}</p>
        </div>
      </div>

      <div class="aform-item">
        <label class="aform-label" for="awg-hpk">HeaderProtectionKey <HelpTip :text="t('interface.awgHint.hpk')" /></label>
        <label class="ainput block" :class="{ invalid: invalid('headerProtectionKey') }">
          <input id="awg-hpk" data-field="awg.headerProtectionKey" class="ltr mono" autocomplete="off" spellcheck="false" :placeholder="t('interface.awgOff')" :value="p.headerProtectionKey || ''" @input="str('headerProtectionKey')($event)" />
        </label>
        <p v-if="invalid('headerProtectionKey')" class="field-error" role="alert">{{ fieldError }}</p>
      </div>
    </fieldset>

    <!-- I1-I5 are kept even when the numbers are drawn again. -->
    <div class="awg-specials">
      <div class="awg-specials-head">I1 – I5 <HelpTip :text="t('interface.awgHint.i')" /></div>
      <div v-for="(k, i) in SPECIALS" :key="k" class="aform-item">
        <div class="awg-special">
          <span class="awg-i ltr">I{{ i + 1 }}</span>
          <label class="ainput block" :class="{ invalid: invalid(k) }">
            <input :data-field="`awg.${k}`" class="ltr mono" autocomplete="off" spellcheck="false" :placeholder="t('interface.awgOff')" :value="p[k] || ''" @input="str(k)($event)" />
          </label>
          <div class="aselect awg-preset">
            <!-- Names the packet in the field: a ready one by its name, one
                 typed by hand as the operator's own. -->
            <select :aria-label="t('interface.awgPreset')" :value="presetOf(p[k])" @change="applyPreset(k, $event.target.value)">
              <option value="" disabled>{{ t('interface.awgPreset') }}</option>
              <option v-for="pr in presets" :key="pr.id" :value="pr.id">{{ t(`interface.awgPresetName.${pr.id}`) }}</option>
              <option v-if="presetOf(p[k]) === 'custom'" value="custom" disabled>{{ t('interface.awgPresetCustom') }}</option>
            </select>
          </div>
          <!-- Always there, so a row does not change width as it fills. -->
          <button type="button" class="abtn icon" :class="{ hide: !p[k] }" :disabled="!p[k]" :title="t('action.clear')" :aria-label="t('action.clear')" @click="set(k, '')"><AntIcon name="CloseOutlined" /></button>
        </div>
        <p v-if="invalid(k)" class="field-error" role="alert">{{ fieldError }}</p>
      </div>
      <div class="aalert info slim"><AntIcon name="InfoCircleOutlined" /><span>{{ t('interface.awgClientsNote') }}</span></div>
    </div>
  </section>
</template>

<style scoped>
.awg-box { margin: 0 0 24px; padding: 16px; border: 1px solid var(--line); border-radius: 8px; background: var(--surface-2, transparent); display: grid; gap: 12px; }
.awg-head { display: flex; align-items: center; gap: 8px; font-weight: 600; }
.awg-regen { margin-inline-start: auto; }
.awg-level { display: flex; }
.awg-level .aradio-btn { flex: 1 1 0; }
.awg-level-note { margin: -4px 0 0; color: var(--muted); font-size: 12px; }
.spin { animation: awg-spin 0.8s linear infinite; }
@keyframes awg-spin { to { transform: rotate(360deg); } }
.awg-fields { border: 0; margin: 0; padding: 0; min-width: 0; display: grid; gap: 12px; }
.awg-fields:disabled { opacity: 0.5; }
.awg-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); gap: 8px 12px; }
.awg-grid.headers { grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); }
.awg-grid .aform-item, .awg-specials .aform-item { margin: 0; }
.awg-range { display: flex; align-items: center; gap: 6px; }
.awg-dash { color: var(--muted); }
.awg-specials { display: grid; gap: 8px; }
.awg-specials-head { font-weight: 500; }
.awg-special { display: flex; align-items: center; gap: 8px; min-width: 0; }
.awg-special .ainput { flex: 1; min-width: 0; }
.awg-i { width: 22px; color: var(--muted); font-size: 13px; }
.awg-preset { flex: 0 0 auto; width: 150px; }
.abtn.hide { visibility: hidden; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
@media (max-width: 520px) {
  .awg-special { flex-wrap: wrap; }
  .awg-preset { width: 100%; order: 3; }
}
</style>
