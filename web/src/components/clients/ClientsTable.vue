<script setup>
// The customer list as a table, a row each, for a screen wide enough.
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import {
  INBOUND_CHIP_LIMIT, barColor, expiryTag, expiryTitle, inboundChips, lastOnlineTitle, remainingTag, speedActive, speedText,
  statusTag, usedPercent,
} from '../../lib/clientRow.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

defineProps({
  // The customers on this page, and which are chosen.
  items: { type: Array, required: true },
  selected: { type: Object, required: true },
  allSelected: { type: Boolean, default: false },
  // Rows with a change in flight, whose switch spins.
  pending: { type: Object, required: true },
  interfaces: { type: Array, default: () => [] },
  // Whether any customer has a group, which adds that column.
  hasGroups: { type: Boolean, default: false },
  groupFilter: { type: String, default: '' },
  stale: { type: Boolean, default: false },
})
const emit = defineEmits(['toggle', 'toggle-all', 'qr', 'info', 'rotate', 'reset', 'edit', 'remove', 'enable', 'group'])
</script>

<template>
  <div class="atable-wrap" :class="{ stale }" style="margin-top: 0">
    <table class="atable small" style="min-width: 1200px">
      <thead>
        <tr>
          <th class="sel"><input type="checkbox" class="acheck" :checked="allSelected" :aria-label="t('action.selectAll')" @change="emit('toggle-all', $event.target.checked)" /></th>
          <th style="width: 236px">{{ t('table.actions') }}</th>
          <th style="width: 80px">{{ t('table.enabled') }}</th>
          <th style="width: 90px">{{ t('status.online') }}</th>
          <th style="width: 220px">{{ t('client.menu.client') }}</th>
          <th v-if="hasGroups" style="width: 130px">{{ t('client.groups') }}</th>
          <th style="width: 170px">{{ t('client.attachedInbounds') }}</th>
          <th style="width: 300px">{{ t('client.traffic') }}</th>
          <th class="center" style="width: 216px">{{ t('client.speed') }}</th>
          <th style="width: 130px">{{ t('client.remaining') }}</th>
          <th style="width: 130px">{{ t('client.menu.duration') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!items.length" class="empty-row">
          <td :colspan="hasGroups ? 11 : 10">
            <div class="clients-empty">
              <AntIcon name="TeamOutlined" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="c in items" :key="c.id" :class="{ picked: selected.has(c.id) }">
          <td class="sel"><input type="checkbox" class="acheck" :checked="selected.has(c.id)" :aria-label="c.name" @change="emit('toggle', c.id, $event.target.checked)" /></td>
          <td>
            <div class="aspace" style="gap: 4px; flex-wrap: nowrap">
              <button class="abtn text sm" :title="t('client.qrCode')" :aria-label="t('client.qrCode')" @click="emit('qr', c)"><AntIcon name="QrcodeOutlined" /></button>
              <button class="abtn text sm" :title="t('client.menu.clientInfo')" :aria-label="t('client.menu.clientInfo')" @click="emit('info', c)"><AntIcon name="InfoCircleOutlined" /></button>
              <button class="abtn text sm" :title="t('client.rotateKeys')" :aria-label="t('client.rotateKeys')" :disabled="pending.has(c.id)" @click="emit('rotate', c)"><AntIcon name="KeyOutlined" /></button>
              <button class="abtn text sm" :title="t('outbound.resetTraffic')" :aria-label="t('outbound.resetTraffic')" :disabled="pending.has(c.id)" @click="emit('reset', c)"><AntIcon name="RetweetOutlined" /></button>
              <button class="abtn text sm" :title="t('action.edit')" :aria-label="t('action.edit')" @click="emit('edit', c)"><AntIcon name="EditOutlined" /></button>
              <button class="abtn text sm danger" :title="t('action.delete')" :aria-label="t('action.delete')" @click="emit('remove', c)"><AntIcon name="DeleteOutlined" /></button>
            </div>
          </td>
          <td>
            <span :title="c.ownerPaused ? statusTag(c).title : ''" class="toggle-wrap"><Toggle :model-value="c.status === 'active' && !c.ownerPaused" :label="c.name" small :disabled="c.status === 'expired' || c.status === 'exhausted' || !!c.ownerPaused" :loading="pending.has(c.id)" @update:model-value="(v) => emit('enable', c, v)" /></span>
          </td>
          <td>
            <span class="atag" :class="statusTag(c).color === 'grey' ? '' : statusTag(c).color" :title="statusTag(c).title || lastOnlineTitle(c)" style="margin: 0">
              <i v-if="statusTag(c).dot" class="online-dot"></i>{{ statusTag(c).label }}
            </span>
          </td>
          <td>
            <div class="email-cell">
              <!-- The name opens the customer's own page: their files one
                   by one, each with its configuration and QR, and files
                   added or removed beyond the one per user. -->
              <router-link class="email" :to="`/clients/${c.id}`" :title="t('client.openPage')">{{ c.name }}</router-link>
              <!-- Connections in use right now against what the plan allows at once,
                   the way the classic panel shows its IP count: a file on two devices
                   used one at a time is one connection; both at once is two. -->
              <span class="sub ltr" :class="{ over: c.deviceLimit > 0 && (c.onlineNow || 0) > c.deviceLimit }"
                    :title="t('client.connectionsNow', { n: c.onlineNow || 0, limit: c.deviceLimit, files: c.accounts?.length ?? 0 })">
                {{ c.onlineNow || 0 }} / {{ c.deviceLimit || '∞' }}
              </span>
              <span v-if="c.note" class="sub" :title="c.note">{{ c.note }}</span>
            </div>
          </td>
          <td v-if="hasGroups">
            <!-- As the inbounds cell: the first group as a chip, the rest
                 folded into a +N that names them on hover, so a customer
                 in five groups is still one row high. -->
            <template v-if="(c.groups || []).length">
              <span v-for="g in c.groups.slice(0, INBOUND_CHIP_LIMIT)" :key="g" class="atag geekblue" :style="{ margin: '2px', cursor: 'pointer', opacity: groupFilter === g ? 0.6 : 1 }" :title="g" @click="emit('group', g)">{{ g }}</span>
              <span v-if="c.groups.length > INBOUND_CHIP_LIMIT" class="atag default" style="margin: 2px; cursor: pointer" :title="c.groups.slice(INBOUND_CHIP_LIMIT).join(', ')" @click="emit('group', c.groups[INBOUND_CHIP_LIMIT])">+{{ c.groups.length - INBOUND_CHIP_LIMIT }}</span>
            </template>
            <span v-else class="cell-empty">—</span>
          </td>
          <td>
            <template v-if="inboundChips(c, interfaces).length">
              <span v-for="ib in inboundChips(c, interfaces).slice(0, INBOUND_CHIP_LIMIT)" :key="ib.name" class="atag" :class="{ openvpn: 'orange', l2tp: 'purple' }[ib.protocol] || 'gold'" style="margin: 2px" :title="ib.name">{{ ib.name }}</span>
              <span v-if="inboundChips(c, interfaces).length > INBOUND_CHIP_LIMIT" class="atag default" style="margin: 2px; cursor: pointer" :title="inboundChips(c, interfaces).slice(INBOUND_CHIP_LIMIT).map((x) => x.name).join(', ')">+{{ inboundChips(c, interfaces).length - INBOUND_CHIP_LIMIT }}</span>
            </template>
            <span v-else class="cell-empty">—</span>
          </td>
          <td>
            <div class="client-traffic-cell" :class="{ 'is-unlimited': !c.quotaBytes }" :title="`↑ ${bytes(c.upBytes || 0, store.locale)}  ↓ ${bytes(c.downBytes || 0, store.locale)}`">
              <span class="client-traffic-cell-used ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
              <span class="aprogress client-traffic-cell-bar"><span :style="{ width: (c.quotaBytes ? Math.min(100, usedPercent(c) ?? 0) : 100) + '%', background: barColor(c) }"></span></span>
              <span class="client-traffic-cell-limit ltr">
                <span v-if="!c.quotaBytes" class="client-traffic-cell-infinity">∞</span>
                <template v-else>{{ bytes(c.quotaBytes, store.locale) }}</template>
              </span>
            </div>
          </td>
          <td class="center"><span class="atag speed-tag ltr" :class="{ blue: speedActive(c) }">{{ speedActive(c) ? speedText(c) : '—' }}</span></td>
          <td><span class="atag ltr" :class="remainingTag(c).color" style="margin: 0">{{ remainingTag(c).label }}</span></td>
          <td><span class="atag ltr" :class="expiryTag(c).color" :title="expiryTitle(c)" style="margin: 0">{{ expiryTag(c).label }}</span></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.atable-wrap.stale { opacity: 0.6; }
.email-cell { display: flex; flex-direction: column; }
.email-cell .email { font-weight: 500; color: inherit; text-decoration: none; }
.email-cell .sub.over { color: var(--danger, #e5484d); font-weight: 600; }
.email-cell .sub { font-size: 11px; opacity: 0.55; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 220px; }
.cell-empty { color: var(--faint); }
.speed-tag { display: inline-flex; width: 200px; align-items: center; justify-content: center; margin-inline-end: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; box-sizing: border-box; font-variant-numeric: tabular-nums; }
.clients-empty { padding: 32px 0; text-align: center; color: var(--muted); }
</style>
