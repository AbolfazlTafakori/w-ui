<script setup>
// Attach Clients To… / Detach Clients: every customer on one tunnel put on,
// or taken off, another tunnel.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, tn, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const props = defineProps({
  iface: { type: Object, required: true },
  // attach or detach.
  kind: { type: String, required: true },
  interfaces: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'done'])
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const busy = ref(false)
const move = ref({ iface: props.iface, kind: props.kind, target: null })

async function submitMove() {
  const m = move.value
  if (!m.target) return
  busy.value = true
  try {
    const list = await api.allClients({ interfaceIds: m.iface.id })
    const res = await api.post(`/api/clients/servers/${m.kind}`, { ids: list.map((c) => c.id), interfaceIds: [m.target] })
    const failed = Object.entries(res?.failures || {})
    if (failed.length) notify(failed.map(([n, why]) => `${n}: ${why}`).join('\n'), 'error')
    else notify(`${m.kind === 'attach' ? t('iface.menu.attachTo') : t('iface.menu.detachClients')} — ${nf(res?.changed || 0)}`, 'success')
    emit('done')
    emit('close')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <!-- Attach Clients To… / Detach Clients: a target tunnel to pick. -->
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="mv-title">
      <div class="card-head">
        <h2 id="mv-title">{{ move.kind === 'attach' ? t('iface.menu.attachTo') : t('iface.menu.detachClients') }} — {{ move.iface.name }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field">
          <label for="mv-target">{{ t('iface.menu.targetInbound') }}</label>
          <select id="mv-target" v-model="move.target">
            <option :value="null" disabled>—</option>
            <option v-for="o in props.interfaces.filter((x) => x.id !== move.iface.id)" :key="o.id" :value="o.id">{{ o.name }} · {{ o.protocol }}:{{ o.listenPort }}</option>
          </select>
        </div>
        <p class="muted small">{{ tn('interface.nClients', move.iface.clients || 0) }}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !move.target" @click="submitMove">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ move.kind === 'attach' ? t('iface.menu.attach') : t('iface.menu.detach') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
