<script setup>
// The engine's balancers: a table of them with what each is using right now
// and a way to pin one, and the observatory that probes their members.
import { computed, onMounted, ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import { useEngine } from './context.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import MultiSelect from '../MultiSelect.vue'
import MoreMenu from '../MoreMenu.vue'
import ConfirmDialog from '../ConfirmDialog.vue'

const { form, inner } = useEngine()

const balancers = ref([])
const outbounds = ref([])
const liveLoading = ref(false)
const busy = ref(false)
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
// Read each time the tab is opened: what a balancer is using changes on
// its own, between visits.
onMounted(() => {
  loadBalancers()
  loadOutbounds()
})

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

// The row menu holds one thing: deleting, which asks first.
const rowItems = computed(() => [{ key: 'delete', label: t('eng.delete'), icon: 'DeleteOutlined', danger: true }])
const askDelete = ref(null)
async function deleteBalancer() {
  const b = askDelete.value
  askDelete.value = null
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
</script>

<template>
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
                  <MoreMenu :items="rowItems" compact button-class="abtn circle" :label="t('eng.more')" @pick="askDelete = b" />
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

  <ConfirmDialog
    :open="!!askDelete"
    :title="`${t('eng.delete')} ${t('eng.balancers')} #${balancers.findIndex((x) => x.id === askDelete?.id) + 1}?`"
    :body="t('eng.deleteBalancerBody')"
    :confirm-label="t('eng.delete')"
    :danger="true"
    @confirm="deleteBalancer"
    @cancel="askDelete = null"
  />
</template>
