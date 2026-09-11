<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'
import Toggle from './Toggle.vue'
import MultiSelect from './MultiSelect.vue'

// The balancer dialog: a tag, a strategy, the outbounds it spreads over.
// Laid out as the rule dialog is, so the two read as one family.

const props = defineProps({
  balancer: { type: Object, default: null },
  outbounds: { type: Array, default: () => [] },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.balancer)
const busy = ref(false)
const fieldError = ref({})
const formError = ref('')

const form = ref({ tag: '', enabled: true, strategy: 'random', members: [], note: '' })
const STRATEGIES = ['random', 'leastPing']

onMounted(() => {
  if (props.balancer) {
    const b = props.balancer
    Object.assign(form.value, {
      tag: b.tag,
      enabled: b.enabled,
      strategy: b.strategy || 'random',
      members: b.memberList || [],
      note: b.note || '',
    })
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

// Only hops can be balanced over: the two built-ins have no device.
const memberOptions = computed(() =>
  props.outbounds
    .filter((o) => !o.builtin)
    .map((o) => ({ value: o.tag, label: o.tag, tags: [{ text: t(`outbound.kind.${o.kind}`), kind: 'green' }] })),
)

async function submit() {
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  try {
    const saved = props.balancer
      ? await api.patch(`/api/balancers/${props.balancer.id}`, form.value)
      : await api.post('/api/balancers', form.value)
    notify(editing.value ? t('routing.balancerUpdated') : t('routing.balancerCreated'), 'success')
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
    <div class="modal bl-modal" role="dialog" aria-modal="true" aria-labelledby="bl-title">
      <div class="card-head">
        <h2 id="bl-title">{{ editing ? `${t('action.edit')} ${t('routing.tab.balancers')}` : `+ ${t('routing.tab.balancers')}` }}</h2>
        <button class="act" :title="t('common.close')" @click="emit('cancel')"><Icon name="close" :size="16" /></button>
      </div>

      <div class="card-body">
        <p v-if="formError" class="form-error">{{ formError }}</p>
        <form class="hform" @submit.prevent="submit">
          <div class="hrow">
            <label>{{ t('table.enabled') }}</label>
            <div class="hctl"><Toggle v-model="form.enabled" :label="t('table.enabled')" /></div>
          </div>
          <div class="hrow">
            <label for="bl-tag" class="req">{{ t('outbound.tag') }}</label>
            <div class="hctl">
              <input id="bl-tag" v-model="form.tag" class="ltr" :placeholder="t('outbound.form.tagPlaceholder')" autocomplete="off" />
              <p v-if="fieldError.tag" class="field-error">{{ fieldError.tag }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="bl-strategy">{{ t('routing.balancer.strategy') }}</label>
            <div class="hctl">
              <select id="bl-strategy" v-model="form.strategy">
                <option v-for="s in STRATEGIES" :key="s" :value="s">{{ s }}</option>
              </select>
              <span class="hint">{{ t(`routing.balancer.strategy.${form.strategy}`) }}</span>
              <p v-if="fieldError.strategy" class="field-error">{{ fieldError.strategy }}</p>
            </div>
          </div>
          <div class="hrow">
            <label class="req">{{ t('nav.outbounds') }}</label>
            <div class="hctl">
              <MultiSelect v-model="form.members" :options="memberOptions" :placeholder="t('nav.outbounds')" />
              <p v-if="fieldError.members" class="field-error">{{ fieldError.members }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="bl-note">{{ t('routing.rule.note') }}</label>
            <div class="hctl"><input id="bl-note" v-model="form.note" autocomplete="off" /></div>
          </div>
        </form>
      </div>

      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('cancel')">{{ t('common.close') }}</button>
        <button type="button" class="btn primary" :disabled="busy" @click="submit">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ editing ? t('outbound.form.saveChanges') : t('outbound.form.create') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.bl-modal {
  max-width: 780px;
}
.card-body {
  padding: 24px 24px 8px;
}
.field-error {
  margin: 4px 0 0;
  color: var(--bad);
  font-size: var(--t-sm);
}
.form-error {
  margin: 0 0 12px;
  color: var(--bad);
  font-size: var(--t-sm);
}
</style>
