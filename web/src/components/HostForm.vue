<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

const props = defineProps({
  host: { type: Object, default: null },
  interfaces: { type: Array, default: () => [] },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.host)
const busy = ref(false)
const fieldError = ref({})
const formError = ref('')

const form = ref({
  interfaceId: 0,
  name: '',
  address: '',
  port: 0,
  priority: 0,
  note: '',
})

const chosen = computed(() => props.interfaces.find((i) => i.id === form.value.interfaceId))

onMounted(() => {
  if (props.host) {
    Object.assign(form.value, {
      interfaceId: props.host.interfaceId,
      name: props.host.name,
      address: props.host.address,
      port: props.host.port || 0,
      priority: props.host.priority || 0,
      note: props.host.note || '',
    })
  } else if (props.interfaces.length) {
    form.value.interfaceId = props.interfaces[0].id
  }
})

watch(
  () => ({ ...form.value }),
  () => {
    fieldError.value = {}
    formError.value = ''
  },
  { deep: true },
)

async function submit() {
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  try {
    const saved = props.host
      ? await api.patch(`/api/hosts/${props.host.id}`, form.value)
      : await api.post('/api/hosts', form.value)
    notify(editing.value ? t('host.updated') : t('host.created'), 'success')
    emit('saved', saved)
  } catch (err) {
    if (err.field) fieldError.value = { [err.field]: err.message }
    else formError.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="hf-title">
      <div class="card-head">
        <h2 id="hf-title">{{ editing ? t('host.edit') : t('host.add') }}</h2>
        <button class="act" :title="t('action.cancel')" @click="emit('cancel')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <form class="card-body form" @submit.prevent="submit">
        <p v-if="formError" class="form-error">{{ formError }}</p>

        <div class="field">
          <label for="hf-iface">{{ t('host.interface') }}</label>
          <select id="hf-iface" v-model.number="form.interfaceId">
            <option v-for="i in interfaces" :key="i.id" :value="i.id">
              {{ i.name }} — {{ i.protocol }}
            </option>
          </select>
          <p v-if="fieldError.interfaceId" class="field-error">{{ fieldError.interfaceId }}</p>
        </div>

        <div class="field">
          <label for="hf-name">{{ t('host.name') }}</label>
          <input id="hf-name" v-model="form.name" autocomplete="off" />
          <p class="hint">{{ t('host.nameHint') }}</p>
          <p v-if="fieldError.name" class="field-error">{{ fieldError.name }}</p>
        </div>

        <div class="field">
          <label for="hf-address">{{ t('host.address') }}</label>
          <input
            id="hf-address"
            v-model="form.address"
            class="ltr"
            autocomplete="off"
            placeholder="edge.example.com"
          />
          <p class="hint">{{ t('host.addressHint') }}</p>
          <p v-if="fieldError.address" class="field-error">{{ fieldError.address }}</p>
        </div>

        <div class="field">
          <label for="hf-port">{{ t('host.port') }}</label>
          <input id="hf-port" v-model.number="form.port" class="ltr" type="number" min="0" />
          <p class="hint">
            {{ t('host.portHint') }}<template v-if="chosen"> ({{ chosen.listenPort }})</template>
          </p>
          <p v-if="fieldError.port" class="field-error">{{ fieldError.port }}</p>
        </div>

        <div class="field">
          <label for="hf-prio">{{ t('host.priority') }}</label>
          <input id="hf-prio" v-model.number="form.priority" class="ltr" type="number" />
          <p class="hint">{{ t('host.priorityHint') }}</p>
        </div>

        <div class="field">
          <label for="hf-note">{{ t('host.note') }}</label>
          <input id="hf-note" v-model="form.note" autocomplete="off" />
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('cancel')">
          {{ t('action.cancel') }}
        </button>
        <button type="button" class="btn primary" :disabled="busy" @click="submit">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
