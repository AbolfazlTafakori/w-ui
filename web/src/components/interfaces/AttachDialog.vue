<script setup>
// Putting customers who are not on a tunnel yet onto it, picked from a list
// the page has read.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { bytes } from '../../lib/format.js'
import { store, t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const props = defineProps({
  iface: { type: Object, required: true },
  // The customers not on it yet.
  list: { type: Array, required: true },
})
const emit = defineEmits(['close', 'attached'])
const busy = ref(false)
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const attach = ref({ iface: props.iface, list: props.list, chosen: new Set(), q: '' })

async function submitAttach() {
  const a = attach.value
  if (!a.chosen.size) return
  busy.value = true
  try {
    const res = await api.post('/api/clients/servers/attach', { ids: [...a.chosen], interfaceIds: [a.iface.id] })
    const failed = Object.entries(res?.failures || {})
    if (failed.length) notify(failed.map(([n, why]) => `${n}: ${why}`).join('\n'), 'error')
    else notify(`${t('iface.menu.attachExisting')} — ${nf(res?.changed || 0)}`, 'success')
    emit('attached')
    emit('close')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}
function toggleAttach(id) {
  const next = new Set(attach.value.chosen)
  next.has(id) ? next.delete(id) : next.add(id)
  attach.value.chosen = next
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="at-title">
      <div class="card-head">
        <h2 id="at-title">{{ t('iface.menu.attachExisting') }} — {{ attach.iface.name }}</h2>
        <button class="act" :aria-label="t('common.close')" @click="emit('close')"><Icon name="close" :size="16" /></button>
      </div>
      <div class="card-body">
        <div class="field">
          <input v-model="attach.q" :placeholder="t('action.search')" />
        </div>
        <ul class="picklist">
          <li v-for="c in attach.list.filter((x) => !attach.q || x.name.toLowerCase().includes(attach.q.toLowerCase()))" :key="c.id">
            <label class="pick">
              <input type="checkbox" :checked="attach.chosen.has(c.id)" @change="toggleAttach(c.id)" />
              <span class="pick-name">{{ c.name }}</span>
              <span v-if="c.group" class="tag geekblue">{{ c.group }}</span>
              <span class="muted small ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
            </label>
          </li>
          <li v-if="!attach.list.length" class="muted small pick">{{ t('common.nothingYet') }}</li>
        </ul>
      </div>
      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('close')">{{ t('common.close') }}</button>
        <button class="btn primary" :disabled="busy || !attach.chosen.size" @click="submitAttach">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('iface.menu.attach') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
