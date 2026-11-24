<script setup>
// Whose customers these are: the operator to hand them to, whether what
// they used before the move counts on that operator's traffic too, and --
// for customers that already exist -- what the move would take, asked of the
// server as a dry run so the line below says exactly what saving will do.
//
// The owner chooses among themselves and every admin and reseller; an admin
// among themselves and their own resellers. A reseller is never shown this.
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../../lib/api.js'
import { store, t, tn } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import Toggle from '../Toggle.vue'
import HelpTip from '../HelpTip.vue'
import SearchSelect from '../SearchSelect.vue'
import { PANEL_TONE } from '../../lib/clientRow.js'

const props = defineProps({
  // { to, carryUsage }
  modelValue: { type: Object, required: true },
  // The customers it is for; empty while one is being made.
  ids: { type: Array, default: () => [] },
  // Whose they are now, or null when a selection is mixed.
  current: { type: Number, default: null },
})
const emit = defineEmits(['update:modelValue'])

const owner = computed(() => !!store.admin?.managesPanel)
const operators = ref([])
onMounted(async () => {
  try {
    operators.value = ((await api.admins()).items || []).filter((a) => a.role === 'admin' || a.role === 'reseller')
  } catch {
    operators.value = []
  }
})

// The choices, the caller first: the owner's own customers are theirs as
// zero; an admin's are their own id. Each named, with its role in the
// colour the customer list gives that panel; admins before resellers, each
// by name.
const roleTag = (role) => ({ text: t(`admins.role.${role}`), kind: PANEL_TONE[role] })
const options = computed(() => {
  const me = store.admin
  const out = []
  if (owner.value) out.push({ value: 0, label: t('owner.me'), tags: [roleTag('owner')] })
  else if (me) out.push({ value: me.id, label: `${me.username} (${t('owner.me')})`, tags: [roleTag('admin')] })
  const rank = (a) => (a.role === 'admin' ? 0 : 1)
  const rest = operators.value
    .filter((a) => owner.value || a.id !== me?.id)
    .sort((a, b) => rank(a) - rank(b) || a.username.localeCompare(b.username))
  for (const a of rest) {
    out.push({ value: a.id, label: a.username, tags: [roleTag(a.role === 'admin' ? 'admin' : 'reseller')], disabled: a.enabled === false })
    if (a.enabled === false) out[out.length - 1].note = t('status.disabled')
  }
  return out
})

const to = computed({
  get: () => props.modelValue.to,
  set: (v) => emit('update:modelValue', { ...props.modelValue, to: Number(v) }),
})
const carry = computed({
  get: () => props.modelValue.carryUsage,
  set: (v) => emit('update:modelValue', { ...props.modelValue, carryUsage: !!v }),
})
const moving = computed(() => props.current === null || to.value !== props.current)

// What the move would do, for customers that exist.
const preview = ref(null)
const previewError = ref('')
let seq = 0
async function ask() {
  const n = ++seq
  preview.value = null
  previewError.value = ''
  if (!props.ids.length || !moving.value) return
  try {
    const res = await api.post('/api/clients/transfer', {
      ids: props.ids, to: to.value, carryUsage: carry.value, dryRun: true,
    })
    if (n === seq) preview.value = res
  } catch (err) {
    if (n === seq) previewError.value = err.message
  }
}
watch(() => [to.value, carry.value, props.ids.join(',')], ask, { immediate: true })

const receiver = computed(() => options.value.find((o) => o.value === to.value)?.label || '')
</script>

<template>
  <div class="owner-picker">
    <div class="aform-item">
      <label class="aform-label" for="op-to">{{ t('owner.to') }} <HelpTip :text="t('owner.toHint')" /></label>
      <SearchSelect id="op-to" v-model="to" :options="options" :search-placeholder="t('owner.search')" />
    </div>

    <label v-if="moving" class="perm-item">
      <div class="perm-meta">
        <div class="perm-title">{{ t('owner.carry') }}</div>
        <div class="perm-desc">{{ t('owner.carryHint') }}</div>
      </div>
      <Toggle v-model="carry" :label="t('owner.carry')" />
    </label>

    <div v-if="moving" class="aalert with-desc" :class="previewError ? 'warning' : 'info'" role="status">
      <div class="aalert-body">
        <span v-if="previewError" class="aalert-desc">{{ previewError }}</span>
        <span v-else-if="preview" class="aalert-desc">
          {{ tn('owner.willMove', preview.moved) }}
          <template v-if="preview.units"> · {{ tn('owner.takes', preview.units).replace('{who}', receiver) }}</template>
          <template v-if="preview.carriedBytes"> · {{ t('owner.carries', { b: bytes(preview.carriedBytes, store.locale) }) }}</template>
          <template v-if="preview.serversAdded || preview.serversRemoved"> · {{ t('owner.servers', { a: preview.serversAdded, r: preview.serversRemoved }) }}</template>
        </span>
        <span v-else class="aalert-desc">{{ t('owner.saleNote') }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.owner-picker { display: flex; flex-direction: column; gap: 12px; }
.owner-picker .aform-item { margin-bottom: 0; }
.perm-item {
  display: flex; align-items: center; gap: 16px;
  padding: 12px 14px;
  border: 1px solid var(--line-soft); border-radius: 8px;
  background: var(--surface-2, transparent);
  cursor: pointer;
}
.perm-meta { flex: 1 1 auto; min-width: 0; }
.perm-title { font-size: 14px; font-weight: 500; color: var(--ink); }
.perm-desc { margin-top: 2px; font-size: 12px; color: var(--faint); line-height: 1.5; }
</style>
