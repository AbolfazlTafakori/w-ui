<script setup>
// An AmneziaWG tunnel's obfuscation profile, in two modes.
//
// Auto: the operator picks a strength -- AmneziaWG 2 (lighter, every app and a
// weak server) or AmneziaWG 3 (stronger: identifier ranges and a packet before
// each handshake, a capable server) -- and the panel fills every value, fitted
// to this server. Nothing is left blank, and a server too weak for 3 is drawn
// as 2 with a word to say so.
//
// Manual: every field is the operator's own to write, as they see fit. Nothing
// is drawn; the server still checks each value against AmneziaWG's rules and
// names the field it refuses.
//
// "2" is the classic level in the data, "3" is modern -- the two the panel has
// always drawn, now named for the app versions that read them.
import { computed, watch } from 'vue'
import { store, t } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import HelpTip from './HelpTip.vue'

const props = defineProps({
  modelValue: { type: Object, required: true },
  // 'classic' (AmneziaWG 2) or 'modern' (AmneziaWG 3).
  level: { type: String, default: 'modern' },
  // 'auto' (the panel draws) or 'manual' (the operator writes).
  mode: { type: String, default: 'auto' },
  drawing: { type: Boolean, default: false },
  // Set when 3 was asked for but the server could only take 2.
  downgraded: { type: Boolean, default: false },
  // An existing tunnel: a change means new files for its customers.
  editing: { type: Boolean, default: false },
  fieldName: { type: String, default: '' },
  fieldError: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue', 'update:level', 'update:mode', 'draw', 'clear'])

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

const auto = computed(() => props.mode === 'auto')
// Ranges and I1-I5 are AmneziaWG 3's; AmneziaWG 2 has neither. The strength is
// chosen in both modes, so this follows the level alone.
const strong = computed(() => props.level === 'modern')

function setMode(m) {
  if (m === props.mode) return
  emit('update:mode', m)
  // Entering Auto fills the fields for the strength on show; Manual keeps
  // whatever is there for the operator to edit.
  if (m === 'auto') emit('draw', props.level)
}
function pickLevel(lv) {
  if (lv === props.level && !props.downgraded) return
  emit('update:level', lv)
  if (auto.value) {
    emit('draw', lv)
    return
  }
  // Manual: choosing AmneziaWG 2 drops the fields it does not have, so what is
  // saved is a real AmneziaWG 2 profile and not a 3 with its extras hidden.
  if (lv === 'classic') {
    const cleared = { ...latest, h1Max: 0, h2Max: 0, h3Max: 0, h4Max: 0, i1: '', i2: '', i3: '', i4: '', i5: '' }
    latest = cleared
    emit('update:modelValue', cleared)
  }
}

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
      <button v-if="auto" type="button" class="abtn small awg-regen" :disabled="drawing" @click="emit('draw', level)">
        <AntIcon name="ReloadOutlined" :class="{ spin: drawing }" />{{ t('interface.awgRegen') }}
      </button>
    </div>

    <!-- Auto draws every value for this server; Manual is the operator's to
         write. The row of two is the panel's standard segmented control. -->
    <div class="aradio-group" role="radiogroup" :aria-label="t('interface.awgMode')">
      <button type="button" role="radio" class="aradio-btn" :class="{ active: mode === 'auto' }" :aria-checked="mode === 'auto'" :disabled="drawing" @click="setMode('auto')">{{ t('interface.awgMode.auto') }}</button>
      <button type="button" role="radio" class="aradio-btn" :class="{ active: mode === 'manual' }" :aria-checked="mode === 'manual'" :disabled="drawing" @click="setMode('manual')">{{ t('interface.awgMode.manual') }}</button>
    </div>

    <!-- The strength, in both modes: Auto draws it, Manual shows the fields it
         uses. Two plain selectable cards, selected softly rather than filled. -->
    <div class="awg-ver" role="radiogroup" :aria-label="t('interface.awgLevel')">
      <button type="button" role="radio" class="awg-ver-card" :class="{ selected: level === 'classic' }" :aria-checked="level === 'classic'" :disabled="drawing" @click="pickLevel('classic')">
        <span class="ver-name">{{ t('interface.awgLevel.classic') }}</span>
        <span class="ver-sub">{{ t('interface.awgLevelSub.classic') }}</span>
      </button>
      <button type="button" role="radio" class="awg-ver-card" :class="{ selected: level === 'modern' }" :aria-checked="level === 'modern'" :disabled="drawing" @click="pickLevel('modern')">
        <span class="ver-name">{{ t('interface.awgLevel.modern') }}</span>
        <span class="ver-sub">{{ t('interface.awgLevelSub.modern') }}</span>
      </button>
    </div>
    <div v-if="auto && downgraded" class="aalert warning slim"><AntIcon name="WarningOutlined" /><span>{{ t('interface.awgDowngraded') }}</span></div>
    <p class="awg-level-note">{{ auto ? t('interface.awgAutoNote') : t('interface.awgManualNote') }}</p>

    <div v-if="editing" class="aalert warning slim"><AntIcon name="WarningOutlined" /><span>{{ t('interface.awgEditWarn') }}</span></div>
    <p v-if="fieldName === 'awg'" class="field-error" role="alert">{{ fieldError }}</p>

    <fieldset :disabled="drawing || auto" class="awg-fields" :class="{ locked: auto && !drawing }">
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
            <template v-if="strong">
              <span class="awg-dash">–</span>
              <label class="ainput block"><input :aria-label="`H${n} ${t('interface.awgRangeMax')}`" class="ltr mono" type="number" min="0" :placeholder="t('interface.awgRangeMax')" :value="p[`h${n}Max`] || ''" @input="num(`h${n}Max`)($event)" /></label>
            </template>
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

    <!-- I1-I5: the packet before each handshake, the stronger profile's. Hidden
         for AmneziaWG 2, which has none. -->
    <fieldset v-if="strong" :disabled="drawing || auto" class="awg-specials" :class="{ locked: auto && !drawing }">
      <div class="awg-specials-head">I1 – I5 <HelpTip :text="t('interface.awgHint.i')" /></div>
      <div v-for="(k, i) in SPECIALS" :key="k" class="aform-item">
        <div class="awg-special">
          <span class="awg-i ltr">I{{ i + 1 }}</span>
          <label class="ainput block" :class="{ invalid: invalid(k) }">
            <input :data-field="`awg.${k}`" class="ltr mono" autocomplete="off" spellcheck="false" :placeholder="t('interface.awgOff')" :value="p[k] || ''" @input="str(k)($event)" />
          </label>
          <div v-if="!auto" class="aselect awg-preset">
            <select :aria-label="t('interface.awgPreset')" :value="presetOf(p[k])" @change="applyPreset(k, $event.target.value)">
              <option value="" disabled>{{ t('interface.awgPreset') }}</option>
              <option v-for="pr in presets" :key="pr.id" :value="pr.id">{{ t(`interface.awgPresetName.${pr.id}`) }}</option>
              <option v-if="presetOf(p[k]) === 'custom'" value="custom" disabled>{{ t('interface.awgPresetCustom') }}</option>
            </select>
          </div>
          <button v-if="!auto" type="button" class="abtn icon" :class="{ hide: !p[k] }" :disabled="!p[k]" :title="t('action.clear')" :aria-label="t('action.clear')" @click="set(k, '')"><AntIcon name="CloseOutlined" /></button>
        </div>
        <p v-if="invalid(k)" class="field-error" role="alert">{{ fieldError }}</p>
      </div>
      <div class="aalert info slim"><AntIcon name="InfoCircleOutlined" /><span>{{ t('interface.awgClientsNote') }}</span></div>
    </fieldset>
  </section>
</template>

<style scoped>
.awg-box { margin: 0 0 24px; padding: 16px; border: 1px solid var(--line); border-radius: 8px; background: var(--surface-2, transparent); display: grid; gap: 12px; }
.awg-head { display: flex; align-items: center; gap: 8px; font-weight: 600; }
.awg-regen { margin-inline-start: auto; }
.aradio-group { display: flex; }
.aradio-group .aradio-btn { flex: 1 1 0; }
/* Two plain selectable cards for the strength, selected with a soft accent
   wash and border rather than a loud fill. */
.awg-ver { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
.awg-ver-card {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px 12px;
  text-align: start;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface-2, transparent);
  color: var(--ink);
  cursor: pointer;
  transition: border-color 0.15s, background-color 0.15s;
}
.awg-ver-card:hover:not(:disabled) { border-color: var(--faint); }
.awg-ver-card.selected {
  border-color: var(--accent);
  background: var(--accent-soft, color-mix(in srgb, var(--accent) 12%, transparent));
}
.awg-ver-card.selected .ver-name { color: var(--accent-text); }
.awg-ver-card:disabled { cursor: default; }
.ver-name { font-weight: 600; font-size: 13px; }
.ver-sub { font-size: 11px; color: var(--muted); line-height: 1.5; }
.awg-level-note { margin: -4px 0 0; color: var(--muted); font-size: 12px; }
@media (max-width: 520px) { .awg-ver { grid-template-columns: 1fr; } }
.spin { animation: awg-spin 0.8s linear infinite; }
@keyframes awg-spin { to { transform: rotate(360deg); } }
.awg-fields { border: 0; margin: 0; padding: 0; min-width: 0; display: grid; gap: 12px; }
.awg-fields:disabled { opacity: 0.5; }
/* In Auto the values are the panel's, shown rather than edited: readable, not
   greyed like a disabled control mid-draw. */
.awg-fields.locked, .awg-specials.locked { opacity: 1; }
.awg-fields.locked .ainput, .awg-specials.locked .ainput { background: var(--surface-2, transparent); cursor: default; }
.awg-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); gap: 8px 12px; }
.awg-grid.headers { grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); }
.awg-grid .aform-item, .awg-specials .aform-item { margin: 0; }
.awg-range { display: flex; align-items: center; gap: 6px; }
.awg-dash { color: var(--muted); }
.awg-specials { border: 0; margin: 0; padding: 0; min-width: 0; display: grid; gap: 8px; }
.awg-specials:disabled { opacity: 1; }
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
