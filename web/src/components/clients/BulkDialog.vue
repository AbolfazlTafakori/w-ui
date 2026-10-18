<script setup>
// Changing a selection of customers at once: putting them in a group, putting
// them on servers or taking them off, or setting their limit and renewal.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { unitToBytes } from '../../lib/format.js'
import { store, t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const props = defineProps({
  // group, attach, detach or adjust.
  kind: { type: String, required: true },
  // The chosen customers.
  ids: { type: Array, required: true },
  interfaces: { type: Array, default: () => [] },
  groupNames: { type: Array, default: () => [] },
})
// done: something changed, and the list should be read again.
const emit = defineEmits(['close', 'done'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const busy = ref(false)
// Started empty each time: a list of ticks left over from the last use would
// be a bulk action applied to servers nobody chose this time.
const form = ref({ group: '', quotaGB: '', quotaUnit: 'GB', resetCycle: '', interfaceIds: [] })

// Moving the customers onto a server, or off it. The answer names every
// customer it could not move and why -- a device limit, an address pool with
// nothing left -- because a count alone would leave an operator to work out
// which of three hundred people is still on the old server.
async function submitServers() {
  const chosen = form.value.interfaceIds.map(Number)
  if (!chosen.length) {
    notify(t('client.pickAServer'), 'error')
    return false
  }
  const res = await api.post(`/api/clients/servers/${props.kind === 'attach' ? 'attach' : 'detach'}`, {
    ids: props.ids,
    interfaceIds: chosen,
  })
  const failed = Object.entries(res?.failures || {})
  if (failed.length) notify(failed.map(([name, why]) => `${name}: ${why}`).join('\n'), 'error')
  else notify(`${t('client.bulkDone')} — ${nf(res?.changed || 0)}`, 'success')
  return true
}

async function submitDialog() {
  busy.value = true
  try {
    if (props.kind === 'attach' || props.kind === 'detach') {
      if (!(await submitServers())) return
    } else if (props.kind === 'group') {
      const res = await api.assignGroup(form.value.group.trim(), props.ids)
      notify(`${t('client.bulkDone')} — ${nf(res.affected)}`, 'success')
    } else if (props.kind === 'adjust') {
      const payload = { ids: props.ids }
      if (form.value.quotaGB !== '') payload.quotaBytes = unitToBytes(form.value.quotaGB, form.value.quotaUnit)
      if (form.value.resetCycle) payload.resetCycle = form.value.resetCycle
      const res = await api.adjustClients(payload)
      notify(`${t('client.bulkDone')} — ${nf(res.affected)}`, 'success')
    }
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
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="cd-title">
      <div class="card-head">
        <h2 id="cd-title">
          {{ kind === 'group' ? t('client.addToGroup')
            : kind === 'adjust' ? t('client.adjust')
            : kind === 'attach' ? t('client.attachServers')
            : t('client.detachServers') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="cd-form" class="card-body" @submit.prevent="submitDialog">
        <p class="target muted small">
          {{ t('action.selected') }}: <b>{{ nf(ids.length) }}</b>
        </p>

        <div v-if="kind === 'group'" class="field">
          <label for="cd-group">{{ t('client.group') }}</label>
          <input id="cd-group" v-model="form.group" list="cd-groups" :placeholder="t('client.groupPlaceholder')" autofocus />
          <datalist id="cd-groups">
            <option v-for="g in groupNames" :key="g" :value="g" />
          </datalist>
          <span class="hint">{{ t('client.groupHint') }}</span>
        </div>

        <!-- Adding keeps what a customer already has; only taking away
             removes anything. Said on the dialog, because "attach" and
             "detach" do not say it and the difference is the whole point of
             selling more than one server. -->
        <template v-else-if="kind === 'attach' || kind === 'detach'">
          <div class="field">
            <label>{{ kind === 'attach' ? t('client.attachServers') : t('client.detachServers') }}</label>
            <div class="srv-list">
              <label v-for="i in interfaces" :key="i.id" class="srv-item">
                <input v-model="form.interfaceIds" type="checkbox" :value="i.id" />
                <span class="srv-name">{{ i.name }}</span>
                <span class="srv-meta ltr">{{ i.protocol }} · {{ i.endpointHost }}</span>
              </label>
            </div>
            <span class="hint">
              {{ kind === 'attach' ? t('client.attachHint') : t('client.detachHint') }}
            </span>
          </div>
        </template>

        <!-- Setting a limit and the renewal. Adding time is Time's, which
             keeps unlimited customers unlimited and plans not started
             waiting. -->
        <template v-else-if="kind === 'adjust'">
          <div class="field">
            <label for="cd-quota">{{ t('client.quota') }}</label>
            <div class="unit-field">
              <input id="cd-quota" v-model="form.quotaGB" type="number" min="0" step="any" inputmode="decimal" :placeholder="t('client.leaveBlank')" autofocus />
              <select v-model="form.quotaUnit" class="unit-select" :aria-label="t('client.quotaUnit')">
                <option value="MB">MB</option><option value="GB">GB</option><option value="TB">TB</option>
              </select>
            </div>
          </div>
          <div class="field">
            <label for="cd-cycle">{{ t('client.resetCycle') }}</label>
            <select id="cd-cycle" v-model="form.resetCycle">
              <option value="">{{ t('client.leaveBlank') }}</option>
              <option value="none">{{ t('reset.none') }}</option>
              <option value="daily">{{ t('reset.daily') }}</option>
              <option value="weekly">{{ t('reset.weekly') }}</option>
              <option value="monthly">{{ t('reset.monthly') }}</option>
            </select>
          </div>
        </template>

      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="cd-form" class="btn primary" :disabled="busy">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.unit-select { flex: 0 0 auto; width: auto; min-width: 72px; padding-inline: 8px; }
</style>
