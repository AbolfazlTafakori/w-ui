<script setup>
// The counts over the customer list: everyone, online, ended, running low,
// switched off, active.
import { store, t } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'

defineProps({ stats: { type: Object, required: true } })
const nf = (n) => Number(n || 0).toLocaleString(store.locale)
</script>

<template>
  <div class="acard small summary-card">
    <div class="acard-body">
      <div class="arow six">
        <div class="acol">
          <div class="stat-title">{{ t('nav.clients') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(stats.clients) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('status.online') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-blue"></i></span><span>{{ nf(stats.online) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('stat.depleted') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-red"></i></span><span>{{ nf((stats.exhausted || 0) + (stats.expired || 0)) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('stat.depleting') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-orange"></i></span><span>{{ nf(stats.depleting) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('status.disabled') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-gray"></i></span><span>{{ nf(stats.disabled) }}</span></div>
        </div>
        <div class="acol">
          <div class="stat-title">{{ t('status.active') }}</div>
          <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-green"></i></span><span>{{ nf(stats.active) }}</span></div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.arow.six > .acol { flex: 0 0 16.6667%; max-width: 16.6667%; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 4px; vertical-align: middle; }
.dot-green { background: var(--ok); }
.dot-blue { background: var(--accent); }
.dot-red { background: var(--bad); }
.dot-orange { background: var(--warn); }
.dot-gray { background: var(--faint); }
</style>
