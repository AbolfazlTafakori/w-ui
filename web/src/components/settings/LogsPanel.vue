<script setup>
// The panel's recent log, from the settings page: the last two hundred
// lines, by level.
import { onMounted, ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'

onMounted(loadLogs)

const logs = ref([])
const logLevel = ref('')
const logsBusy = ref(false)
async function loadLogs() {
  logsBusy.value = true
  try {
    const res = await api.get(`/api/logs?limit=200&level=${logLevel.value}`)
    logs.value = res.entries || []
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    logsBusy.value = false
  }
}
function logTime(iso) {
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
}
function logFields(e) {
  if (!e.fields) return ''
  return Object.entries(e.fields)
    .map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : v}`)
    .join('  ')
}
</script>

<template>
  <div class="logs-panel">
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.recentLog') }}</div><div class="setting-list-description">{{ t('settings.recentLogDesc') }}</div></div></div>
      <div class="acol"><div class="aspace">
        <div class="aselect" style="width: 160px"><select v-model="logLevel" @change="loadLogs" :aria-label="t('settings.recentLog')">
          <option value="">{{ t('settings.logAll') }}</option>
          <option value="INFO">{{ t('settings.logInfo') }}</option>
          <option value="WARN">{{ t('settings.logWarn') }}</option>
          <option value="ERROR">{{ t('settings.logError') }}</option>
        </select></div>
        <button class="abtn" :disabled="logsBusy" @click="loadLogs"><AntIcon name="ReloadOutlined" /><span>{{ t('common.refresh') }}</span></button>
      </div></div>
    </div></div>
    <div class="log-view">
      <p v-if="!logs.length" class="muted">{{ t('settings.logEmpty') }}</p>
      <ol v-else class="log-lines">
        <li v-for="(e, i) in logs" :key="i" :class="`lvl-${(e.level || '').toLowerCase()}`">
          <span class="log-time ltr">{{ logTime(e.time) }}</span>
          <span class="log-level ltr">{{ e.level }}</span>
          <span class="log-msg">{{ e.message }}</span>
          <span v-if="logFields(e)" class="log-fields ltr">{{ logFields(e) }}</span>
        </li>
      </ol>
    </div>
  </div>
</template>

<style scoped>
.log-view { padding: 10px 20px; }
</style>
