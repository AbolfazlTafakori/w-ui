<script setup>
// The question a page with an unsaved edit asks before it is left: stay,
// discard and leave, or save and leave. Escape and the backdrop stay.
import { onMounted, onUnmounted } from 'vue'
import { t } from '../lib/store.js'
import Icon from './Icon.vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['stay', 'discard', 'save'])

function onKey(e) {
  if (props.open && e.key === 'Escape') emit('stay')
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="emit('stay')">
    <div class="modal narrow confirm" role="alertdialog" aria-modal="true" aria-labelledby="leave-title">
      <div class="card-body confirm-body">
        <span class="confirm-mark">
          <Icon name="info" :size="20" />
        </span>
        <h2 id="leave-title">{{ t('settings.leaveTitle') }}</h2>
        <p class="confirm-text">{{ t('settings.leaveBody') }}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('stay')">{{ t('settings.stay') }}</button>
        <button type="button" class="btn danger" :disabled="busy" @click="emit('discard')">{{ t('settings.discardAndLeave') }}</button>
        <button type="button" class="btn primary" :disabled="busy" @click="emit('save')">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('settings.saveAndLeave') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
