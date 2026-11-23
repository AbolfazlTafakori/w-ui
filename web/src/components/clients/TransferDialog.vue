<script setup>
// Handing a selection of customers to another operator: all of them or none,
// with what it takes said before it is confirmed (OwnerPicker).
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, tn, notify } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'
import OwnerPicker from './OwnerPicker.vue'

const props = defineProps({
  ids: { type: Array, required: true },
})
const emit = defineEmits(['close', 'done'])

// Starts on the caller: the owner's own, or the admin's.
const choice = ref({ to: store.admin?.managesPanel ? 0 : store.admin?.id || 0, carryUsage: false })
const busy = ref(false)

async function submit() {
  busy.value = true
  try {
    const res = await api.post('/api/clients/transfer', { ids: props.ids, ...choice.value })
    notify(`${t('client.bulkDone')} — ${tn('owner.willMove', res.moved)}`, 'success')
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
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w520" role="dialog" aria-modal="true" aria-labelledby="td-title">
      <div class="amodal-head">
        <h2 id="td-title" class="amodal-title">{{ t('owner.title') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <p class="td-target">{{ t('action.selected') }}: <b>{{ tn('client.nCustomers', ids.length) }}</b></p>
        <OwnerPicker v-model="choice" :ids="ids" />
      </div>
      <div class="amodal-foot">
        <button class="abtn" type="button" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="button" :disabled="busy" @click="submit">
          <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
          <span>{{ t('owner.move') }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w520 { width: min(520px, calc(100vw - 32px)); }
.td-target { margin: 0 0 12px; font-size: 13px; color: var(--muted); }
.anticon.spin { animation: aspin 1s linear infinite; }
@keyframes aspin { to { transform: rotate(360deg); } }
</style>
