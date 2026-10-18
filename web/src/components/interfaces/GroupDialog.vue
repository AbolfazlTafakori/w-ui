<script setup>
// Add Clients To Group…: every customer on one tunnel gets the group, a name
// existing or new.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, tn, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const props = defineProps({
  iface: { type: Object, required: true },
  // The groups there are, offered as the name is typed.
  names: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'done'])
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const busy = ref(false)
const grouping = ref({ iface: props.iface, name: '', names: props.names })

async function submitGroup() {
  const g = grouping.value
  if (!g.name.trim()) return
  busy.value = true
  try {
    const list = await api.allClients({ interfaceIds: g.iface.id })
    await api.assignGroup(g.name.trim(), list.map((c) => c.id))
    notify(`${t('iface.menu.addToGroup')} — ${nf(list.length)}`, 'success')
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
  <!-- Add Clients To Group…: a name, existing or new. -->
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="gr-title">
      <div class="card-head">
        <h2 id="gr-title">{{ t('iface.menu.addToGroup') }} — {{ grouping.iface.name }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field">
          <label for="gr-name">{{ t('client.group') }}</label>
          <input id="gr-name" v-model="grouping.name" list="gr-names" autofocus />
          <datalist id="gr-names"><option v-for="n in grouping.names" :key="n" :value="n" /></datalist>
        </div>
        <p class="muted small">{{ tn('interface.nClients', grouping.iface.clients || 0) }}</p>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !grouping.name.trim()" @click="submitGroup">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
