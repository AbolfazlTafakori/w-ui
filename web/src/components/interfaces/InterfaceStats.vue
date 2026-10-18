<script setup>
// The strip over the tunnel list: what the whole set has carried each way,
// in all, and how many tunnels there are.
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import AntIcon from '../AntIcon.vue'

defineProps({ totals: { type: Object, required: true } })
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
</script>

<template>
  <div class="acard small summary-card">
    <div class="acard-body">
      <div class="arow">
        <div class="acol">
          <div class="stat-title">{{ t('iface.stat.totalDownUp') }}</div>
          <div class="stat-content ltr">
            <span><AntIcon name="ArrowUpOutlined" /> {{ bytes(totals.up, store.locale) }} / <AntIcon name="ArrowDownOutlined" /> {{ bytes(totals.down, store.locale) }}</span>
          </div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('iface.stat.totalUsage') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="PieChartOutlined" /></span><span>{{ bytes(totals.used, store.locale) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('iface.stat.count') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="BarsOutlined" /></span><span>{{ nf(totals.count) }}</span></div>
        </div>
      </div>
    </div>
  </div>
</template>
