<script setup>
// Every chosen customer's subscription address, one per line, read for each
// of them -- the list's rows do not carry it. A customer whose link could not
// be read is named, so the lines still say who is who.
import { onMounted, ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

// Sub links: every selected customer's subscription address, one per line,
// read for each of them -- the list's rows do not carry it. A customer whose
// link could not be read is named, so the lines still say who is who.
const props = defineProps({
  // The customers, by id, in the order their lines are written.
  ids: { type: Array, required: true },
  // Their names, for a line whose link could not be read.
  names: { type: Object, default: () => ({}) },
})
const emit = defineEmits(['close'])
const subLinksText = ref('')
const subLinksBusy = ref(false)
onMounted(async () => {
  subLinksBusy.value = true
  try {
    const links = await api.subscriptionLinks(props.ids)
    subLinksText.value = props.ids.map((id, i) => links[i] || `${props.names[id] || `#${id}`}: —`).join('\n')
  } finally {
    subLinksBusy.value = false
  }
})
async function copySubLinks() {
  try {
    await navigator.clipboard.writeText(subLinksText.value)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="sl-title">
      <div class="card-head">
        <h2 id="sl-title">{{ t('client.menu.subLinks') }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field"><textarea class="ltr mono" rows="10" readonly spellcheck="false" :value="subLinksText" :placeholder="subLinksBusy ? '…' : ''"></textarea></div>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="subLinksBusy || !subLinksText" @click="copySubLinks">
          <span v-if="subLinksBusy" class="spin"></span>
          <template v-else><Icon name="copy" :size="14" /><span>{{ t('action.copy') }}</span></template>
        </button>
      </div>
    </div>
  </div>
</template>
