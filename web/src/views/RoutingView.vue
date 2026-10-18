<script setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import PageSpin from '../components/PageSpin.vue'
import BasicTab from '../components/routing/BasicTab.vue'
import RulesTab from '../components/routing/RulesTab.vue'
import BalancersTab from '../components/routing/BalancersTab.vue'
import TesterTab from '../components/routing/TesterTab.vue'
import { useIsMobile } from '../lib/mobile.js'

// The routing page, laid out the way the classic panel lays its own out: the Save bar
// with its warning, then one card with the tabs -- Basic Routing, Routing
// Rules, Route Tester -- each with the icon theirs carries, plus Balancers,
// which theirs keeps on another page and this panel keeps beside the rules
// that use them.
//
// Basic Routing is their list of settings rows: title and description on the
// left half, the control on the right half.

const route = useRoute()
const router = useRouter()

const tabs = [
  { key: 'basic', icon: 'settings' },
  { key: 'rules', icon: 'menu' },
  { key: 'balancers', icon: 'swap' },
  { key: 'tester', icon: 'zap' },
]
const tab = computed({
  get: () => (tabs.some((x) => x.key === route.hash.slice(1)) ? route.hash.slice(1) : 'basic'),
  set: (v) => router.replace({ hash: '#' + v }),
})

const loading = ref(true)
const loadError = ref('')
const saving = ref(false)
const inactive = ref('')
const groups = ref([])
const resolver = ref(null)
const rules = ref([])
// On a phone the rules table becomes the classic panel's RuleCardList.
const isMobile = useIsMobile()
const outbounds = ref([])
const balancers = ref([])
const interfaces = ref([])
const fieldError = ref({})

// Declared after `rules`, not above it: useDelayed watches with `immediate` and
// reads its source during setup, so placed any earlier it reaches a const that
// does not exist yet and takes the page down.
const showSkeleton = useDelayed(computed(() => loading.value && !rules.value.length))

const basic = ref({
  blockBitTorrent: false,
  failClosed: true,
  blockIps: [],
  blockDomains: [],
  blockPorts: [],
  directIps: [],
  directDomains: [],
  ipv4Domains: [],
  defaultOutbound: 'direct',
})
// What was loaded, so Save can be offered only when something actually changed.
const clean = ref('')
const dirty = computed(() => JSON.stringify(basic.value) !== clean.value)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const data = await api.get('/api/routing', { background: quiet })
    basic.value = data.basic
    clean.value = JSON.stringify(data.basic)
    rules.value = data.rules || []
    balancers.value = data.balancers || []
    groups.value = data.groups || []
    resolver.value = data.resolver
    inactive.value = data.inactive || ''
    loadError.value = ''
  } catch (err) {
    loadError.value = err.message
  } finally {
    loading.value = false
  }
}

async function loadOutbounds() {
  try {
    outbounds.value = await api.get('/api/outbounds', { background: true })
    interfaces.value = await api.get('/api/interfaces', { background: true })
    const cs = await api.allClients()
    const names = {}
    for (const c of cs) names[String(c.id)] = c.name
    clientNames.value = names
  } catch {
    // The rule form falls back to a free-text tag if this fails, so a failure
    // here is not worth interrupting the page for.
  }
}

onMounted(async () => {
  await Promise.all([load(), loadOutbounds()])
})

async function save() {
  saving.value = true
  fieldError.value = {}
  try {
    const saved = await api.put('/api/routing', basic.value)
    basic.value = saved
    clean.value = JSON.stringify(saved)
    notify(t('routing.saved'), 'success')
    await load(true)
  } catch (err) {
    if (err.field) {
      fieldError.value = { [err.field]: err.message }
      tab.value = 'basic'
    } else notify(err.message, 'error')
  } finally {
    saving.value = false
  }
}

// ── rules ────────────────────────────────────────────────────────────────────

// The rule the tester last said would decide, and customers' names for the
// rules that name customers.
const decidedBy = ref(null)
const clientNames = ref({})

// Switch to the rules and mark the row the answer named.
function showDecidingRule() {
  tab.value = 'rules'
  nextTick(() => {
    document.querySelector('tr.decided')?.scrollIntoView({ block: 'center' })
  })
}

// ── the tester ───────────────────────────────────────────────────────────────

// ── balancers ────────────────────────────────────────────────────────────────
</script>

<template>
  <section class="view routing-page">
    <!-- Their header Card: Save on the left, the warning on the right. -->
    <div class="card save-bar">
      <div class="save-left">
        <button class="btn primary" :disabled="!dirty || saving" @click="save">
          <span v-if="saving" class="spin sm"></span>
          <span>{{ t('action.save') }}</span>
        </button>
      </div>
      <div class="save-right">
        <div class="alert warning" role="status">
          <Icon name="alert" :size="14" />
          <span>{{ t('routing.saveHint') }}</span>
        </div>
      </div>
    </div>

    <div v-if="loadError" class="card">
      <div class="empty empty-cta">
        <Icon name="alert" :size="28" />
        <p>{{ loadError }}</p>
        <button class="btn" @click="load()">{{ t('action.retry') }}</button>
      </div>
    </div>

    <PageSpin v-else-if="showSkeleton" />

    <div v-else class="card">
      <div class="card-body">
        <!-- On a phone the tab is its icon alone, with the word as its
             tooltip: the classic panel's catTabLabel. -->
        <div class="tabs" :class="{ 'icons-only': isMobile }" role="tablist">
          <button
            v-for="x in tabs"
            :key="x.key"
            role="tab"
            class="tab"
            :class="{ on: tab === x.key }"
            :aria-selected="tab === x.key"
            :aria-label="t(`routing.tab.${x.key}`)"
            :title="isMobile ? t(`routing.tab.${x.key}`) : ''"
            @click="tab = x.key"
          >
            <Icon :name="x.icon" :size="isMobile ? 18 : 14" />
            <template v-if="!isMobile">{{ t(`routing.tab.${x.key}`) }}</template>
          </button>
        </div>

        <!-- Said once. Without it an operator can block a domain, see it
             listed, and never learn the kernel here cannot apply any of it. -->
        <div v-if="inactive" class="alert warning block mb-12">
          <Icon name="alert" :size="14" />
          <span>{{ t('routing.inactive') }} — {{ inactive }}</span>
        </div>

        <!-- ── Basic Routing ─────────────────────────────────────────── -->
        <template v-if="tab === 'basic'">
          <BasicTab v-model:basic="basic" :outbounds="outbounds" :groups="groups" :resolver="resolver" :field-error="fieldError" />
        </template>

        <!-- ── Routing Rules ─────────────────────────────────────────── -->
        <template v-else-if="tab === 'rules'">
          <RulesTab v-model:rules="rules" :outbounds="outbounds" :balancers="balancers" :interfaces="interfaces"
            :client-names="clientNames" :decided-by="decidedBy" @reload="load" />
        </template>

        <!-- ── Balancers ─────────────────────────────────────────────── -->
        <template v-else-if="tab === 'balancers'">
          <BalancersTab :balancers="balancers" :outbounds="outbounds" @reload="load" />
        </template>

        <!-- ── Route Tester ──────────────────────────────────────────── -->
        <!-- Kept while another tab is open, so its answer is still there. -->
        <KeepAlive>
          <TesterTab v-if="tab === 'tester'" :interfaces="interfaces" @decided="(id) => (decidedBy = id)" @show-rule="showDecidingRule" />
        </KeepAlive>
      </div>
    </div>

  </section>
</template>

<style>
/* Keyed on the page's root rather than scoped, so the components it is
   made of are styled as when their markup was the page's. */
.routing-page .card-body {
  padding: 24px;
}
.routing-page .mb-12 {
  margin-bottom: 12px;
}
.routing-page .mb-16 {
  margin-bottom: 16px;
}
/* Their Alert: a full-width band; the hint ones centre their text. */
.routing-page .alert.block {
  display: flex;
  width: 100%;
  box-sizing: border-box;
}
.routing-page .alert.centered {
  justify-content: center;
}
.routing-page .alert.centered > span {
  flex: 1;
  text-align: center;
}
.routing-page .alert.info {
  background: rgba(22, 119, 255, 0.12);
  border-color: rgba(22, 119, 255, 0.45);
}
.routing-page .alert.info svg {
  color: #1677ff;
  flex: none;
}

/* Their SettingListItem with paddings="small": title and description on
   the left half, the control on the right half, 10px 20px of padding and a
   hairline between rows. */
.routing-page .setting-list {
  display: flex;
  flex-direction: column;
}
.routing-page .setting-item {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px 8px;
  align-items: center;
  padding: 10px 20px;
  border-bottom: 1px solid var(--line-soft);
}
.routing-page .setting-list .setting-item:last-child {
  border-bottom: 0;
}
.routing-page .setting-meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.routing-page .setting-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--ink);
}
.routing-page .setting-desc {
  font-size: 14px;
  line-height: 1.5715;
  color: var(--faint);
}
.routing-page .setting-ctl > select {
  width: 100%;
}
.routing-page .resolver {
  margin: 12px 0 0;
}
@media (max-width: 992px) {
  .routing-page .setting-item {
    grid-template-columns: 1fr;
  }
}

/* Rules table: their '#' cell with the drag handle, and the two circles. */
.routing-page .w-act2 {
  width: 1%;
  white-space: nowrap;
}
.routing-page .action-buttons.start {
  justify-content: flex-start;
  margin-inline-start: 0;
}
.routing-page .drag {
  color: var(--faint);
  cursor: grab;
}
.routing-page tr.decided td {
  background: var(--accent-soft);
}

/* Tester: one row, the way their Row gutter lays it out. */
.routing-page .tester-row {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.routing-page .tester-row .grow {
  flex: 1 1 240px;
}
.routing-page .tester-row .port {
  width: 120px;
}
.routing-page .tester-row .net {
  width: 110px;
}
.routing-page .tester-row .inb {
  width: 160px;
}
.routing-page .crit {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}
.routing-page .answer {
  margin-top: 16px;
}
.routing-page .answer-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.routing-page .steps {
  margin: 8px 0 0;
  padding-inline-start: 18px;
  color: var(--muted);
  font-size: var(--t-sm);
}
</style>
