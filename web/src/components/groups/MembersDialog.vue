<script setup>
// The members picker, shared by "add clients" and "remove clients". They
// differ only in which customers are listed and what the save does, so one
// dialog with two modes beats two dialogs that drift apart.
import { computed, onMounted, ref } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, tn, notify } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { memberChoices } from '../../lib/groups/rows.js'
import Icon from '../Icon.vue'

const props = defineProps({
  group: { type: Object, required: true },
  // 'add' or 'remove'.
  mode: { type: String, required: true },
})
const emit = defineEmits(['close', 'done'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const clients = ref([])
const search = ref('')
const chosen = ref(new Set())
const busy = ref(false)

onMounted(async () => {
  try {
    clients.value = await api.allClients({}, {})
  } catch (e) {
    notify(e.message, 'error')
    emit('close')
  }
})

const choices = computed(() => memberChoices(clients.value, props.group.name, props.mode, search.value))

function toggle(id) {
  const next = new Set(chosen.value)
  next.has(id) ? next.delete(id) : next.add(id)
  chosen.value = next
}

async function apply() {
  if (!chosen.value.size) return
  busy.value = true
  try {
    // One endpoint serves both: adding to this group, and taking out of
    // this group only -- a customer's other groups are theirs to keep.
    await api.post('/api/groups/assign', {
      group: props.group.name,
      ids: [...chosen.value],
      remove: props.mode !== 'add',
    })
    notify(tn(props.mode === 'add' ? 'group.clientsAdded' : 'group.clientsRemoved', chosen.value.size), 'success')
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
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="m-title">
      <div class="card-head">
        <h2 id="m-title">
          {{ mode === 'add' ? t('group.addClients') : t('group.removeClients') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="$emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <p class="target">
          <span class="tag geekblue">{{ group.name }}</span>
          <span class="muted small">{{ nf(chosen.size) }} {{ t('group.selected') }}</span>
        </p>

        <div class="field">
          <input v-model="search" type="search" :placeholder="t('client.searchHint')" />
        </div>

        <p v-if="!choices.length" class="muted small">{{ t('group.noCandidates') }}</p>

        <ul v-else class="picklist">
          <li v-for="c in choices" :key="c.id">
            <label class="pick">
              <input type="checkbox" :checked="chosen.has(c.id)" @change="toggle(c.id)" />
              <span class="pick-name">{{ c.name }}</span>
              <span v-for="g in c.groups || []" :key="g" class="tag geekblue">{{ g }}</span>
              <span class="muted small ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
            </label>
          </li>
        </ul>
      </div>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="$emit('close')">{{ t('action.cancel') }}</button>
        <button
          class="btn primary"
          :disabled="busy || !chosen.size"
          :title="!chosen.size ? t('group.pickSomeone') : ''"
          @click="apply"
        >
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
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
