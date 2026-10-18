<script setup>
// The resellers as a table: a box to select each, its actions and switch,
// who they are, where they stand, and what is left of what they bought.
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { CHIP_LIMIT, barColor, customersTag, standing, termTag, trafficLeftTag, usedPercent } from '../../lib/resellers/rows.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

const props = defineProps({
  rows: { type: Array, required: true },
  selected: { type: Set, required: true },
  pending: { type: Set, required: true },
  servers: { type: Array, required: true },
  allSelected: { type: Boolean, default: false },
  someSelected: { type: Boolean, default: false },
})
defineEmits(['toggle', 'toggle-all', 'edit', 'reset', 'remove', 'enable'])

const serverName = (id) => props.servers.find((s) => s.id === id)?.name || `#${id}`
const serverProto = (id) => props.servers.find((s) => s.id === id)?.protocol
</script>

<template>
  <div class="atable-wrap" style="margin-top: 0">
    <table class="atable small" style="min-width: 1080px">
      <thead>
        <tr>
          <th class="sel"><input type="checkbox" class="acheck" :checked="allSelected" :indeterminate="someSelected" :aria-label="t('action.selectAll')" @change="$emit('toggle-all', $event.target.checked)" /></th>
          <th style="width: 110px">{{ t('table.actions') }}</th>
          <th style="width: 70px">{{ t('table.enabled') }}</th>
          <th style="width: 200px">{{ t('admins.reseller') }}</th>
          <th style="width: 110px">{{ t('admins.status') }}</th>
          <th style="width: 100px">{{ t('admins.customers') }}</th>
          <th style="width: 260px">{{ t('client.traffic') }}</th>
          <th style="width: 110px">{{ t('admins.trafficLeft') }}</th>
          <th style="width: 110px">{{ t('admins.timeLeft') }}</th>
          <th>{{ t('admins.servers') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in rows" :key="a.id" :class="{ off: !a.enabled, picked: selected.has(a.id) }">
          <td class="sel keep"><input type="checkbox" class="acheck" :checked="selected.has(a.id)" :aria-label="a.username" @change="$emit('toggle', a.id, $event.target.checked)" /></td>
          <td class="keep">
            <div class="aspace" style="gap: 4px; flex-wrap: nowrap">
              <button class="abtn text sm" :title="t('action.edit')" :aria-label="t('action.edit')" @click="$emit('edit', a)"><AntIcon name="EditOutlined" /></button>
              <button v-if="a.role === 'reseller' && a.quotaBytes" class="abtn text sm" :title="t('admins.resetUsage')" :aria-label="t('admins.resetUsage')" :disabled="pending.has(a.id)" @click="$emit('reset', a)"><AntIcon name="RetweetOutlined" /></button>
              <button class="abtn text sm danger" :title="t('action.delete')" :aria-label="t('action.delete')" @click="$emit('remove', a)"><AntIcon name="DeleteOutlined" /></button>
            </div>
          </td>
          <td class="keep">
            <Toggle :model-value="a.enabled" :label="a.username" small :loading="pending.has(a.id)" @update:model-value="(v) => $emit('enable', a, v)" />
          </td>
          <td>
            <div class="who">
              <span class="name ltr">{{ a.username }}</span>
              <span v-if="a.role === 'admin'" class="sub"><span class="atag geekblue" style="margin: 0">{{ t('admins.role.admin') }}</span></span>
              <span v-if="a.note" class="sub" :title="a.note">{{ a.note }}</span>
              <span v-if="a.groupName" class="sub" :title="t('admins.groupHint')"><AntIcon name="TagsOutlined" /> {{ a.groupName }}</span>
            </div>
          </td>
          <td><span class="atag" dir="auto" :class="standing(a).color" style="margin: 0">{{ standing(a).label }}</span></td>
          <template v-if="a.role === 'reseller'">
            <td><span class="atag ltr" :class="customersTag(a).color" style="margin: 0">{{ customersTag(a).label }}</span></td>
            <td>
              <div class="traffic-cell" :class="{ 'is-unlimited': !a.quotaBytes }">
                <span class="traffic-used ltr">{{ bytes(a.usedBytes, store.locale) }}</span>
                <span class="aprogress traffic-bar"><span :style="{ width: (a.quotaBytes ? usedPercent(a) : 100) + '%', background: barColor(a) }"></span></span>
                <span class="traffic-limit ltr">
                  <span v-if="!a.quotaBytes" class="traffic-infinity">∞</span>
                  <template v-else>{{ bytes(a.quotaBytes, store.locale) }}</template>
                </span>
              </div>
            </td>
            <td><span class="atag ltr" :class="trafficLeftTag(a).color" style="margin: 0">{{ trafficLeftTag(a).label }}</span></td>
            <td><span class="atag" dir="auto" :class="termTag(a).color" :title="termTag(a).title" style="margin: 0">{{ termTag(a).label }}</span></td>
            <td>
              <template v-if="(a.interfaceIds || []).length">
                <span v-for="id in a.interfaceIds.slice(0, CHIP_LIMIT)" :key="id" class="atag" :class="serverProto(id) === 'openvpn' ? 'orange' : 'gold'" style="margin: 2px">{{ serverName(id) }}</span>
                <span v-if="a.interfaceIds.length > CHIP_LIMIT" class="atag default" style="margin: 2px" :title="a.interfaceIds.slice(CHIP_LIMIT).map(serverName).join(', ')">+{{ a.interfaceIds.length - CHIP_LIMIT }}</span>
              </template>
              <span v-else class="atag red" style="margin: 0" :title="t('admins.noServersWarn')">{{ t('admins.noServers') }}</span>
            </td>
          </template>
          <template v-else>
            <td colspan="5" class="cell-empty">{{ t('admins.role.adminHint') }}</td>
          </template>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
tr.off .who .name { opacity: 0.55; }
/* A switched-off row recedes, but its switch and its actions do not: they
   are how it is switched back on, edited or removed, and a faded button
   reads as one that cannot be pressed. */
tr.off td.keep { opacity: 1; }
.who { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.who .name { font-weight: 500; }
.who .sub { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 200px; }
.cell-empty { color: var(--faint); font-size: 12px; }

/* The customer list's traffic pill: the figures either side of a bar. */
.traffic-cell { display: flex; align-items: center; gap: 8px; width: 100%; min-width: 0; box-sizing: border-box; padding: 2px 10px; border-radius: 999px; background: var(--surface-2); }
.traffic-used, .traffic-limit { flex: 0 0 68px; min-width: 68px; font-size: 12px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.traffic-used { text-align: end; color: var(--ink); }
.traffic-limit { text-align: start; color: var(--muted); }
.traffic-bar { flex: 1 1 60px; min-width: 40px; }
.traffic-cell.is-unlimited .traffic-bar > span { border: 1px solid rgba(114, 46, 209, 0.55); }
.traffic-infinity { color: var(--tag-purple-ink); font-size: 14px; line-height: 1; }

/* The selection. */
.atable th.sel, .atable td.sel { width: 40px; text-align: center; }
tr.picked td { background: var(--accent-soft); }
</style>
