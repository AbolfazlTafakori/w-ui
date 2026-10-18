<script setup>
// Import and export, the way theirs offers them: a paste box that adds each
// outbound in it, or the list as JSON to copy elsewhere.
import { computed, ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, tn, notify } from '../../lib/store.js'
import { exportText, parseImport } from '../../lib/outbounds/rows.js'
import Icon from '../Icon.vue'

const props = defineProps({
  // 'import' or 'export'.
  kind: { type: String, required: true },
  outbounds: { type: Array, required: true },
})
// imported(added): how many were added before it stopped, if it did.
const emit = defineEmits(['close', 'imported'])

const text = ref('')
const busy = ref(false)
const exported = computed(() => exportText(props.outbounds))

async function copyExport() {
  try {
    await navigator.clipboard.writeText(exported.value)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

async function runImport() {
  const list = parseImport(text.value)
  if (!list) {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  busy.value = true
  let added = 0
  try {
    for (const item of list) {
      await api.post('/api/outbounds', item)
      added++
    }
    notify(tn('outbound.imported', added), 'success')
    emit('imported', added)
    emit('close')
  } catch (err) {
    notify(err.message, 'error')
    if (added) emit('imported', added)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="$emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ob-io-title">
      <div class="card-head">
        <h2 id="ob-io-title">{{ kind === 'import' ? t('outbound.import') : t('outbound.export') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="$emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>
      <div class="card-body">
        <div class="field">
          <textarea v-if="kind === 'import'" v-model="text" class="ltr mono" rows="12" spellcheck="false" placeholder="[ { ... } ]"></textarea>
          <textarea v-else class="ltr mono" rows="12" readonly spellcheck="false" :value="exported"></textarea>
        </div>
      </div>
      <div class="modal-foot">
        <template v-if="kind === 'import'">
          <button type="button" class="btn ghost" @click="$emit('close')">{{ t('action.cancel') }}</button>
          <button class="btn primary" :disabled="busy || !text.trim()" @click="runImport">
            <span v-if="busy" class="spin"></span>
            <template v-else>{{ t('outbound.import') }}</template>
          </button>
        </template>
        <template v-else>
          <button type="button" class="btn ghost" @click="$emit('close')">{{ t('common.close') }}</button>
          <button class="btn primary" @click="copyExport">
            <Icon name="copy" :size="14" />
            <span>{{ t('action.copy') }}</span>
          </button>
        </template>
      </div>
    </div>
  </div>
</template>
