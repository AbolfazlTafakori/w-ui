<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

const props = defineProps({
  rule: { type: Object, default: null },
  outbounds: { type: Array, default: () => [] },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.rule)
const busy = ref(false)
const fieldError = ref({})
const formError = ref('')
const groups = ref([])
const clients = ref([])

const form = ref({
  name: '',
  match: 'domain',
  value: '',
  outboundTag: 'direct',
  note: '',
})

const matches = ['domain', 'ip', 'port', 'protocol', 'client', 'group']

// What the value field should look like depends entirely on what is being
// matched. One generic text box for all six would make four of them guesswork.
const valueKind = computed(() => {
  switch (form.value.match) {
    case 'protocol':
      return 'protocol'
    case 'client':
      return 'client'
    case 'group':
      return 'group'
    default:
      return 'text'
  }
})

const placeholder = computed(
  () =>
    ({
      domain: 'netflix.com, example.org',
      ip: '8.8.8.0/24, private',
      port: '443, 6881-6889',
    })[form.value.match] || '',
)

onMounted(async () => {
  if (props.rule) Object.assign(form.value, props.rule)

  // Only fetched for the two matches that need a list to pick from.
  try {
    const [g, c] = await Promise.all([
      api.get('/api/groups', { background: true }).catch(() => []),
      api.get('/api/clients?limit=500', { background: true }).catch(() => null),
    ])
    groups.value = Array.isArray(g) ? g : g?.groups || []
    clients.value = c?.clients || (Array.isArray(c) ? c : [])
  } catch {
    // The fields fall back to free text, so this failing is survivable.
  }
})

watch(
  () => form.value.match,
  () => {
    // A value written for one kind of match is meaningless for another, so it
    // is cleared rather than carried over and rejected on save.
    if (!editing.value || form.value.match !== props.rule?.match) form.value.value = ''
    fieldError.value = {}
  },
)

async function submit() {
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  try {
    const saved = props.rule
      ? await api.patch(`/api/routing/rules/${props.rule.id}`, form.value)
      : await api.post('/api/routing/rules', form.value)
    notify(editing.value ? t('routing.ruleUpdated') : t('routing.ruleCreated'), 'success')
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
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="rr-title">
      <div class="card-head">
        <h2 id="rr-title">{{ editing ? t('routing.editRule') : t('routing.addRule') }}</h2>
        <button class="act" :title="t('action.cancel')" @click="emit('cancel')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <form class="card-body form" @submit.prevent="submit">
        <p v-if="formError" class="form-error">{{ formError }}</p>

        <div class="field">
          <label for="rr-name">{{ t('routing.rule.name') }}</label>
          <input id="rr-name" v-model="form.name" autocomplete="off" />
          <p class="hint">{{ t('routing.rule.nameHint') }}</p>
          <p v-if="fieldError.name" class="field-error">{{ fieldError.name }}</p>
        </div>

        <div class="field">
          <label for="rr-match">{{ t('routing.rule.match') }}</label>
          <select id="rr-match" v-model="form.match">
            <option v-for="m in matches" :key="m" :value="m">{{ t(`routing.match.${m}`) }}</option>
          </select>
          <p v-if="fieldError.match" class="field-error">{{ fieldError.match }}</p>
        </div>

        <div class="field">
          <label for="rr-value">{{ t('routing.rule.value') }}</label>

          <select v-if="valueKind === 'protocol'" id="rr-value" v-model="form.value">
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="icmp">ICMP</option>
          </select>

          <select v-else-if="valueKind === 'group'" id="rr-value" v-model="form.value">
            <option value="" disabled>{{ t('form.choose') }}</option>
            <option v-for="g in groups" :key="g.id || g.name" :value="g.name">{{ g.name }}</option>
          </select>

          <select v-else-if="valueKind === 'client'" id="rr-value" v-model="form.value">
            <option value="" disabled>{{ t('form.choose') }}</option>
            <option v-for="c in clients" :key="c.id" :value="String(c.id)">{{ c.name }}</option>
          </select>

          <input
            v-else
            id="rr-value"
            v-model="form.value"
            class="ltr"
            autocomplete="off"
            :placeholder="placeholder"
          />

          <p class="hint">{{ t(`routing.rule.valueHint.${form.match}`) }}</p>
          <p v-if="fieldError.value" class="field-error">{{ fieldError.value }}</p>
        </div>

        <div class="field">
          <label for="rr-ob">{{ t('routing.rule.outbound') }}</label>
          <select id="rr-ob" v-model="form.outboundTag">
            <option v-for="o in outbounds" :key="o.id" :value="o.tag" :disabled="!o.enabled">
              {{ o.tag }}<template v-if="!o.enabled"> — {{ t('routing.disabled') }}</template>
            </option>
          </select>
          <p v-if="fieldError.outboundTag" class="field-error">{{ fieldError.outboundTag }}</p>
        </div>

        <div class="field">
          <label for="rr-note">{{ t('routing.rule.note') }}</label>
          <input id="rr-note" v-model="form.note" autocomplete="off" />
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
