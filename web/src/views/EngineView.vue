<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import ErrorState from '../components/ErrorState.vue'
import Toggle from '../components/Toggle.vue'
import TagInput from '../components/TagInput.vue'
import MultiSelect from '../components/MultiSelect.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

// The engine pages: what 3x-ui keeps under "Xray" -- the core's own
// settings, its balancers, its DNS and the raw template -- laid out the
// same way, for the kernel tunnels this panel runs instead of a core.

const router = useRouter()
const props = defineProps({ tab: { type: String, default: '' } })

const SECTIONS = ['basic', 'balancer', 'dns', 'advanced']
function known(slug) {
  return SECTIONS.includes(slug) ? slug : ''
}
function fromHash() {
  return known((location.hash || '').replace(/^#/, '')) || 'basic'
}
const active = ref(known(props.tab) || fromHash())
watch(
  () => props.tab,
  (v) => {
    const next = known(v)
    if (next && next !== active.value) active.value = next
  },
)
window.addEventListener('hashchange', () => {
  if (!props.tab) active.value = fromHash()
})
const inner = ref({ basic: '1', balancer: 'balancers', dns: '1' })

// ── the settings ──
const loading = ref(true)
const loadError = ref(null)
const saved = ref(null)
const defaults = ref(null)
const form = ref(null)
const dnsListening = ref([])
const busy = ref(false)
const showWait = useDelayed(computed(() => loading.value && !form.value))

const dirty = computed(() => !!form.value && !!saved.value && JSON.stringify(form.value) !== JSON.stringify(saved.value))

function take(res) {
  saved.value = res.settings
  defaults.value = res.defaults
  form.value = JSON.parse(JSON.stringify(res.settings))
  dnsListening.value = res.dnsListening || []
}

async function load() {
  loading.value = true
  try {
    take(await api.get('/api/engine'))
    loadError.value = null
  } catch (e) {
    loadError.value = e
  } finally {
    loading.value = false
  }
  loadBalancers()
  loadOutbounds()
}

async function save() {
  busy.value = true
  try {
    take(await api.put('/api/engine', form.value))
    notify(t('eng.saved'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

const askReset = ref(false)
async function resetDefaults() {
  askReset.value = false
  busy.value = true
  try {
    take(await api.post('/api/engine/reset'))
    notify(t('eng.resetDone'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

// Leaving with an unsaved edit asks, as the settings page does.
const pendingRoute = ref(null)
const leaving = ref(false)
onBeforeRouteLeave((to) => {
  if (!dirty.value || leaving.value) return true
  pendingRoute.value = to.fullPath
  return false
})
async function discardAndGo() {
  leaving.value = true
  const to = pendingRoute.value
  pendingRoute.value = null
  await router.push(to)
  leaving.value = false
}
async function saveAndGo() {
  await save()
  if (!dirty.value) await discardAndGo()
  else pendingRoute.value = null
}

function isDefault(key) {
  return !!form.value && !!defaults.value && form.value[key] === defaults.value[key]
}

// ── balancers ──
const balancers = ref([])
const outbounds = ref([])
const liveLoading = ref(false)
async function loadBalancers(quiet = true) {
  liveLoading.value = true
  try {
    balancers.value = await api.get('/api/balancers', { background: quiet })
  } catch {
    /* the page still shows the rest */
  } finally {
    liveLoading.value = false
  }
}
async function loadOutbounds() {
  try {
    outbounds.value = await api.get('/api/outbounds', { background: true })
  } catch {
    outbounds.value = []
  }
}
// Only hops can be balanced over: the built-ins have no device.
const hopTags = computed(() => outbounds.value.filter((o) => !o.builtin).map((o) => o.tag))
const memberOptions = computed(() =>
  outbounds.value
    .filter((o) => !o.builtin)
    .map((o) => ({ value: o.tag, label: o.tag, tags: [{ text: t(`outbound.kind.${o.kind}`), kind: 'green' }] })),
)
const watched = computed(() => [...new Set(balancers.value.flatMap((b) => b.memberList || []))])
const STRATEGY_LABELS = { random: 'Random', leastPing: 'Least Ping' }

const balancerModal = ref(null) // { id?, tag, strategy, members, fallback, note, enabled }
const bError = ref('')
function openAddBalancer() {
  bError.value = ''
  balancerModal.value = { tag: '', strategy: 'random', members: [], fallback: '', note: '', enabled: true }
}
function openEditBalancer(b) {
  bError.value = ''
  balancerModal.value = { id: b.id, tag: b.tag, strategy: b.strategy, members: [...(b.memberList || [])], fallback: b.fallback || '', note: b.note || '', enabled: b.enabled }
}
async function submitBalancer() {
  const m = balancerModal.value
  busy.value = true
  bError.value = ''
  try {
    if (m.id) await api.patch(`/api/balancers/${m.id}`, m)
    else await api.post('/api/balancers', m)
    balancerModal.value = null
    await loadBalancers()
  } catch (e) {
    bError.value = e.message
  } finally {
    busy.value = false
  }
}
const askDeleteBalancer = ref(null)
async function deleteBalancer() {
  const b = askDeleteBalancer.value
  askDeleteBalancer.value = null
  try {
    await api.del(`/api/balancers/${b.id}`)
    await loadBalancers()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function setOverride(b, target) {
  try {
    await api.post(`/api/balancers/${b.id}/override`, { target })
    await loadBalancers()
  } catch (e) {
    notify(e.message, 'error')
  }
}
const rowMenu = ref(null) // { b, x, y }
function openRowMenu(b, e) {
  if (rowMenu.value?.b.id === b.id) {
    rowMenu.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  rowMenu.value = { b, x: r.left, y: r.bottom + 4 }
}
function onDocClick(e) {
  if (rowMenu.value && !e.target.closest?.('.amenu') && !e.target.closest?.('.abtn')) rowMenu.value = null
}

// ── DNS ──
const PRESETS = [
  { name: 'Google DNS', tags: ['UDP'], data: ['8.8.8.8', '8.8.4.4', '2001:4860:4860::8888', '2001:4860:4860::8844'] },
  { name: 'Cloudflare DNS', tags: ['UDP'], data: ['1.1.1.1', '1.0.0.1', '2606:4700:4700::1111', '2606:4700:4700::1001'] },
  { name: 'AdGuard DNS', tags: ['UDP'], data: ['94.140.14.14', '94.140.15.15', '2a10:50c0::ad1:ff', '2a10:50c0::ad2:ff'] },
  { name: 'AdGuard Family DNS', tags: ['UDP', 'Family'], data: ['94.140.14.15', '94.140.15.16', '2a10:50c0::bad1:ff', '2a10:50c0::bad2:ff'] },
  { name: 'Cloudflare Family DNS', tags: ['UDP', 'Family'], data: ['1.1.1.3', '1.0.0.3', '2606:4700:4700::1113', '2606:4700:4700::1003'] },
  { name: 'Cloudflare DoH', tags: ['DoH'], data: ['https://cloudflare-dns.com/dns-query'] },
  { name: 'Google DoH', tags: ['DoH'], data: ['https://dns.google/dns-query'] },
  { name: 'Quad9 Secure DoH', tags: ['DoH', 'Malware'], data: ['https://dns.quad9.net/dns-query'] },
  { name: 'AdGuard DoH', tags: ['DoH', 'Ads'], data: ['https://dns.adguard-dns.com/dns-query'] },
  { name: 'Control D Ads DoH', tags: ['DoH', 'Ads'], data: ['https://freedns.controld.com/p2'] },
  { name: 'Control D Family DoH', tags: ['DoH', 'Family'], data: ['https://freedns.controld.com/p4'] },
]
const presetsOpen = ref(false)
function installPreset(data) {
  form.value.dns.servers = [...form.value.dns.servers, ...data.map((address) => ({ address, port: 0, domains: [], skipFallback: false }))]
  presetsOpen.value = false
}
const serverModal = ref(null) // { index?, address, port, domains, skipFallback }
function openAddServer() {
  serverModal.value = { address: '', port: 0, domains: [], skipFallback: false }
}
function openEditServer(i) {
  const s = form.value.dns.servers[i]
  serverModal.value = { index: i, address: s.address, port: s.port || 0, domains: [...(s.domains || [])], skipFallback: !!s.skipFallback }
}
function submitServer() {
  const m = serverModal.value
  if (!m.address.trim()) return
  const row = { address: m.address.trim(), port: Number(m.port) || 0, domains: m.domains, skipFallback: m.skipFallback }
  const list = [...form.value.dns.servers]
  if (m.index == null) list.push(row)
  else list[m.index] = row
  form.value.dns.servers = list
  serverModal.value = null
}
function removeServer(i) {
  form.value.dns.servers = form.value.dns.servers.filter((_, k) => k !== i)
}
function addHost() {
  form.value.dns.hosts = [...form.value.dns.hosts, { domain: '', values: [] }]
}

// ── advanced ──
const ADV = ['complete', 'inbounds', 'outbounds', 'routing', 'programs']
const adv = ref('complete')
const doc = ref(null)
const advText = ref('')
const advError = ref('')
const advDirty = ref(false)
const programs = ref(null)
async function loadDoc() {
  try {
    doc.value = await api.get('/api/template', { background: true })
    programs.value = await api.get('/api/configs', { background: true })
    renderAdv()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function slice() {
  const d = doc.value
  if (!d) return null
  switch (adv.value) {
    case 'inbounds': return { inbounds: d.inbounds }
    case 'outbounds': return { outbounds: d.outbounds }
    case 'routing': return { routing: d.routing, balancers: d.balancers }
    default: return d
  }
}
function renderAdv() {
  advError.value = ''
  advDirty.value = false
  if (adv.value === 'programs') {
    const p = programs.value || {}
    advText.value = ['# enforcement', p.enforcement || '', '', '# routing', p.routing || '', '', '# shaping', p.shaping || ''].join('\n')
    return
  }
  advText.value = JSON.stringify(slice(), null, 2)
}
watch(adv, renderAdv)
function onAdvInput(v) {
  advText.value = v
  advDirty.value = true
  try {
    JSON.parse(v)
    advError.value = ''
  } catch (e) {
    advError.value = e.message
  }
}
async function saveAdv() {
  let parsed
  try {
    parsed = JSON.parse(advText.value)
  } catch (e) {
    advError.value = e.message
    return
  }
  busy.value = true
  try {
    const res = await api.put(`/api/template?section=${adv.value}`, parsed)
    const msg = `${t('eng.applied')} — ${res.created} ${t('eng.created')}, ${res.updated} ${t('eng.updated')}`
    if (res.skipped?.length) notify(`${msg}\n${res.skipped.join('\n')}`, 'error')
    else notify(msg, 'success')
    await loadDoc()
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

// The header's Save: the settings on the first three pages, the document
// on the fourth.
const saveDisabled = computed(() => busy.value || (active.value === 'advanced' ? !advDirty.value || !!advError.value || adv.value === 'programs' : !dirty.value))
function onSave() {
  if (active.value === 'advanced') return saveAdv()
  return save()
}

watch(active, (v) => {
  if (v === 'advanced' && !doc.value) loadDoc()
  if (v === 'balancer') loadBalancers()
})

onMounted(() => {
  load()
  if (active.value === 'advanced') loadDoc()
  window.addEventListener('click', onDocClick, true)
})
</script>

<template>
  <div class="antpage engine-page">
    <section v-if="showWait" class="acard sk-rows" aria-hidden="true">
      <div v-for="n in 6" :key="n" class="sk-row">
        <div class="sk-row-meta">
          <span class="sk" :style="{ width: 34 + ((n * 7) % 22) + '%' }"></span>
          <span class="sk" :style="{ width: 62 + ((n * 5) % 26) + '%' }"></span>
        </div>
        <span class="sk sk-lg sk-row-control"></span>
      </div>
    </section>
    <div v-else-if="loading" class="empty"></div>
    <ErrorState v-else-if="loadError && !form" :error="loadError" @retry="load" />

    <template v-else-if="form">
      <!-- Their header card: Save, and the standing note. -->
      <div class="acard">
        <div class="acard-body">
          <div class="header-row wide">
            <div class="header-actions">
              <div class="aspace">
                <button class="abtn primary" :disabled="saveDisabled" @click="onSave">{{ t('eng.save') }}</button>
              </div>
            </div>
            <div class="header-info">
              <div class="aalert warning">
                <AntIcon name="ExclamationCircleFilled" />
                <div class="aalert-body"><span class="aalert-title">{{ t('eng.infoDesc') }}</span></div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="acard">
        <div class="acard-body">
          <!-- ══ Basics ══ -->
          <template v-if="active === 'basic'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.basic === '1' }" @click="inner.basic = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('eng.generalConfigs') }}</span></button>
              <button class="atab" :class="{ active: inner.basic === '2' }" @click="inner.basic = '2'"><AntIcon name="BarChartOutlined" /><span>{{ t('eng.statistics') }}</span></button>
              <button class="atab" :class="{ active: inner.basic === '3' }" @click="inner.basic = '3'"><AntIcon name="ClockCircleOutlined" /><span>{{ t('eng.connectionLimits') }}</span></button>
              <button class="atab" :class="{ active: inner.basic === '4' }" @click="inner.basic = '4'"><AntIcon name="FileTextOutlined" /><span>{{ t('eng.logConfigs') }}</span></button>
              <button class="atab" :class="{ active: inner.basic === '5' }" @click="inner.basic = '5'"><AntIcon name="ReloadOutlined" /><span>{{ t('eng.resetDefaultConfig') }}</span></button>
            </div></div>

            <template v-if="inner.basic === '1'">
              <div class="aalert warning mb-12 hint-alert">
                <AntIcon name="ExclamationCircleFilled" />
                <div class="aalert-body"><span class="aalert-title">{{ t('eng.generalConfigsDesc') }}</span></div>
              </div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.routingStrategy') }}</div><div class="setting-list-description">{{ t('eng.routingStrategyDesc') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.domainStrategy">
                  <option value="AsIs">AsIs</option>
                  <option value="UseIPv4">UseIPv4</option>
                  <option value="UseIPv6">UseIPv6</option>
                </select></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.outboundTestUrl') }}</div><div class="setting-list-description">{{ t('eng.outboundTestUrlDesc') }}</div></div></div>
                <div class="acol"><label class="ainput block"><input v-model="form.outboundTestUrl" class="ltr" placeholder="https://www.cloudflare.com/cdn-cgi/trace" /></label></div>
              </div></div>
            </template>

            <template v-else-if="inner.basic === '2'">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.statsOutbound') }}</div><div class="setting-list-description">{{ t('eng.statsOutboundDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.outboundCounters" :label="t('eng.statsOutbound')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.collectInterval') }}<span v-if="isDefault('collectInterval')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('eng.collectIntervalDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.collectInterval" type="number" min="0" max="3600" class="ltr" placeholder="0" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.onlineWindow') }}<span v-if="isDefault('onlineWindow')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('eng.onlineWindowDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.onlineWindow" type="number" min="10" max="86400" class="ltr" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
              </div></div>
            </template>

            <template v-else-if="inner.basic === '3'">
              <div class="aalert warning mb-12 hint-alert">
                <AntIcon name="ExclamationCircleFilled" />
                <div class="aalert-body"><span class="aalert-title">{{ t('eng.connectionLimitsDesc') }}</span></div>
              </div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.probeInterval') }}<span v-if="isDefault('probeInterval')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('eng.probeIntervalDesc') }}</div></div></div>
                <div class="acol"><label class="ainput number"><input v-model.number="form.probeInterval" type="number" min="10" max="86400" class="ltr" placeholder="60" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.enableConcurrency') }}</div><div class="setting-list-description">{{ t('eng.enableConcurrencyDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.probeConcurrency" :label="t('eng.enableConcurrency')" /></div>
              </div></div>
            </template>

            <template v-else-if="inner.basic === '4'">
              <div class="aalert warning mb-12 hint-alert">
                <AntIcon name="ExclamationCircleFilled" />
                <div class="aalert-body"><span class="aalert-title">{{ t('eng.logConfigsDesc') }}</span></div>
              </div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.logLevel') }}</div><div class="setting-list-description">{{ t('eng.logLevelDesc') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.logLevel">
                  <option value="">{{ t('eng.empty') }}</option>
                  <option value="debug">debug</option>
                  <option value="info">info</option>
                  <option value="warn">warning</option>
                  <option value="error">error</option>
                </select></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.logFormat') }}</div><div class="setting-list-description">{{ t('eng.logFormatDesc') }}</div></div></div>
                <div class="acol"><div class="aselect"><select v-model="form.logFormat">
                  <option value="">{{ t('eng.empty') }}</option>
                  <option value="text">text</option>
                  <option value="json">json</option>
                </select></div></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.accessLog') }}</div><div class="setting-list-description">{{ t('eng.accessLogDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.accessLog" :label="t('eng.accessLog')" /></div>
              </div></div>
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.maskAddress') }}</div><div class="setting-list-description">{{ t('eng.maskAddressDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.maskAddress" :label="t('eng.maskAddress')" /></div>
              </div></div>
            </template>

            <template v-else-if="inner.basic === '5'">
              <div class="aspace" style="padding: 0 20px">
                <button class="abtn primary danger-primary" :disabled="busy" @click="askReset = true"><AntIcon name="ReloadOutlined" /><span>{{ t('eng.resetDefaultConfig') }}</span></button>
              </div>
            </template>
          </template>

          <!-- ══ Balancers ══ -->
          <template v-else-if="active === 'balancer'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.balancer === 'balancers' }" @click="inner.balancer = 'balancers'"><AntIcon name="DeploymentUnitOutlined" /><span>{{ t('eng.tabBalancerSettings') }}</span></button>
              <button class="atab" :class="{ active: inner.balancer === 'observatory' }" @click="inner.balancer = 'observatory'"><AntIcon name="RadarChartOutlined" /><span>{{ t('eng.tabObservatory') }}</span></button>
            </div></div>

            <template v-if="inner.balancer === 'balancers'">
              <div v-if="!balancers.length" class="aempty">
                <div class="aempty-desc">{{ t('eng.emptyBalancersDesc') }}</div>
                <button class="abtn primary" style="margin-top: 16px" @click="openAddBalancer"><AntIcon name="PlusOutlined" /><span>{{ t('eng.balancers') }}</span></button>
              </div>
              <div v-else class="aspace-v gap-16">
                <div class="aspace">
                  <button class="abtn primary" @click="openAddBalancer"><AntIcon name="PlusOutlined" /><span>{{ t('eng.balancers') }}</span></button>
                  <button class="abtn" :title="t('eng.balancerLiveRefresh')" :aria-label="t('eng.balancerLiveRefresh')" @click="loadBalancers(false)"><AntIcon name="SyncOutlined" :class="{ spin: liveLoading }" /></button>
                </div>
                <div class="atable-wrap" style="margin-top: 0">
                  <table class="atable small" style="min-width: 900px">
                    <thead>
                      <tr>
                        <th class="center" style="width: 100px">#</th>
                        <th class="center" style="width: 160px">Tag</th>
                        <th class="center" style="width: 140px">Strategy</th>
                        <th class="center">Selector</th>
                        <th class="center" style="width: 160px">Fallback</th>
                        <th class="center" style="width: 140px">{{ t('eng.balancerLive') }}</th>
                        <th class="center" style="width: 200px">{{ t('eng.balancerOverride') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(b, i) in balancers" :key="b.id">
                        <td class="center">
                          <div class="action-cell">
                            <span class="row-index">{{ i + 1 }}</span>
                            <div class="action-buttons">
                              <button class="abtn circle" :aria-label="t('eng.edit')" @click="openEditBalancer(b)"><AntIcon name="EditOutlined" /></button>
                              <button class="abtn circle" :aria-label="t('eng.more')" @click="openRowMenu(b, $event)"><AntIcon name="MoreOutlined" /></button>
                            </div>
                          </div>
                        </td>
                        <td class="center">{{ b.tag }}</td>
                        <td class="center"><span class="atag" :class="b.strategy === 'random' ? 'purple' : 'green'" style="margin: 0">{{ STRATEGY_LABELS[b.strategy] || b.strategy }}</span></td>
                        <td class="center"><span v-for="m in b.memberList" :key="m" class="atag info-large-tag">{{ m }}</span></td>
                        <td class="center">{{ b.fallback || '' }}</td>
                        <td class="center">
                          <span v-if="!b.enabled" class="atag" style="margin: 0" :title="t('eng.balancerNotRunning')">—</span>
                          <span v-else class="atag" :class="b.override ? 'orange' : 'blue'" style="margin: 0" :title="(b.live || []).join(', ')">{{ b.override || (b.live || [])[0] || b.fallback || '—' }}</span>
                        </td>
                        <td class="center">
                          <div class="aselect small" style="width: 170px; text-align: start"><select :value="b.override || ''" :disabled="!b.enabled" @change="setOverride(b, $event.target.value)">
                            <option value="">{{ t('eng.balancerOverridePh') }}</option>
                            <option v-for="m in b.memberList" :key="m" :value="m">{{ m }}</option>
                            <option v-if="b.fallback && !b.memberList.includes(b.fallback)" :value="b.fallback">{{ b.fallback }}</option>
                          </select></div>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </template>

            <template v-else>
              <div v-if="!balancers.length" class="aempty"><div class="aempty-desc">{{ t('eng.observatoryEmptyHint') }}</div></div>
              <div v-else class="aspace-v gap-16">
                <div class="aalert info">
                  <AntIcon name="InfoCircleFilled" />
                  <div class="aalert-body"><span class="aalert-title">{{ t('eng.observatoryAutoManaged') }}</span></div>
                </div>
                <div>
                  <div class="setting-list-item"><div class="arow">
                    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.subjectSelector') }}</div><div class="setting-list-description">{{ t('eng.subjectSelectorDesc') }}</div></div></div>
                    <div class="acol"><span v-for="m in watched" :key="m" class="atag">{{ m }}</span></div>
                  </div></div>
                  <div class="setting-list-item"><div class="arow">
                    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.probeURL') }}</div><div class="setting-list-description">{{ t('eng.probeURLDesc') }}</div></div></div>
                    <div class="acol"><label class="ainput block"><input v-model="form.outboundTestUrl" class="ltr" placeholder="https://www.cloudflare.com/cdn-cgi/trace" /></label></div>
                  </div></div>
                  <div class="setting-list-item"><div class="arow">
                    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.probeInterval') }}</div><div class="setting-list-description">{{ t('eng.probeIntervalDesc') }}</div></div></div>
                    <div class="acol"><label class="ainput number"><input v-model.number="form.probeInterval" type="number" min="10" max="86400" class="ltr" placeholder="60" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
                  </div></div>
                  <div class="setting-list-item"><div class="arow">
                    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.enableConcurrency') }}</div><div class="setting-list-description">{{ t('eng.enableConcurrencyDesc') }}</div></div></div>
                    <div class="acol"><Toggle v-model="form.probeConcurrency" :label="t('eng.enableConcurrency')" /></div>
                  </div></div>
                </div>
              </div>
            </template>
          </template>

          <!-- ══ DNS ══ -->
          <template v-else-if="active === 'dns'">
            <div class="atabs-nav"><div class="atabs-list">
              <button class="atab" :class="{ active: inner.dns === '1' }" @click="inner.dns = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('eng.generalConfigs') }}</span></button>
              <template v-if="form.dns.enabled">
                <button class="atab" :class="{ active: inner.dns === 'hosts' }" @click="inner.dns = 'hosts'"><AntIcon name="ProfileOutlined" /><span>{{ t('eng.dns.hosts') }}</span></button>
                <button class="atab" :class="{ active: inner.dns === '2' }" @click="inner.dns = '2'"><AntIcon name="DatabaseOutlined" /><span>DNS</span></button>
              </template>
            </div></div>

            <template v-if="inner.dns === '1' || !form.dns.enabled">
              <div class="setting-list-item"><div class="arow">
                <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.enable') }}</div><div class="setting-list-description">{{ t('eng.dns.enableDesc') }}</div></div></div>
                <div class="acol"><Toggle v-model="form.dns.enabled" :label="t('eng.dns.enable')" /></div>
              </div></div>
              <template v-if="form.dns.enabled">
                <div class="aalert warning" style="margin-bottom: 12px">
                  <AntIcon name="ExclamationCircleFilled" />
                  <div class="aalert-body"><span class="aalert-title">{{ t('eng.dns.dnsLeakWarning') }}</span></div>
                </div>
                <div class="setting-list-item"><div class="arow">
                  <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.listening') }}</div><div class="setting-list-description">{{ t('eng.dns.listeningDesc') }}</div></div></div>
                  <div class="acol">
                    <span v-for="a in dnsListening" :key="a" class="atag green ltr">{{ a }}:53</span>
                    <span v-if="!dnsListening.length" class="atag">{{ t('eng.dns.notListening') }}</span>
                  </div>
                </div></div>
                <div class="setting-list-item"><div class="arow">
                  <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.strategy') }}</div><div class="setting-list-description">{{ t('eng.dns.strategyDesc') }}</div></div></div>
                  <div class="acol"><div class="aselect"><select v-model="form.dns.queryStrategy">
                    <option value="UseIP">UseIP</option>
                    <option value="UseIPv4">UseIPv4</option>
                    <option value="UseIPv6">UseIPv6</option>
                  </select></div></div>
                </div></div>
                <div class="setting-list-item"><div class="arow">
                  <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.disableCache') }}</div><div class="setting-list-description">{{ t('eng.dns.disableCacheDesc') }}</div></div></div>
                  <div class="acol"><Toggle v-model="form.dns.disableCache" :label="t('eng.dns.disableCache')" /></div>
                </div></div>
                <div class="setting-list-item"><div class="arow">
                  <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.serveExpiredTTL') }}</div><div class="setting-list-description">{{ t('eng.dns.serveExpiredTTLDesc') }}</div></div></div>
                  <div class="acol"><label class="ainput number"><input v-model.number="form.dns.serveExpiredTTL" type="number" min="0" step="60" class="ltr" /></label></div>
                </div></div>
              </template>
            </template>

            <template v-else-if="inner.dns === 'hosts'">
              <div v-if="!form.dns.hosts.length" class="aempty">
                <div class="aempty-desc">{{ t('eng.dns.hostsEmpty') }}</div>
                <button class="abtn primary" style="margin-top: 16px" @click="addHost"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.hostsAdd') }}</span></button>
              </div>
              <div v-else class="aspace-v gap-16">
                <div><button class="abtn primary" @click="addHost"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.hostsAdd') }}</span></button></div>
                <div v-for="(h, i) in form.dns.hosts" :key="i" class="hosts-row">
                  <label class="ainput"><input v-model="h.domain" class="ltr" :placeholder="t('eng.dns.hostsDomain')" :aria-label="t('eng.dns.hostsDomain')" /></label>
                  <div class="tagbox"><TagInput v-model="h.values" :placeholder="t('eng.dns.hostsValues')" /></div>
                  <button class="abtn danger" :aria-label="t('eng.delete')" @click="form.dns.hosts = form.dns.hosts.filter((_, k) => k !== i)"><AntIcon name="DeleteOutlined" /></button>
                </div>
              </div>
            </template>

            <template v-else>
              <div v-if="!form.dns.servers.length" class="aempty">
                <div class="aempty-desc">{{ t('eng.emptyDnsDesc') }}</div>
                <div class="aspace" style="margin-top: 16px">
                  <button class="abtn primary" @click="openAddServer"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.add') }}</span></button>
                  <button class="abtn" @click="presetsOpen = true"><AntIcon name="MenuOutlined" /><span>{{ t('eng.dns.usePreset') }}</span></button>
                </div>
              </div>
              <div v-else class="aspace-v gap-16">
                <div class="aspace">
                  <button class="abtn primary" @click="openAddServer"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.add') }}</span></button>
                  <button class="abtn" @click="presetsOpen = true"><AntIcon name="MenuOutlined" /><span>{{ t('eng.dns.usePreset') }}</span></button>
                  <button class="abtn danger" @click="form.dns.servers = []"><AntIcon name="DeleteOutlined" /><span>{{ t('eng.dns.clearAll') }}</span></button>
                </div>
                <div class="atable-wrap" style="margin-top: 0">
                  <table class="atable small bordered">
                    <thead>
                      <tr>
                        <th class="center" style="width: 60px">#</th>
                        <th>{{ t('eng.address') }}</th>
                        <th>{{ t('eng.dns.domains') }}</th>
                        <th class="center" style="width: 120px">{{ t('eng.dns.skipFallback') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(s, i) in form.dns.servers" :key="i">
                        <td class="center">
                          <div class="action-cell">
                            <span class="row-index">{{ i + 1 }}</span>
                            <div class="action-buttons">
                              <button class="abtn circle" :aria-label="t('eng.edit')" @click="openEditServer(i)"><AntIcon name="EditOutlined" /></button>
                              <button class="abtn circle" :aria-label="t('eng.delete')" @click="removeServer(i)"><AntIcon name="DeleteOutlined" /></button>
                            </div>
                          </div>
                        </td>
                        <td class="ltr muted-break">{{ s.address }}<span v-if="s.port">:{{ s.port }}</span></td>
                        <td><span v-for="d in s.domains" :key="d" class="atag ltr">{{ d }}</span><span v-if="!s.domains?.length" class="muted">{{ t('eng.dns.allDomains') }}</span></td>
                        <td class="center"><span class="atag" :class="s.skipFallback ? 'green' : ''" style="margin: 0">{{ s.skipFallback ? t('eng.yes') : t('eng.no') }}</span></td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </template>
          </template>

          <!-- ══ Advanced ══ -->
          <template v-else>
            <div class="advanced-meta">
              <h4>{{ t('eng.template') }}</h4>
              <p>{{ t('eng.templateDesc') }}</p>
            </div>
            <div class="aradio-group" style="margin: 12px 0">
              <button v-for="k in ADV" :key="k" class="aradio-btn" :class="{ active: adv === k }" @click="adv = k">{{ t(`eng.adv.${k}`) }}</button>
            </div>
            <textarea class="atextarea json" :value="advText" :readonly="adv === 'programs'" spellcheck="false" @input="onAdvInput($event.target.value)"></textarea>
            <p v-if="advError" class="json-error ltr">{{ advError }}</p>
          </template>
        </div>
      </div>
    </template>

    <!-- ── the balancer's row menu ── -->
    <Teleport to="body">
      <div v-if="rowMenu" class="amenu" role="menu" :style="{ top: rowMenu.y + 'px', left: rowMenu.x + 'px' }">
        <button class="amenu-item danger" role="menuitem" @click="askDeleteBalancer = rowMenu.b; rowMenu = null"><AntIcon name="DeleteOutlined" /> {{ t('eng.delete') }}</button>
      </div>
    </Teleport>

    <!-- ── the balancer form ── -->
    <div v-if="balancerModal" class="amodal-backdrop" @click.self="balancerModal = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head">
          <h2 class="amodal-title">{{ balancerModal.id ? `${t('eng.edit')} ${t('eng.balancers')} #${balancers.findIndex((x) => x.id === balancerModal.id) + 1}` : `+ ${t('eng.balancers')}` }}</h2>
          <button class="amodal-close" :aria-label="t('common.close')" @click="balancerModal = null"><AntIcon name="CloseOutlined" /></button>
        </div>
        <div class="amodal-body">
          <div class="aform-item"><label class="aform-label required">{{ t('eng.balancer.tag') }}</label><label class="ainput block"><input v-model="balancerModal.tag" class="ltr" /></label></div>
          <div class="aform-item"><label class="aform-label">{{ t('eng.balancer.strategy') }}</label><div class="aselect"><select v-model="balancerModal.strategy"><option value="random">Random</option><option value="leastPing">Least Ping</option></select></div></div>
          <div class="aform-item"><label class="aform-label required">{{ t('eng.balancer.selector') }}</label><MultiSelect v-model="balancerModal.members" :options="memberOptions" /></div>
          <div class="aform-item"><label class="aform-label">{{ t('eng.balancer.fallback') }}</label><div class="aselect"><select v-model="balancerModal.fallback"><option value="">{{ t('eng.empty') }}</option><option v-for="tag in hopTags" :key="tag" :value="tag">{{ tag }}</option></select></div></div>
          <div v-if="balancerModal.fallback" class="aalert info">
            <AntIcon name="InfoCircleFilled" />
            <div class="aalert-body"><span class="aalert-title">{{ t('eng.balancer.fallbackInfo') }}</span></div>
          </div>
          <p v-if="bError" class="json-error">{{ bError }}</p>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="balancerModal = null">{{ t('eng.cancel') }}</button>
          <button class="abtn primary" :disabled="busy" @click="submitBalancer">{{ t('eng.confirm') }}</button>
        </div>
      </div>
    </div>

    <!-- ── the DNS server form ── -->
    <div v-if="serverModal" class="amodal-backdrop" @click.self="serverModal = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head">
          <h2 class="amodal-title">{{ serverModal.index == null ? t('eng.dns.add') : `${t('eng.edit')} #${serverModal.index + 1}` }}</h2>
          <button class="amodal-close" :aria-label="t('common.close')" @click="serverModal = null"><AntIcon name="CloseOutlined" /></button>
        </div>
        <div class="amodal-body">
          <div class="aform-item"><label class="aform-label required">{{ t('eng.address') }}</label><label class="ainput block"><input v-model="serverModal.address" class="ltr" placeholder="1.1.1.1, tcp://1.1.1.1, https://dns.google/dns-query" /></label></div>
          <div class="aform-item"><label class="aform-label">{{ t('eng.port') }}</label><label class="ainput number"><input v-model.number="serverModal.port" type="number" min="0" max="65535" class="ltr" placeholder="53" /></label></div>
          <div class="aform-item"><label class="aform-label">{{ t('eng.dns.domains') }}</label><TagInput v-model="serverModal.domains" placeholder="example.com, domain:example.com, full:x.y" /></div>
          <div class="aform-item"><label class="aform-label">{{ t('eng.dns.skipFallback') }}</label><Toggle v-model="serverModal.skipFallback" :label="t('eng.dns.skipFallback')" /></div>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="serverModal = null">{{ t('eng.cancel') }}</button>
          <button class="abtn primary" @click="submitServer">{{ t('eng.confirm') }}</button>
        </div>
      </div>
    </div>

    <!-- ── the presets ── -->
    <div v-if="presetsOpen" class="amodal-backdrop" @click.self="presetsOpen = false">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head">
          <h2 class="amodal-title">{{ t('eng.dns.presetTitle') }}</h2>
          <button class="amodal-close" :aria-label="t('common.close')" @click="presetsOpen = false"><AntIcon name="CloseOutlined" /></button>
        </div>
        <div class="amodal-body">
          <div class="aalert warning preset-warning">
            <AntIcon name="ExclamationCircleFilled" />
            <div class="aalert-body"><span class="aalert-title">{{ t('eng.dns.dnsLeakWarning') }}</span></div>
          </div>
          <div class="preset-list">
            <div v-for="p in PRESETS" :key="p.name" class="preset-row">
              <div class="aspace">
                <span v-for="tag in p.tags" :key="tag" class="atag" :class="tag === 'Family' ? 'purple' : tag === 'UDP' ? 'orange' : 'green'" style="margin: 0">{{ tag === 'Family' ? t('eng.dns.presetFamily') : tag }}</span>
                <span class="preset-name">{{ p.name }}</span>
              </div>
              <button class="abtn primary small" @click="installPreset(p.data)">{{ t('eng.install') }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <ConfirmDialog
      :open="askReset"
      :title="t('eng.resetDefaultConfig')"
      :body="t('eng.resetBody')"
      :confirm-label="t('eng.resetDefaultConfig')"
      :danger="true"
      :busy="busy"
      @confirm="resetDefaults"
      @cancel="askReset = false"
    />
    <ConfirmDialog
      :open="!!askDeleteBalancer"
      :title="`${t('eng.delete')} ${t('eng.balancers')} #${balancers.findIndex((x) => x.id === askDeleteBalancer?.id) + 1}?`"
      :body="t('eng.deleteBalancerBody')"
      :confirm-label="t('eng.delete')"
      :danger="true"
      @confirm="deleteBalancer"
      @cancel="askDeleteBalancer = null"
    />
    <ConfirmDialog
      :open="!!pendingRoute"
      :title="t('settings.leaveTitle')"
      :body="t('settings.leaveBody')"
      :confirm-label="t('settings.saveAndLeave')"
      :danger="false"
      :busy="busy"
      @confirm="saveAndGo"
      @cancel="discardAndGo"
    />
  </div>
</template>

<style scoped>
/* Their header row: actions 14/24 wide, the note 10/24, wrapping. */
.header-row { display: flex; flex-wrap: wrap; align-items: center; margin: 0 -8px; }
.header-actions { flex: 0 0 58.3333%; max-width: 58.3333%; padding: 4px 8px; }
.header-info { flex: 0 0 41.6667%; max-width: 41.6667%; padding: 0 8px; display: flex; justify-content: flex-end; }
@media (max-width: 575px) {
  .header-actions, .header-info { flex: 0 0 100%; max-width: 100%; }
}
.gap-16 { gap: 16px; }
.aempty-desc { color: var(--faint); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.json-error { margin: 8px 0 0; color: var(--bad); font-size: 12px; }
.muted { color: var(--faint); }
.muted-break { word-break: break-all; }
.atable td.center { text-align: center; }
.atable th.center { text-align: center; }
.preset-warning { margin-bottom: 12px; }
.preset-list { border: 1px solid var(--line-soft); border-radius: 8px; overflow: hidden; }
.preset-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 12px 24px; border-bottom: 1px solid var(--line-soft); }
.preset-row:last-child { border-bottom: 0; }
.preset-name { font-weight: 500; }
.atag.orange { background: var(--tag-orange-bg); border-color: var(--tag-orange-line); color: var(--tag-orange-ink); }
</style>
