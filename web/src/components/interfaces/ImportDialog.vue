<script setup>
// Making tunnels from an export: this panel's, or one written before the
// export took its present shape. Each is made on its own, so one refused --
// a name or a port taken here -- says why and leaves the rest to be made.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

const emit = defineEmits(['close', 'imported'])
const busy = ref(false)
const importText = ref('')
const importFile = ref(null)

// What the create call takes, out of an export: this panel's, or one written
// before the export took that shape, which carried the stored AmneziaWG and
// OpenVPN blocks whole. The server refuses a field it does not know rather
// than guess at it, so those never imported; the transport inside the OpenVPN
// block is the one part of them worth keeping.
const IMPORT_FIELDS = ['name', 'protocol', 'enabled', 'listenPort', 'subnet', 'endpointHost', 'mtu', 'dns', 'natInterface', 'mode', 'transport', 'psk', 'l2tpPlain', 'nodeId']
function importable(raw) {
  const out = {}
  for (const k of IMPORT_FIELDS) {
    if (raw[k] !== undefined && raw[k] !== null && raw[k] !== '') out[k] = raw[k]
  }
  if (!out.transport && raw.openvpn?.transport) out.transport = raw.openvpn.transport
  if (out.protocol !== 'openvpn') delete out.transport
  return out
}
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
  const list = (Array.isArray(parsed) ? parsed : [parsed]).filter((it) => it && typeof it === 'object' && !Array.isArray(it))
  if (!list.length) {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  busy.value = true
  // Each on its own: one tunnel refused -- a name or a port already taken
  // here -- says why and leaves the rest to be made.
  let made = 0
  const failed = []
  try {
    for (const it of list) {
      try {
        await api.post('/api/interfaces', importable(it))
        made++
      } catch (err) {
        failed.push(`${it.name || '?'}: ${err.message}`)
      }
    }
    if (made) notify(`${t('iface.menu.import')}: ${nf(made)}`, 'success')
    if (made) emit('imported')
    if (failed.length) notify(failed.join('\n'), 'error')
    else emit('close')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="im-title">
      <div class="card-head">
        <h2 id="im-title">{{ t('iface.menu.import') }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field"><textarea v-model="importText" class="ltr mono" rows="14" spellcheck="false" placeholder="{ ... }"></textarea></div>
        <p class="muted small">{{ t('iface.importHint') }}</p>
        <input ref="importFile" type="file" accept=".json,application/json,.txt" hidden @change="onImportFile" />
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="pickImportFile"><Icon name="upload" :size="14" /><span>{{ t('action.chooseFile') }}</span></button>
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !importText.trim()" @click="runImport">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('iface.menu.importBtn') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
