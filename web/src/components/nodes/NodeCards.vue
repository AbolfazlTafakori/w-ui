<script setup>
// On a phone the table becomes the classic panel's node cards: the head
// opens the readings, and the actions sit in one menu.
import { ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { ago, latencyTone, takesImport, uptime } from '../../lib/nodes/rows.js'
import AntIcon from '../AntIcon.vue'
import MoreMenu from '../MoreMenu.vue'

defineProps({
  nodes: { type: Array, required: true },
  pending: { type: Set, required: true },
})
const emit = defineEmits(['probe', 'update', 'import', 'edit', 'remove', 'toggle'])

const expanded = ref(new Set())
function toggleExpanded(id) {
  const next = new Set(expanded.value)
  next.has(id) ? next.delete(id) : next.add(id)
  expanded.value = next
}

const menuItems = (n) => [
  { key: 'probe', label: t('node.probe'), icon: 'ThunderboltOutlined' },
  { key: 'update', label: t('update.askNode'), icon: 'DownloadOutlined' },
  ...(takesImport(n) ? [{ key: 'import', label: t('nodeImport.action'), icon: 'ImportOutlined' }] : []),
  { key: 'edit', label: t('action.edit'), icon: 'EditOutlined' },
  { key: 'remove', label: t('action.delete'), icon: 'DeleteOutlined', danger: true },
]
</script>

<template>
  <div class="node-cards">
    <div v-if="!nodes.length" class="card-empty">
      <AntIcon name="ClusterOutlined" :size="28" style="opacity: 0.5" />
      <div>{{ t('common.nothingYet') }}</div>
    </div>
    <div v-for="n in nodes" :key="n.id" class="node-card">
      <div class="card-head" @click="toggleExpanded(n.id)">
        <AntIcon name="RightOutlined" class="card-expand" :class="{ 'is-expanded': expanded.has(n.id) }" />
        <i class="abadge-dot" :class="n.kind === 'local' || n.reachable ? 'green' : !n.enabled ? '' : 'red'"></i>
        <span class="node-name">{{ n.name }}</span>
        <span v-if="n.kind === 'local'" class="tag grey">{{ t('node.thisPanel') }}</span>
        <span v-else-if="n.kind === 'xui'" class="tag blue">3x-ui</span>
        <span v-else-if="n.kind === 'pasarguard'" class="tag blue">Pasarguard</span>
        <div class="card-actions" @click.stop>
          <input type="checkbox" class="acheck" :checked="n.enabled" :disabled="n.kind === 'local' || pending.has(n.id)" :aria-label="n.name" @change="emit('toggle', n, $event.target.checked)" />
          <MoreMenu v-if="n.kind !== 'local'" :items="menuItems(n)" compact button-class="row-action-trigger" :label="t('action.more')" @pick="(key) => emit(key, n)" />
        </div>
      </div>
      <div v-if="expanded.has(n.id)" class="card-history card-stats">
        <div class="stat-row"><span class="stat-label">{{ t('node.address') }}</span><span class="ltr">{{ n.address || '—' }}</span></div>
        <div class="stat-row"><span class="stat-label">{{ t('node.status') }}</span>
          <span v-if="n.kind === 'local'" class="tag green">{{ t('node.running') }}</span>
          <span v-else-if="!n.enabled" class="tag grey">{{ t('status.disabled') }}</span>
          <span v-else-if="n.reachable" class="tag green">{{ t('node.online') }}</span>
          <span v-else class="tag red" :title="n.lastError">{{ t('node.offline') }}</span>
        </div>
        <div class="stat-row"><span class="stat-label">CPU / RAM</span><span class="ltr">{{ n.cpuPercent ? n.cpuPercent.toFixed(0) + '%' : '—' }} / {{ n.memPercent ? n.memPercent.toFixed(0) + '%' : '—' }}</span></div>
        <div class="stat-row"><span class="stat-label">{{ t('node.version') }}</span><span class="ltr">{{ n.version || '—' }}</span></div>
        <div class="stat-row"><span class="stat-label">{{ t('node.uptime') }}</span><span class="ltr">{{ uptime(n.uptimeSec) }}</span></div>
        <div class="stat-row"><span class="stat-label">{{ t('client.traffic') }}</span><span class="ltr">{{ bytes(n.usedBytes || 0, store.locale) }}<template v-if="n.dataLimitBytes"> / {{ bytes(n.dataLimitBytes, store.locale) }}</template></span></div>
        <div class="stat-row"><span class="stat-label">{{ t('node.latency') }}</span><span class="tag num ltr" :class="latencyTone(n.latencyMs)">{{ n.latencyMs || 0 }} ms</span></div>
        <div class="stat-row"><span class="stat-label">{{ t('node.lastSeen') }}</span><span>{{ n.kind === 'local' ? t('node.justNow') : ago(n.lastSeenAt) }}</span></div>
      </div>
    </div>
  </div>
</template>
