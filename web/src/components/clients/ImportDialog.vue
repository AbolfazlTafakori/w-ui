<script setup>
// Bringing customers in from the list's own export: the file it downloads,
// or its text pasted, onto one tunnel here.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// Import: a JSON list in the shape the export writes -- the file it
// downloads, chosen here, or its text pasted.
const props = defineProps({ interfaces: { type: Array, default: () => [] } })
const emit = defineEmits(['close', 'imported'])
const importText = ref('')
const importFile = ref(null)
// The export names no tunnel -- one on the old server means nothing here --
// so the operator says which one they go on, and what to do about a name
// that is already taken. Skip is the default: it is the one that cannot
// overwrite a plan somebody is paying for.
const importTo = ref(props.interfaces[0]?.id || 0)
const importConflict = ref('skip')
const importBusy = ref(false)
function pickImportFile() {
  importFile.value?.click()
}
async function onImportFile(e) {
  const file = e.target.files?.[0]
  e.target.value = ''
  if (file) importText.value = await file.text()
}
async function runImport() {
  let parsed
  try {
    parsed = JSON.parse(importText.value)
  } catch {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  // The export's own file ({ exportedAt, count, clients }) or a bare list of
  // customers, alike.
  const clients = Array.isArray(parsed) ? parsed : parsed?.clients
  if (!Array.isArray(clients) || !clients.length) {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  importBusy.value = true
  try {
    const rep = await api.post('/api/clients/import', {
      clients,
      interfaceId: Number(importTo.value),
      onConflict: importConflict.value,
    })
    // Every outcome said, not only what was made: importing a list into the
    // panel it came from makes nothing and skips everyone, and "0" alone
    // read as an import that did not work.
    const summary = t('client.importDone', {
      created: nf(rep.created || 0),
      replaced: nf(rep.replaced || 0),
      skipped: nf(rep.skipped || 0),
      failed: nf(rep.failed || 0),
    })
    const problems = rep.problems || []
    notify(problems.length ? `${summary}\n${problems.join('\n')}` : summary, rep.failed ? 'error' : 'success')
    if (rep.created || rep.replaced) emit('imported')
    if (!rep.failed) emit('close')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    importBusy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ci-title">
      <div class="card-head">
        <h2 id="ci-title">{{ t('client.menu.import') }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field"><textarea v-model="importText" class="ltr mono" rows="12" spellcheck="false" placeholder="[ { ... } ]"></textarea></div>
        <input ref="importFile" type="file" accept=".json,application/json" hidden @change="onImportFile" />
        <div class="field">
          <label for="ci-to">{{ t('client.importTo') }}</label>
          <select id="ci-to" v-model="importTo">
            <option v-for="i in props.interfaces" :key="i.id" :value="i.id">{{ i.name }} · {{ i.protocol }}:{{ i.listenPort }}</option>
          </select>
        </div>
        <div class="field">
          <label for="ci-conflict">{{ t('client.importConflict') }}</label>
          <select id="ci-conflict" v-model="importConflict">
            <option value="skip">{{ t('client.importSkip') }}</option>
            <option value="rename">{{ t('client.importRename') }}</option>
            <option value="replace">{{ t('client.importReplace') }}</option>
          </select>
        </div>
        <p class="muted small">{{ t('client.importHint') }}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="pickImportFile"><Icon name="upload" :size="14" /><span>{{ t('action.chooseFile') }}</span></button>
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="importBusy || !importText.trim() || !importTo" @click="runImport">
          <span v-if="importBusy" class="spin"></span>
          <template v-else>{{ t('client.menu.import') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
