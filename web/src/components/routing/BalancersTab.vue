<script setup>
// Balancers: a tag rules can send to, spreading flows over several
// outbounds at random or to the fastest, each switched on or off, edited or
// deleted.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'
import Toggle from '../Toggle.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import BalancerForm from '../BalancerForm.vue'

defineProps({
  balancers: { type: Array, required: true },
  outbounds: { type: Array, default: () => [] },
})
const emit = defineEmits(['reload'])

const balancerFormFor = ref(null)
async function setBalancerEnabled(b, on) {
  const was = b.enabled
  b.enabled = on
  try {
    await api.patch(`/api/balancers/${b.id}`, { tag: b.tag, strategy: b.strategy, members: b.memberList, note: b.note, enabled: on })
  } catch (err) {
    b.enabled = was
    notify(err.message, 'error')
  }
}
function removeBalancer(b) {
  ask.value = {
    title: t('routing.removeBalancerTitle'),
    subject: b.tag,
    body: t('routing.removeBalancerBody'),
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.del(`/api/balancers/${b.id}`)
      notify(t('routing.balancerRemoved'), 'success')
      emit('reload')
    },
  }
}
const ask = ref(null)
const busy = ref(false)
async function runConfirmed() {
  if (!ask.value) return
  busy.value = true
  try {
    await ask.value.run()
    ask.value = null
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="toolbar-group mb-16">
    <button class="btn primary" @click="balancerFormFor = {}">
      <Icon name="plus" :size="14" />
      <span>{{ t('routing.tab.balancers') }}</span>
    </button>
  </div>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th class="w-act2">{{ t('table.actions') }}</th>
          <th class="w-sm">{{ t('table.enabled') }}</th>
          <th>{{ t('outbound.tag') }}</th>
          <th>{{ t('routing.balancer.strategy') }}</th>
          <th>{{ t('nav.outbounds') }}</th>
          <th>{{ t('routing.rule.note') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!balancers.length" class="empty-row">
          <td colspan="6">
            <div class="card-empty">
              <Icon name="swap" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="b in balancers" :key="b.id" :class="{ off: !b.enabled }">
          <td class="w-act2">
            <div class="action-buttons start">
              <button class="act round" :aria-label="t('action.edit')" :title="t('action.edit')" @click="balancerFormFor = { balancer: b }"><Icon name="edit" :size="13" /></button>
              <button class="act round" :aria-label="t('action.delete')" :title="t('action.delete')" @click="removeBalancer(b)"><Icon name="trash" :size="13" /></button>
            </div>
          </td>
          <td><Toggle :model-value="b.enabled" :label="b.tag" @update:model-value="(v) => setBalancerEnabled(b, v)" /></td>
          <td><span class="tag purple">{{ b.tag }}</span></td>
          <td>{{ b.strategy }}</td>
          <td>
            <div class="crit"><span v-for="m in b.memberList" :key="m" class="tag green">{{ m }}</span></div>
          </td>
          <td class="muted small">{{ b.note }}</td>
        </tr>
      </tbody>
    </table>
  </div>

  <BalancerForm
    v-if="balancerFormFor"
    :balancer="balancerFormFor.balancer"
    :outbounds="outbounds"
    @saved="((balancerFormFor = null), emit('reload'))"
    @cancel="balancerFormFor = null"
  />

  <ConfirmDialog
    :open="!!ask"
    :title="ask?.title || ''"
    :body="ask?.body || ''"
    :subject="ask?.subject || ''"
    :confirm-label="ask?.confirmLabel || ''"
    :busy="busy"
    @confirm="runConfirmed"
    @cancel="ask = null"
  />
</template>
