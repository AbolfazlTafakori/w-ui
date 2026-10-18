<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { store, t, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import UpdateDialog from '../components/overview/UpdateDialog.vue'
import { useUpdate } from '../lib/update.js'
import OverviewVitals from '../components/overview/OverviewVitals.vue'
import OverviewStrip from '../components/overview/OverviewStrip.vue'
import LogsDialog from '../components/overview/LogsDialog.vue'
import BackupDialog from '../components/overview/BackupDialog.vue'
import HistoryDialog from '../components/overview/HistoryDialog.vue'

const data = ref(null)
// Which of the page's dialogs is open.
const logsOpen = ref(false)
const backupOpen = ref(false)
const historyOpen = ref(false)
const loading = ref(true)
let timer = null

// loadError is only shown when there is nothing to show instead. A refresh
// that fails while the page already holds figures should not replace them with
// an error: stale numbers with a warning are more use than none.
const loadError = ref(null)

async function load(quiet = false) {
  try {
    data.value = await api.fullOverview({ background: quiet })
    loadError.value = null
  } catch (err) {
    loadError.value = err
    if (!quiet) notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await load()
  // The server samples on its own ticker and keeps the history; polling only
  // pulls the latest window. Failures stay quiet so a brief blip does not stack
  // toasts on an unattended dashboard.
  timer = setInterval(() => load(true), 3000)
})
onUnmounted(() => clearInterval(timer))

const sys = computed(() => data.value?.system)

// A first load only: the three-second refresh must never put a skeleton over a
// page that already has numbers on it.
//
// Declared after `sys`, not before it: useDelayed watches with `immediate`, so
// it reads the source during setup -- placed any earlier it reaches a `const`
// that does not exist yet and the whole page fails to render.
const showSkeleton = useDelayed(computed(() => loading.value && !sys.value))
const panel = computed(() => data.value?.panel)
const clients = computed(() => data.value?.clients)

// The tiles an operator acts on, in the order they matter. The ones that mean
// work sit first; the reassuring totals sit last.
const customerTiles = computed(() => {
  const c = clients.value
  if (!c) return []
  return [
    { key: 'online', label: t('overview.onlineNow'), value: c.online, tone: 'ok', to: '/clients?status=online' },
    { key: 'depleting', label: t('stat.depleting'), value: c.depleting, tone: 'warn',
      urgent: true, to: '/clients?status=depleting' },
    { key: 'exhausted', label: t('status.exhausted'), value: c.exhausted, tone: 'bad',
      urgent: true, to: '/clients?status=exhausted' },
    { key: 'expired', label: t('status.expired'), value: c.expired, tone: 'bad',
      urgent: true, to: '/clients?status=expired' },
    { key: 'active', label: t('status.active'), value: c.active, tone: 'ok', to: '/clients?status=active' },
    { key: 'total', label: t('nav.clients'), value: c.clients, tone: '', to: '/clients' },
  ]
})
const ifaces = computed(() => data.value?.interfaces || [])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

const poolTotals = computed(() => ({
  allocated: ifaces.value.reduce((a, i) => a + (i.allocated || 0), 0),
  capacity: ifaces.value.reduce((a, i) => a + (i.capacity || 0), 0),
}))

// How much of this server is actually carrying traffic, which is the state an
// operator wants at a glance and the thing the two controls below act on.
const tunnelsUp = computed(() => ifaces.value.filter((i) => i.enabled && i.running).length)

const planeBusy = ref(false)
const confirmStop = ref(false)

async function tunnelAction(path, message) {
  planeBusy.value = true
  try {
    const res = await api.post(`/api/tunnels/${path}`)
    // A tunnel that would not come up is the whole content of the answer, so it
    // is said rather than folded into a count that looks like success.
    const failed = Object.entries(res?.failures || {})
    if (failed.length) {
      notify(failed.map(([name, why]) => `${name}: ${why}`).join('\n'), 'error')
    } else {
      notify(message.replace('{n}', res?.interfaces ?? 0), 'ok')
    }
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    planeBusy.value = false
  }
}

const restartTunnels = () => tunnelAction('restart', t('overview.restartedAll'))
const startTunnels = () => tunnelAction('start', t('overview.startedAll'))

function stopTunnels() {
  confirmStop.value = false
  return tunnelAction('stop', t('overview.stoppedAll'))
}

// v2.3.0 however the build was stamped; a development build says so as is.
const versionLabel = computed(() => {
  const v = String(panel.value?.version || '')
  return /^v?[0-9]/.test(v) ? 'v' + v.replace(/^v/, '') : v
})
// The update: offered in the header, followed in its dialog.
const { update, updateOpen, installing, openUpdate } = useUpdate(computed(() => panel.value?.version))

</script>

<template>
  <!-- The shape of the page while it is still arriving, rather than a spinner
       where the page will be. Only once the load has run long enough to be
       worth admitting to: this reading refreshes every three seconds, and a
       skeleton that flashed on each of them would be unusable. -->
  <PageSpin v-if="showSkeleton" />

  <div v-else-if="loading" class="ov-page"></div>

  <ErrorState v-else-if="loadError && !sys" :error="loadError" @retry="load()" />

  <div v-else-if="sys" class="ov-page">
    <!-- Laid out as the classic panel's overview bar: at the start, what this
         server is doing and which version it runs, and beside it the newer
         release when there is one; at the end, the things an operator reaches
         for while looking at this page. The page's name is for screen readers:
         the sidebar already says where you are. -->
    <div class="ov-actionbar">
      <h1 class="sr-only">{{ t('nav.overview') }}</h1>
      <div class="ov-state">
        <span class="ov-state-pill" :class="tunnelsUp ? 'up' : 'down'" :title="t('overview.tunnelsHint')">
          <i class="ov-state-dot"></i>
          <span>{{ t('overview.tunnels') }} · {{ tunnelsUp }} / {{ ifaces.length }}</span>
          <button type="button" class="ov-version ltr" :title="t('update.check')" @click="openUpdate">{{ versionLabel }}</button>
        </span>
        <!-- An install under way, or a newer release to install. Only an
             operator who can install it is told: the check is theirs alone. -->
        <button v-if="installing && installing.stage !== 'failed'" type="button" class="ov-update" @click="updateOpen = true">
          <span class="spin sm"></span>
          <span>{{ t('update.updating') }}</span>
        </button>
        <button v-else-if="update?.available && update?.latest" type="button" class="ov-update" @click="openUpdate">
          <AntIcon name="CloudUploadOutlined" />
          <span>{{ t('update.cta') }} <bdi class="ltr">v{{ update.latest }}</bdi></span>
        </button>
      </div>
      <div class="spacer row ov-actions">
        <button class="btn sm ghost" :title="t('overview.restartAllHint')"
                :disabled="planeBusy || !ifaces.length" @click="restartTunnels">
          <Icon name="refresh" :size="14" /><span class="lbl">{{ t('overview.restartAll') }}</span>
        </button>
        <button v-if="tunnelsUp" class="btn sm ghost" :title="t('overview.stopAllHint')"
                :disabled="planeBusy" @click="confirmStop = true">
          <Icon name="power" :size="14" /><span class="lbl">{{ t('overview.stopAll') }}</span>
        </button>
        <button v-else-if="ifaces.length" class="btn sm ghost" :title="t('overview.startAllHint')"
                :disabled="planeBusy" @click="startTunnels">
          <Icon name="power" :size="14" /><span class="lbl">{{ t('overview.startAll') }}</span>
        </button>

        <button class="btn sm ghost" :title="t('history.hint')" @click="historyOpen = true">
          <Icon name="clock" :size="14" /><span class="lbl">{{ t('history.title') }}</span>
        </button>
        <button class="btn sm ghost" :title="t('overview.viewLogs')" @click="logsOpen = true">
          <Icon name="info" :size="14" /><span class="lbl">{{ t('settings.tab.logs') }}</span>
        </button>
        <button class="btn sm ghost" :title="t('overview.backupRestore')" @click="backupOpen = true">
          <Icon name="database" :size="14" /><span class="lbl">{{ t('overview.backupRestore') }}</span>
        </button>
        <!-- The system report is reached from the page about this server. It
             was in the settings menu, which is for settings. -->
        <RouterLink to="/settings/system" class="btn sm ghost" :title="t('settings.tab.system')">
          <Icon name="server" :size="14" /><span class="lbl">{{ t('settings.tab.system') }}</span>
        </RouterLink>
        <RouterLink to="/settings" class="btn sm ghost" :title="t('nav.settings')">
          <Icon name="settings" :size="14" /><span class="lbl">{{ t('nav.settings') }}</span>
        </RouterLink>
      </div>
    </div>

    <div v-if="!panel.enforcementActive" class="ov-health">
      <Icon name="alert" :size="16" />
      <span>{{ panel.enforcementMessage }}</span>
    </div>

    <!-- Customers first. This is the page an operator lands on, and the
         question they arrive with is whether anything needs doing today - not
         how the machine's memory is. Each tile leads to the list already
         filtered, so noticing a problem and acting on it is one click. -->
    <div v-if="clients" class="ov-customers">
      <RouterLink
        v-for="tile in customerTiles"
        :key="tile.key"
        :to="tile.to"
        class="card ov-cust"
        :class="{ quiet: !tile.value, urgent: tile.urgent && tile.value }"
      >
        <span class="ov-cust-label"><i class="dot" :class="tile.tone"></i>{{ tile.label }}</span>
        <strong class="ov-cust-value num ltr">{{ nf(tile.value) }}</strong>
      </RouterLink>
    </div>

    <hr class="ov-rule" />

    <!-- Four vitals, each a number read against its own recent history. -->
    <OverviewVitals :system="sys" />

    <!-- The strip the classic panel closes its overview with: uptime, panel, addresses. -->
    <OverviewStrip :system="sys" :panel="panel" />

    <!-- What this panel manages, which the classic panel puts under Inbounds instead. -->
    <article class="card">
      <div class="card-head">
        <h2>{{ t('nav.interfaces') }}</h2>
        <span class="muted small ltr num">
          {{ nf(poolTotals.allocated) }} / {{ nf(poolTotals.capacity) }}
        </span>
        <RouterLink to="/clients" class="spacer small">
          {{ t('nav.clients') }}: {{ nf(clients.active) }} / {{ nf(clients.clients) }}
        </RouterLink>
      </div>

      <div v-if="!ifaces.length" class="empty">
        <p>{{ t('interface.noneYet') }}</p>
        <RouterLink to="/interfaces" class="btn primary sm">
          <Icon name="plus" :size="14" />{{ t('interface.create') }}
        </RouterLink>
      </div>

      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{{ t('interface.name') }}</th>
              <th>{{ t('client.protocol') }}</th>
              <th>{{ t('interface.endpoint') }}</th>
              <th>{{ t('interface.mode') }}</th>
              <th>{{ t('interface.subnet') }}</th>
              <th style="min-width: 190px">{{ t('interface.capacity') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="i in ifaces" :key="i.id">
              <td class="mono">{{ i.name }}</td>
              <td><span class="tag proto">{{ i.protocol }}</span></td>
              <td class="mono small">{{ i.endpointHost }}:{{ i.listenPort }}</td>
              <td>
                <span v-if="i.mode === 'amnezia'" class="tag active">
                  <Icon name="shield" :size="12" />AmneziaWG
                </span>
                <span v-else class="muted small">{{ t('interface.mode.standard') }}</span>
              </td>
              <td class="mono small">{{ i.subnet }}</td>
              <td>
                <div class="row">
                  <div class="meter" style="flex: 1">
                    <span
                      :class="i.capacity && i.allocated / i.capacity > 0.9 ? 'bad' : ''"
                      :style="{ width: Math.max((i.allocated / (i.capacity || 1)) * 100, 1) + '%' }"
                    ></span>
                  </div>
                  <span class="num small muted ltr">
                    {{ nf(i.allocated) }} / {{ nf(i.capacity) }}
                  </span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </article>
  </div>

  <!-- Stopping every tunnel takes every customer offline at once, so it is
       asked for. Restarting is not: it changes no records and the worst case is
       a few seconds of reconnecting. -->
  <ConfirmDialog
    :open="confirmStop"
    :title="t('overview.stopAllTitle')"
    :body="t('overview.stopAllBody')"
    :confirm-label="t('overview.stopAll')"
    :danger="true"
    :busy="planeBusy"
    @confirm="stopTunnels"
    @cancel="confirmStop = false"
  />

  <UpdateDialog v-if="updateOpen" :current="panel?.version || ''" />
  <LogsDialog v-if="logsOpen" @close="logsOpen = false" />
  <BackupDialog v-if="backupOpen" @close="backupOpen = false" />
  <HistoryDialog v-if="historyOpen" @close="historyOpen = false" />
</template>

<style scoped>
.ov-page {
  --ov-gap: 12px;
  display: flex;
  flex-direction: column;
  gap: var(--ov-gap);
}
.ov-actionbar {
  display: flex;
  align-items: center;
  gap: 12px 16px;
  flex-wrap: wrap;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--line);
}
.ov-state {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
/* "● Tunnels · 3 / 3  v2.3.0": the state and the version in one pill. */
.ov-state-pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--ink);
  font-size: var(--t-sm);
  white-space: nowrap;
}
.ov-state-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ok);
  flex: none;
}
.ov-state-pill.down .ov-state-dot { background: var(--bad); }
.ov-state-pill .ov-version { font-size: var(--t-sm); color: var(--muted); }
.ov-health {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 11px 15px;
  border-radius: var(--radius-sm);
  background: var(--warn-soft);
  border: 1px solid rgba(224, 171, 52, 0.4);
  font-size: var(--t-sm);
  color: var(--ink);
}
.ov-health svg {
  color: var(--warn);
  flex-shrink: 0;
  margin-top: 2px;
}
.ov-rule {
  border: none;
  border-top: 1px solid var(--line);
  margin: 0;
}
@media (max-width: 768px) {
  /* IndexPage.css: the action bar's buttons take the whole width, spread. */
  .ov-actionbar .ov-actions {
    margin-inline-start: 0;
    width: 100%;
    justify-content: space-between;
  }
  .ov-page { --ov-gap: 8px; }
}
</style>
