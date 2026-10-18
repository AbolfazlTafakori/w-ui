<script setup>
// One dialog serves create, rename, extend and set-quota; they differ only
// in the field they collect. It sends the request itself and says when it
// is done, so the page only has to reload.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, notify } from '../../lib/store.js'
import { gigabytesToBytes } from '../../lib/format.js'
import Icon from '../Icon.vue'

const props = defineProps({
  // 'create', 'rename', 'extend' or 'quota'.
  kind: { type: String, required: true },
  // The group acted on; none to create one.
  group: { type: Object, default: null },
})
const emit = defineEmits(['close', 'done'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const name = ref(props.kind === 'rename' ? props.group.name : '')
const note = ref('')
const days = ref(30)
const quotaGB = ref('')
const busy = ref(false)

const TITLES = { create: 'group.add', rename: 'group.rename', extend: 'group.extend', quota: 'group.setQuota' }

async function submit() {
  busy.value = true
  try {
    if (props.kind === 'create') {
      const n = name.value.trim()
      if (!n) return
      await api.post('/api/groups', { name: n, note: note.value.trim() })
      notify(t('group.created'), 'success')
    } else if (props.kind === 'rename') {
      const res = await api.renameGroup(props.group.name, name.value)
      notify(`${t('group.renamed')} — ${nf(res.affected)}`, 'success')
    } else {
      const op = props.kind === 'extend'
        ? { action: 'extend', group: props.group.name, days: Number(days.value) }
        : { action: 'quota', group: props.group.name, quotaBytes: gigabytesToBytes(quotaGB.value) }
      const res = await api.groupAction(op)
      notify(`${t('group.applied')} — ${nf(res.affected)}`, 'success')
    }
    emit('done')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="$emit('close')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="g-title">
      <div class="card-head">
        <h2 id="g-title">{{ t(TITLES[kind]) }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="$emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="g-form" class="card-body" @submit.prevent="submit">
        <p v-if="group" class="target">
          <span class="tag geekblue">{{ group.name }}</span>
          <span class="muted small">{{ nf(group.clients) }} {{ t('nav.clients') }}</span>
        </p>

        <template v-if="kind === 'create'">
          <div class="field">
            <label for="g-new">{{ t('group.name') }}</label>
            <input id="g-new" v-model="name" required autofocus maxlength="64" />
            <span class="hint">{{ t('group.addHint') }}</span>
          </div>
          <div class="field">
            <label for="g-note">{{ t('group.note') }}</label>
            <input id="g-note" v-model="note" maxlength="256" />
          </div>
        </template>

        <div v-else-if="kind === 'rename'" class="field">
          <label for="g-name">{{ t('group.newName') }}</label>
          <input id="g-name" v-model="name" required autofocus />
          <span class="hint">{{ t('group.renameHint') }}</span>
        </div>

        <div v-else-if="kind === 'extend'" class="field">
          <label for="g-days">{{ t('group.extendDays') }}</label>
          <input id="g-days" v-model="days" type="number" required autofocus />
          <span class="hint">{{ t('group.extendHint') }}</span>
        </div>

        <div v-else class="field">
          <label for="g-quota">{{ t('client.quota') }} (GB)</label>
          <input
            id="g-quota"
            v-model="quotaGB"
            type="number"
            min="0"
            step="0.5"
            :placeholder="t('client.unlimited')"
            autofocus
          />
          <span class="hint">{{ t('group.quotaHint') }}</span>
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="$emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="g-form" class="btn primary" :disabled="busy">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal.narrow {
  max-width: 440px;
}
.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.target {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line-soft);
}
</style>
