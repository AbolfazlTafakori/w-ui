<script setup>
// A text to read, copy and save: an export, a list of links. The text is the
// page's; this only shows it.
import { saveText } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

const props = defineProps({
  title: { type: String, required: true },
  text: { type: String, required: true },
  // The name it is saved under; without one there is nothing to save.
  file: { type: String, default: '' },
})
const emit = defineEmits(['close'])

function downloadText() {
  const json = props.file.endsWith('.json')
  saveText(props.text + '\n', props.file, json ? 'application/json' : 'text/plain;charset=utf-8')
}
async function copyText() {
  try {
    await navigator.clipboard.writeText(props.text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="tx-title">
      <div class="card-head">
        <h2 id="tx-title">{{ title }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body"><div class="field"><textarea class="ltr mono" rows="14" readonly spellcheck="false" :value="text"></textarea></div></div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button v-if="file" type="button" class="btn" @click="downloadText"><Icon name="download" :size="14" /><span>{{ t('action.download') }}</span></button>
        <button class="btn primary" @click="copyText"><Icon name="copy" :size="14" /><span>{{ t('action.copy') }}</span></button>
      </div>
    </div>
  </div>
</template>
