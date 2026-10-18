<script setup>
// The tunnels as a table, a row each, with Ant's sortable headers. The pager
// is the page's, in the slot.
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { protoTag, rowItems, speedActive, speedText, transportTag } from '../../lib/interfaces/rows.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import MoreMenu from '../MoreMenu.vue'

const props = defineProps({
  rows: { type: Array, required: true },
  selected: { type: Object, required: true },
  allSelected: { type: Boolean, default: false },
  pending: { type: Object, required: true },
  // Whether any tunnel is on another server, which adds that column.
  hasNodes: { type: Boolean, default: false },
  // The order: { key, dir }.
  sort: { type: Object, required: true },
})
const emit = defineEmits(['toggle', 'toggle-all', 'enable', 'pick', 'edit', 'sort'])
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const sortCls = (key) => (props.sort.key === key ? `sorted ${props.sort.dir}` : '')
</script>

<template>
  <div class="atable-wrap">
    <table class="atable small" :style="{ minWidth: (hasNodes ? 1366 : 1236) + 'px' }">
      <thead>
        <tr>
          <th class="sel">
            <input type="checkbox" class="acheck" :checked="allSelected" :aria-label="t('action.selectAll')" @change="emit('toggle-all', $event.target.checked)" />
          </th>
          <th class="w-id right sortable" :class="sortCls('id')" @click="emit('sort', 'id')">
            <div class="sorters"><span class="title">ID</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-menu center">{{ t('iface.col.menu') }}</th>
          <th class="w-enable center">{{ t('table.enabled') }}</th>
          <th class="w-remark center sortable" :class="sortCls('remark')" @click="emit('sort', 'remark')">
            <div class="sorters"><span class="title">{{ t('iface.col.remark') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th v-if="hasNodes" class="w-node center sortable" :class="sortCls('node')" @click="emit('sort', 'node')">
            <div class="sorters"><span class="title">{{ t('iface.col.node') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-port center sortable" :class="sortCls('port')" @click="emit('sort', 'port')">
            <div class="sorters"><span class="title">{{ t('interface.port') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-proto sortable" :class="sortCls('protocol')" @click="emit('sort', 'protocol')">
            <div class="sorters"><span class="title">{{ t('client.protocol') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-clients sortable" :class="sortCls('clients')" @click="emit('sort', 'clients')">
            <div class="sorters"><span class="title">{{ t('nav.clients') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-itraffic center sortable" :class="sortCls('traffic')" @click="emit('sort', 'traffic')">
            <div class="sorters"><span class="title">{{ t('client.traffic') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-speed center sortable" :class="sortCls('speed')" @click="emit('sort', 'speed')">
            <div class="sorters"><span class="title">{{ t('client.speed') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
          <th class="w-dur center sortable" :class="sortCls('expiry')" @click="emit('sort', 'expiry')">
            <div class="sorters"><span class="title">{{ t('iface.col.duration') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length" class="empty-row">
          <td :colspan="hasNodes ? 12 : 11">
            <div class="card-empty">
              <AntIcon name="ImportOutlined" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="i in rows" :key="i.id" :class="{ picked: selected.has(i.id) }">
          <td class="sel">
            <input type="checkbox" class="acheck" :checked="selected.has(i.id)" :aria-label="i.name" @change="emit('toggle', i.id, $event.target.checked)" />
          </td>
          <td class="right">{{ i.id }}</td>
          <td class="center">
            <div class="action-buttons">
              <button class="abtn text sm" :title="t('action.edit')" @click="emit('edit', i)"><AntIcon name="EditOutlined" /></button>
              <MoreMenu :items="rowItems(i)" compact button-class="abtn text sm" :label="t('action.more')" @pick="(key) => emit('pick', i, key)" />
            </div>
          </td>
          <td class="center">
            <Toggle :model-value="i.enabled" :label="i.name" :loading="pending.has(i.id)" :disabled="i.protocol === 'xray'" @update:model-value="(v) => emit('enable', i, v)" />
          </td>
          <td class="center">{{ i.name }}</td>
          <td v-if="hasNodes" class="center">
            <span v-if="i.nodeName && !i.nodeLocal" class="atag" :class="i.nodeUp ? 'blue' : 'red'">{{ i.nodeName }}</span>
            <span v-else class="atag">{{ t('iface.col.localPanel') }}</span>
          </td>
          <td class="center">{{ i.listenPort }}</td>
          <td>
            <div class="protocol-tags">
              <span class="atag purple">{{ protoTag(i) }}</span>
              <span class="atag green">{{ transportTag(i) }}</span>
              <span v-if="i.mode === 'amnezia'" class="atag blue">AmneziaWG</span>
            </div>
          </td>
          <td>
            <span class="atag count" :title="t('nav.clients')"><AntIcon name="TeamOutlined" /> {{ nf(i.clients) }}</span>
            <span class="atag green count" :title="t('status.active')">{{ nf(i.active) }}</span>
            <span v-if="i.disabled" class="atag count" :title="t('status.disabled')">{{ nf(i.disabled) }}</span>
            <span v-if="i.depleted" class="atag red count" :title="t('stat.depleted')">{{ nf(i.depleted) }}</span>
            <span v-if="i.online" class="atag blue count last" :title="t('status.online')">{{ nf(i.online) }}</span>
          </td>
          <td class="center">
            <span class="atag purple ltr" :title="`↑ ${bytes(i.upBytes || 0, store.locale)}  ↓ ${bytes(i.downBytes || 0, store.locale)}`">
              {{ bytes(i.usedBytes, store.locale) }} / <span class="infinity">∞</span>
            </span>
          </td>
          <td class="center"><span class="atag speed-tag ltr" :class="{ blue: speedActive(i) }">{{ speedActive(i) ? speedText(i) : '—' }}</span></td>
          <td class="center"><span class="atag purple"><span class="infinity">∞</span></span></td>
        </tr>
      </tbody>
    </table>
    <slot />
  </div>
</template>
