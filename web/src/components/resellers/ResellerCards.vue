<script setup>
// A phone gets a card each: the same figures as the table, stacked.
import { t } from '../../lib/store.js'
import { customersTag, standing, termTag, trafficLeftTag } from '../../lib/resellers/rows.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

defineProps({
  rows: { type: Array, required: true },
  selected: { type: Set, required: true },
  pending: { type: Set, required: true },
  allSelected: { type: Boolean, default: false },
  someSelected: { type: Boolean, default: false },
})
defineEmits(['toggle', 'toggle-all', 'edit', 'reset', 'remove', 'enable'])
</script>

<template>
  <div class="rcards">
    <label class="acheckbox card-bulk-bar">
      <input type="checkbox" class="acheck" :checked="allSelected" :indeterminate="someSelected" @change="$emit('toggle-all', $event.target.checked)" />
      <span>{{ t('action.selectAll') }}</span>
    </label>
    <div v-for="a in rows" :key="a.id" class="rcard" :class="{ off: !a.enabled, picked: selected.has(a.id) }">
      <div class="rcard-head">
        <input type="checkbox" class="acheck" :checked="selected.has(a.id)" :aria-label="a.username" @change="$emit('toggle', a.id, $event.target.checked)" />
        <div class="rcard-name">
          <span class="name">{{ a.username }}</span>
          <span class="atag" dir="auto" :class="standing(a).color" style="margin: 0">{{ standing(a).label }}</span>
        </div>
        <Toggle :model-value="a.enabled" :label="a.username" small :loading="pending.has(a.id)" @update:model-value="(v) => $emit('enable', a, v)" />
      </div>
      <div v-if="a.note" class="rcard-note">{{ a.note }}</div>
      <dl v-if="a.role === 'reseller'" class="rcard-stats">
        <div><dt>{{ t('admins.customers') }}</dt><dd><span class="atag ltr" :class="customersTag(a).color" style="margin: 0">{{ customersTag(a).label }}</span></dd></div>
        <div><dt>{{ t('admins.trafficLeft') }}</dt><dd><span class="atag ltr" :class="trafficLeftTag(a).color" style="margin: 0">{{ trafficLeftTag(a).label }}</span></dd></div>
        <div><dt>{{ t('admins.timeLeft') }}</dt><dd><span class="atag" dir="auto" :class="termTag(a).color" :title="termTag(a).title" style="margin: 0">{{ termTag(a).label }}</span></dd></div>
      </dl>
      <div v-else class="rcard-note"><span class="atag geekblue" style="margin: 0">{{ t('admins.role.admin') }}</span></div>
      <div class="rcard-actions">
        <button class="abtn text sm" :aria-label="t('action.edit')" @click="$emit('edit', a)"><AntIcon name="EditOutlined" /><span>{{ t('action.edit') }}</span></button>
        <button v-if="a.role === 'reseller' && a.quotaBytes" class="abtn text sm" :disabled="pending.has(a.id)" @click="$emit('reset', a)"><AntIcon name="RetweetOutlined" /><span>{{ t('admins.resetShort') }}</span></button>
        <button class="abtn text sm danger" @click="$emit('remove', a)"><AntIcon name="DeleteOutlined" /><span>{{ t('action.delete') }}</span></button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rcards { display: flex; flex-direction: column; gap: 10px; }
.rcard { padding: 12px; border: 1px solid var(--line); border-radius: 8px; background: var(--surface); }
.rcard.off { opacity: 0.7; }
.rcard-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.rcard-name { display: flex; align-items: center; gap: 8px; min-width: 0; flex-wrap: wrap; }
.rcard-name .name { font-weight: 600; }
.rcard-note { margin-top: 4px; font-size: 12px; color: var(--muted); }
.rcard-stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin: 10px 0 0; }
.rcard-stats dt { font-size: 11px; color: var(--faint); margin-bottom: 2px; }
.rcard-stats dd { margin: 0; }
/* A tag here has a third of a phone to live in; a long one wraps inside it
   rather than pushing out past the card. */
.rcard-stats dd .atag { max-width: 100%; white-space: normal; line-height: 1.4; }
.rcard-actions { display: flex; gap: 4px; flex-wrap: wrap; margin-top: 10px; padding-top: 8px; border-top: 1px solid var(--line); }
.rcard-actions .abtn span { margin-inline-start: 4px; }
.rcard.picked { border-color: var(--accent); }
.rcard-head .acheck { flex: none; }
.card-bulk-bar { display: flex; align-items: center; gap: 8px; padding: 4px 2px; }
</style>
