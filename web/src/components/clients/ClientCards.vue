<script setup>
// The customer list on a phone: a card each, its actions behind one menu.
import { onMounted, onUnmounted, ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import {
  barColor, expiryTag, remainingTag, speedActive, speedText, statusTag, usedPercent,
} from '../../lib/clientRow.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import Pager from '../Pager.vue'

defineProps({
  // The customers on this page, and which are chosen.
  items: { type: Array, required: true },
  selected: { type: Object, required: true },
  allSelected: { type: Boolean, default: false },
  // Rows with a change in flight, whose switch spins.
  pending: { type: Object, required: true },
  interfaces: { type: Array, default: () => [] },
  total: { type: Number, default: 0 },
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 25 },
  stale: { type: Boolean, default: false },
})
const emit = defineEmits(['toggle', 'toggle-all', 'qr', 'info', 'rotate', 'reset', 'edit', 'remove', 'enable', 'group', 'go'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// Each card's actions, behind its own menu.
const cardMenu = ref(null)
function openCardMenu(c, e) {
  e.stopPropagation()
  const r = e.currentTarget.getBoundingClientRect()
  cardMenu.value = cardMenu.value?.client?.id === c.id ? null : { client: c, rect: r, x: Math.max(8, r.right - 180), y: r.bottom + 4 }
}
function cardAction(key) {
  const c = cardMenu.value?.client
  cardMenu.value = null
  if (!c) return
  emit({ qr: 'qr', rotate: 'rotate', reset: 'reset', edit: 'edit', delete: 'remove' }[key], c)
}
function onDocClick(e) {
  if (cardMenu.value && !e.target.closest?.('.rowmenu') && !e.target.closest?.('.row-action-trigger')) cardMenu.value = null
}
function onKey(e) {
  if (e.key === 'Escape') cardMenu.value = null
}
onMounted(() => {
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div class="client-cards" :class="{ stale }">
    <div v-if="items.length" class="card-bulk-bar">
      <label class="acheckbox">
        <input type="checkbox" class="acheck" :checked="allSelected" @change="emit('toggle-all', $event.target.checked)" />
        <span>{{ t('action.selectAll') }}</span>
      </label>
      <span v-if="selected.size" class="bulk-count">{{ nf(selected.size) }}</span>
    </div>
    <div v-if="!items.length" class="card-empty">
      <AntIcon name="TeamOutlined" :size="28" style="opacity: 0.5" />
      <div>{{ t('common.nothingYet') }}</div>
    </div>
    <div v-if="total > pageSize" class="card-pagination">
      <Pager small :total="total" :page="page" :page-size="pageSize" @go="(n) => emit('go', n)" />
    </div>
    <div v-for="c in items" :key="c.id" class="client-card" :class="{ 'is-selected': selected.has(c.id) }">
      <div class="card-head">
        <input type="checkbox" class="acheck" :checked="selected.has(c.id)" :aria-label="c.name" @change="emit('toggle', c.id, $event.target.checked)" />
        <i v-if="statusTag(c).dot" class="online-dot" style="margin-inline-end: 0"></i>
        <i v-else class="abadge-dot" :class="statusTag(c).color"></i>
        <router-link class="tag-name" :to="`/clients/${c.id}`" :title="t('client.openPage')">{{ c.name }}</router-link>
        <span v-if="c.status === 'exhausted' || c.status === 'expired'" class="atag red status-tag">{{ t('stat.depleted') }}</span>
        <span v-else-if="remainingTag(c).color === 'orange' || expiryTag(c).color === 'orange'" class="atag orange status-tag">{{ t('stat.depleting') }}</span>
        <div class="card-actions">
          <button type="button" class="row-action-trigger" :aria-label="t('client.menu.clientInfo')" @click="emit('info', c)"><AntIcon name="InfoCircleOutlined" /></button>
          <span :title="c.ownerPaused ? statusTag(c).title : ''" class="toggle-wrap"><Toggle :model-value="c.status === 'active' && !c.ownerPaused" :label="c.name" small :disabled="c.status === 'expired' || c.status === 'exhausted' || !!c.ownerPaused" :loading="pending.has(c.id)" @update:model-value="(v) => emit('enable', c, v)" /></span>
          <button type="button" class="row-action-trigger" :aria-label="t('action.more')" :aria-expanded="cardMenu?.client?.id === c.id" @click="openCardMenu(c, $event)"><AntIcon name="MoreOutlined" /></button>
        </div>
      </div>
      <span v-if="c.note" class="client-card-comment">{{ c.note }}</span>
      <div class="client-traffic-cell is-compact" :class="{ 'is-unlimited': !c.quotaBytes }">
        <span class="client-traffic-cell-used ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
        <span class="aprogress client-traffic-cell-bar"><span :style="{ width: (c.quotaBytes ? Math.min(100, usedPercent(c) ?? 0) : 100) + '%', background: barColor(c) }"></span></span>
        <span class="client-traffic-cell-limit ltr">
          <span v-if="!c.quotaBytes" class="client-traffic-cell-infinity">∞</span>
          <template v-else>{{ bytes(c.quotaBytes, store.locale) }}</template>
        </span>
      </div>
      <div v-if="speedActive(c)" class="client-card-speed"><span class="atag blue ltr speed-num" style="margin: 0">{{ speedText(c) }}</span></div>
    </div>
    <Teleport to="body">
      <div v-if="cardMenu" v-fit="cardMenu.rect" class="rowmenu" role="menu" :style="{ top: cardMenu.y + 'px', left: cardMenu.x + 'px' }">
        <button class="menu-item" role="menuitem" @click="cardAction('qr')"><AntIcon name="QrcodeOutlined" />{{ t('client.qrCode') }}</button>
        <button class="menu-item" role="menuitem" @click="cardAction('rotate')"><AntIcon name="KeyOutlined" />{{ t('client.rotateKeys') }}</button>
        <button class="menu-item" role="menuitem" @click="cardAction('reset')"><AntIcon name="RetweetOutlined" />{{ t('outbound.resetTraffic') }}</button>
        <button class="menu-item" role="menuitem" @click="cardAction('edit')"><AntIcon name="EditOutlined" />{{ t('action.edit') }}</button>
        <button class="menu-item danger" role="menuitem" @click="cardAction('delete')"><AntIcon name="DeleteOutlined" />{{ t('action.delete') }}</button>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
a.tag-name { color: inherit; text-decoration: none; }
.speed-num { font-variant-numeric: tabular-nums; }
</style>
