<script setup>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { store, t, tn, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import PageSpin from '../components/PageSpin.vue'

// Credentials seen from several places at once. Ours, not 3x-ui's, so it
// is laid out the way its list pages are: a summary card of three figures,
// then a card whose title holds the actions and the search, and a small
// table with sortable columns and a menu column at the front.

const router = useRouter()
const reports = ref([])
const loading = ref(true)
const loadError = ref(null)
const showWait = useDelayed(computed(() => loading.value && !reports.value.length))
let timer = null

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

onMounted(() => {
  load()
  // The window this looks at is ten minutes wide, so a slow refresh is enough
  // and a fast one would only add load for no new information.
  timer = setInterval(() => load(true), 30_000)
})
onBeforeUnmount(() => clearInterval(timer))

async function load(quiet = false) {
  try {
    reports.value = await api.get('/api/sharing', { background: quiet })
    loadError.value = null
  } catch (e) {
    loadError.value = e
    if (!quiet) notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}

function since(iso) {
  if (!iso) return '—'
  const mins = Math.round((Date.now() - new Date(iso)) / 60000)
  if (mins < 60) return tn('sharing.minutesAgo', mins)
  const hours = Math.round(mins / 60)
  if (hours < 24) return tn('sharing.hoursAgo', hours)
  return tn('sharing.daysAgo', Math.round(hours / 24))
}

const totals = computed(() => ({
  devices: reports.value.length,
  clients: new Set(reports.value.map((r) => r.clientId)).size,
  addrs: reports.value.reduce((a, r) => a + (r.addrs?.length || 0), 0),
}))

// Their search box beside the buttons: customer, device, address.
const search = ref('')
const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return reports.value
  return reports.value.filter((r) =>
    [r.clientName, r.deviceName, ...(r.addrs || [])].some((v) => String(v || '').toLowerCase().includes(q)),
  )
})

// Their sortable headers: ascending, descending, then the server's order.
const sort = ref({ key: '', dir: '' })
const cmpText = (a, b) => String(a || '').localeCompare(String(b || ''), undefined, { numeric: true, sensitivity: 'base' })
const sorters = {
  client: (a, b) => cmpText(a.clientName, b.clientName),
  device: (a, b) => cmpText(a.deviceName, b.deviceName),
  addrs: (a, b) => (a.addrs?.length || 0) - (b.addrs?.length || 0),
  since: (a, b) => new Date(a.since || 0) - new Date(b.since || 0),
}
function sortCls(key) {
  return sort.value.key === key ? `sorted ${sort.value.dir}` : ''
}
function sortBy(key) {
  const cur = sort.value
  if (cur.key !== key) sort.value = { key, dir: 'asc' }
  else if (cur.dir === 'asc') sort.value = { key, dir: 'desc' }
  else sort.value = { key: '', dir: '' }
}
const sorted = computed(() => {
  const { key, dir } = sort.value
  if (!key || !sorters[key]) return visible.value
  const rows = [...visible.value].sort(sorters[key])
  return dir === 'desc' ? rows.reverse() : rows
})

function open(r) {
  router.push(`/clients/${r.clientId}`)
}
</script>

<template>
  <div class="antpage sharing">
    <!-- The summary Card: size="small", three Statistics. -->
    <div class="acard small summary-card">
      <div class="acard-body">
        <div class="arow">
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.devices') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="EyeOutlined" /></span><span>{{ nf(totals.devices) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.clients') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(totals.clients) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('sharing.stat.addrs') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="GlobalOutlined" /></span><span>{{ nf(totals.addrs) }}</span></div>
          </div>
        </div>
      </div>
    </div>

    <!-- The list Card: the title is a Space of Refresh and the search. -->
    <div class="acard">
      <div class="acard-head">
        <div class="aspace">
          <button class="abtn primary" @click="load()">
            <AntIcon name="ReloadOutlined" /><span>{{ t('common.refresh') }}</span>
          </button>
          <label class="ainput">
            <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
            <input v-model="search" type="text" :placeholder="t('iface.menu.search')" :aria-label="t('iface.menu.search')" />
            <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.cancel')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
          </label>
        </div>
      </div>

      <div class="acard-body">
        <!-- Said plainly and up front, because acting on this without reading
             it means disconnecting paying customers who did nothing wrong. -->
        <div class="aalert warning with-desc" role="note">
          <AntIcon name="ExclamationCircleFilled" />
          <div class="aalert-body">
            <span class="aalert-title">{{ t('sharing.readFirst') }}</span>
            <span class="aalert-desc">{{ t('sharing.caveat') }}</span>
          </div>
        </div>

        <ErrorState v-if="loadError && !reports.length" :error="loadError" @retry="load()" />
        <PageSpin v-else-if="showWait" />
        <div v-else-if="loading" class="empty"></div>

        <div v-else class="atable-wrap">
          <table class="atable small" style="min-width: 760px">
            <thead>
              <tr>
                <th class="w-menu center">{{ t('iface.col.menu') }}</th>
                <th class="w-client sortable" :class="sortCls('client')" @click="sortBy('client')">
                  <div class="sorters"><span class="title">{{ t('sharing.client') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-device sortable" :class="sortCls('device')" @click="sortBy('device')">
                  <div class="sorters"><span class="title">{{ t('sharing.device') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="sortable" :class="sortCls('addrs')" @click="sortBy('addrs')">
                  <div class="sorters"><span class="title">{{ t('sharing.addresses') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
                <th class="w-since center sortable" :class="sortCls('since')" @click="sortBy('since')">
                  <div class="sorters"><span class="title">{{ t('sharing.firstSeen') }}</span><span class="sorter"><AntIcon name="CaretUpFilled" class="up" /><AntIcon name="CaretDownFilled" class="down" /></span></div>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!sorted.length" class="empty-row">
                <td colspan="5">
                  <div class="card-empty">
                    <AntIcon name="CheckCircleFilled" :size="32" />
                    <div>{{ t('sharing.none') }}</div>
                    <div class="muted">{{ t('sharing.noneHint') }}</div>
                  </div>
                </td>
              </tr>
              <tr v-for="r in sorted" :key="r.accountId">
                <td class="center">
                  <div class="action-buttons">
                    <button class="abtn text sm" :title="t('sharing.review')" :aria-label="t('sharing.review')" @click="open(r)"><AntIcon name="EyeOutlined" /></button>
                  </div>
                </td>
                <td><RouterLink :to="`/clients/${r.clientId}`" class="cell-link">{{ r.clientName }}</RouterLink></td>
                <td class="muted">{{ r.deviceName }}</td>
                <td>
                  <div class="protocol-tags">
                    <span v-for="a in r.addrs" :key="a" class="atag blue ltr" style="margin: 0">{{ a }}</span>
                  </div>
                </td>
                <td class="center"><span class="atag ltr" style="margin: 0">{{ since(r.since) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.aalert.with-desc { margin-bottom: 10px; }
.w-menu { width: 70px; }
.w-client { width: 200px; }
.w-device { width: 160px; }
.w-since { width: 120px; }
.atable th.center, .atable td.center { text-align: center; }
.action-buttons { display: flex; align-items: center; justify-content: center; gap: 4px; }
.card-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 24px 12px;
  color: var(--muted);
}
.card-empty .anticon { margin-bottom: 8px; color: var(--ok); }
.cell-link { color: var(--ink); text-decoration: none; }
.cell-link:hover { color: var(--accent); }
.muted { color: var(--faint); }
</style>
