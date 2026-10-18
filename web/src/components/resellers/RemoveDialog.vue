<script setup>
// Removing one reseller or a selection. What becomes of their customers is
// asked rather than assumed: one answer quietly stops people paying for a
// service and the other quietly leaves the owner administering customers
// they did not know they had.
import { store, t } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'

// removing: { ids, name, customers }.
defineProps({
  removing: { type: Object, required: true },
  busy: { type: Boolean, default: false },
})
defineEmits(['close', 'remove'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
</script>

<template>
  <div class="amodal-backdrop centered" @click.self="$emit('close')">
    <div class="amodal" role="alertdialog" aria-modal="true" aria-labelledby="rm-title">
      <div class="amodal-head">
        <h2 id="rm-title" class="amodal-title">{{ t('admins.remove') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="$emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <p class="rm-text">{{ t(removing.ids.length > 1 ? 'admins.removeAskMany' : 'admins.removeAsk', { name: removing.name }) }}</p>
        <span class="atag red" style="margin: 0">{{ t('admins.holdsCustomers', { n: nf(removing.customers) }) }}</span>
      </div>
      <div class="amodal-foot">
        <button class="abtn" @click="$emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn" :disabled="busy" @click="$emit('remove', 'keep')">{{ t('admins.removeKeep') }}</button>
        <button class="abtn danger" :disabled="busy" @click="$emit('remove', 'delete')">{{ t('admins.removeWithClients') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rm-text { margin: 0 0 12px; color: var(--ink); line-height: 1.6; }
</style>
