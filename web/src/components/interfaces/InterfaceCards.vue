<script setup>
// The tunnels on a phone: a card each, its stats behind its info button and
// its actions behind one menu. The pager is the page's, in the slot.
import { ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { protoTag, rowItems, speedActive, speedText, transportTag } from '../../lib/interfaces/rows.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import MoreMenu from '../MoreMenu.vue'

defineProps({
  // The tunnels on this page, and which are chosen.
  rows: { type: Array, required: true },
  selected: { type: Object, required: true },
  allSelected: { type: Boolean, default: false },
  // Tunnels with a change in flight, whose switch spins.
  pending: { type: Object, required: true },
})
const emit = defineEmits(['toggle', 'toggle-all', 'enable', 'pick'])
const nf = (n) => Number(n || 0).toLocaleString(store.locale)

// Which cards show their stats.
const cardOpen = ref(new Set())
function toggleCard(id) {
  const next = new Set(cardOpen.value)
  next.has(id) ? next.delete(id) : next.add(id)
  cardOpen.value = next
}
</script>

<template>
  <div class="inbound-cards">
    <div v-if="!rows.length" class="card-empty">
      <AntIcon name="ImportOutlined" :size="28" style="opacity: 0.5" />
      <div>{{ t('common.nothingYet') }}</div>
    </div>
    <template v-else>
      <div class="card-bulk-bar">
        <label class="acheckbox">
          <input type="checkbox" class="acheck" :checked="allSelected" @change="emit('toggle-all', $event.target.checked)" />
          <span>{{ t('action.selectAll') }}</span>
        </label>
        <span v-if="selected.size" class="bulk-count">{{ nf(selected.size) }}</span>
      </div>
      <div v-for="i in rows" :key="i.id" class="inbound-card" :class="{ 'is-selected': selected.has(i.id) }">
        <div class="card-head" @click="toggleCard(i.id)">
          <input type="checkbox" class="acheck" :checked="selected.has(i.id)" :aria-label="i.name" @click.stop @change="emit('toggle', i.id, $event.target.checked)" />
          <span class="card-id">#{{ i.id }}</span>
          <span class="tag-name">{{ i.name }}</span>
          <div class="card-actions" @click.stop>
            <button type="button" class="row-action-trigger" :aria-label="t('iface.menu.info')" :title="t('iface.menu.info')" @click="toggleCard(i.id)"><AntIcon name="InfoCircleOutlined" /></button>
            <Toggle :model-value="i.enabled" :label="i.name" :loading="pending.has(i.id)" small @update:model-value="(v) => emit('enable', i, v)" />
            <MoreMenu :items="rowItems(i)" compact button-class="row-action-trigger" :label="t('action.more')" @pick="(key) => emit('pick', i, key)" />
          </div>
        </div>
        <div v-if="cardOpen.has(i.id)" class="card-stats">
          <div class="stat-row">
            <span class="stat-label">{{ t('client.protocol') }}</span>
            <span class="atag purple">{{ protoTag(i) }}</span>
            <span class="atag green">{{ transportTag(i) }}</span>
            <span v-if="i.mode === 'amnezia'" class="atag blue">AmneziaWG</span>
          </div>
          <div class="stat-row">
            <span class="stat-label">{{ t('interface.port') }}</span>
            <span class="ltr">{{ i.listenPort }}</span>
          </div>
          <div class="stat-row">
            <span class="stat-label">{{ t('nav.clients') }}</span>
            <span class="atag count"><AntIcon name="TeamOutlined" /> {{ nf(i.clients) }}</span>
            <span class="atag green count">{{ nf(i.active) }}</span>
            <span v-if="i.depleted" class="atag red count">{{ nf(i.depleted) }}</span>
            <span v-if="i.online" class="atag blue count">{{ nf(i.online) }}</span>
          </div>
          <div class="stat-row">
            <span class="stat-label">{{ t('client.traffic') }}</span>
            <span class="atag purple ltr">{{ bytes(i.usedBytes, store.locale) }} / <span class="infinity">∞</span></span>
          </div>
          <div v-if="speedActive(i)" class="stat-row">
            <span class="stat-label">{{ t('client.speed') }}</span>
            <span class="atag blue ltr">{{ speedText(i) }}</span>
          </div>
        </div>
      </div>
      <slot />
    </template>
  </div>
</template>
