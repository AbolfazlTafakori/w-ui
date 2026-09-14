<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { t, notify, tn } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import Toggle from '../components/Toggle.vue'
import TagInput from '../components/TagInput.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import RoutingRuleForm from '../components/RoutingRuleForm.vue'
import BalancerForm from '../components/BalancerForm.vue'
import PageSpin from '../components/PageSpin.vue'
import AntIcon from '../components/AntIcon.vue'
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

// Their pick-lists. The countries are "geoip:xx", fetched as address lists
// the first time one is used; the rest are this panel's own named groups.
// Anything else -- an address, a range -- can still be typed.
const COUNTRIES = [
  { value: 'geoip:private', label: 'Private IPs' },
  { value: 'geoip:ir', label: '🇮🇷 Iran' },
  { value: 'geoip:cn', label: '🇨🇳 China' },
  { value: 'geoip:ru', label: '🇷🇺 Russia' },
  { value: 'geoip:vn', label: '🇻🇳 Vietnam' },
  { value: 'geoip:es', label: '🇪🇸 Spain' },
  { value: 'geoip:id', label: '🇮🇩 Indonesia' },
  { value: 'geoip:ua', label: '🇺🇦 Ukraine' },
  { value: 'geoip:tr', label: '🇹🇷 Türkiye' },
  { value: 'geoip:br', label: '🇧🇷 Brazil' },
]
const ipSuggestions = computed(() => [
  ...COUNTRIES,
  ...groups.value.filter((g) => g !== 'private').map((g) => ({ value: g, label: g })),
])
const SERVICE_SUGGESTIONS = [
  { value: 'apple.com', label: 'Apple' },
  { value: 'meta.com', label: 'Meta' },
  { value: 'google.com', label: 'Google' },
  { value: 'openai.com', label: 'OpenAI' },
  { value: 'spotify.com', label: 'Spotify' },
  { value: 'netflix.com', label: 'Netflix' },
  { value: 'reddit.com', label: 'Reddit' },
  { value: 'speedtest.net', label: 'Speedtest' },
]

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
    const cs = await api.get('/api/clients?perPage=500', { background: true })
    const names = {}
    for (const c of cs.items || cs || []) names[String(c.id)] = c.name
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

// The rule the tester last said would decide. Held so the row can be pointed
// at: an answer that names a rule and leaves you to find it in a list of
// twenty has told you half of what you asked.
const decidedBy = ref(null)

const ruleFormFor = ref(null)
const ask = ref(null)
const busy = ref(false)
const pending = ref(new Set())
const isPending = (id) => pending.value.has(id)

// What each of their columns shows for a rule: the criteria that belong
// there, each as a small tag, the way theirs renders them.
function sourceOf(r) {
  const out = []
  for (const v of split(r.sourceIps)) out.push({ kind: 'ip', text: v })
  for (const v of split(r.sourcePorts)) out.push({ kind: 'port', text: v })
  for (const v of split(r.clients)) out.push({ kind: 'user', text: clientName(v) })
  for (const v of split(r.groups)) out.push({ kind: 'group', text: v })
  return out
}
function destOf(r) {
  const out = []
  for (const v of split(r.destIps)) out.push({ kind: 'ip', text: v })
  for (const v of split(r.domains)) out.push({ kind: 'domain', text: v })
  for (const v of split(r.ports)) out.push({ kind: 'port', text: v })
  return out
}
function inboundsOf(r) {
  return split(r.interfaces).map((id) => interfaces.value.find((i) => String(i.id) === id)?.name || `#${id}`)
}
const split = (s) => (s || '').split(',').map((x) => x.trim()).filter(Boolean)
const clientNames = ref({})
function clientName(id) {
  return clientNames.value[id] || `#${id}`
}
function isBalancer(tag) {
  return balancers.value.some((b) => b.tag === tag)
}

async function setRuleEnabled(r, on) {
  const was = r.enabled
  if (was === on) return
  r.enabled = on
  pending.value = new Set(pending.value).add(r.id)
  try {
    const updated = await api.patch(`/api/routing/rules/${r.id}`, {
      name: r.name,
      sourceIps: r.sourceIps,
      sourcePorts: r.sourcePorts,
      network: r.network,
      destIps: r.destIps,
      domains: r.domains,
      ports: r.ports,
      clients: r.clients,
      groups: r.groups,
      interfaces: r.interfaces,
      outboundTag: r.outboundTag,
      note: r.note,
      enabled: on,
    })
    Object.assign(r, updated)
  } catch (err) {
    r.enabled = was
    notify(err.message, 'error')
  } finally {
    const next = new Set(pending.value)
    next.delete(r.id)
    pending.value = next
  }
}

// Order is behaviour here, not decoration: the first matching rule decides.
async function move(index, delta) {
  const target = index + delta
  if (target < 0 || target >= rules.value.length) return
  const next = [...rules.value]
  ;[next[index], next[target]] = [next[target], next[index]]
  rules.value = next
  try {
    rules.value = await api.post('/api/routing/rules/order', { ids: next.map((r) => r.id) })
  } catch (err) {
    notify(err.message, 'error')
    await load(true)
  }
}

function removeRule(r) {
  ask.value = {
    title: t('routing.removeRuleTitle'),
    subject: r.name,
    body: t('routing.removeRuleBody'),
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.del(`/api/routing/rules/${r.id}`)
      notify(t('routing.ruleRemoved'), 'success')
      await load()
    },
  }
}

async function runConfirmed() {
  if (!ask.value) return
  busy.value = true
  try {
    await ask.value.run()
    ask.value = null
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

// The row menu their "more" circle opens, and the toolbar's.
const menu = ref(null) // { rule, idx, x, y }
const moreOpen = ref(false)
function openMenuFor(r, idx, event) {
  if (menu.value?.rule?.id === r.id) {
    menu.value = null
    return
  }
  const b = event.currentTarget.getBoundingClientRect()
  const height = 4 * 34 + 10
  const up = b.bottom + height > window.innerHeight && b.top > height
  menu.value = { rule: r, idx, x: b.left, y: up ? b.top - height - 4 : b.bottom + 4 }
}
function closeMenu() {
  menu.value = null
}
function onDocClick(e) {
  if (!menu.value && !moreOpen.value) return
  if (e.target.closest?.('.rowmenu') || e.target.closest?.('.act') || e.target.closest?.('.more-btn')) return
  closeMenu()
  moreOpen.value = false
}
function onKey(e) {
  if (e.key === 'Escape') {
    closeMenu()
    moreOpen.value = false
  }
}
onMounted(() => {
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('resize', closeMenu)
  window.addEventListener('scroll', closeMenu, true)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', closeMenu)
  window.removeEventListener('scroll', closeMenu, true)
})
function menuFor(idx) {
  return [
    { key: 'edit', icon: 'edit', label: t('action.edit') },
    { key: 'up', icon: 'chevronDown', flip: true, label: t('outbound.moveUp'), disabled: idx === 0 },
    { key: 'down', icon: 'chevronDown', label: t('outbound.moveDown'), disabled: idx === rules.value.length - 1 },
    { key: 'del', icon: 'trash', label: t('action.delete'), danger: true },
  ]
}
function pick(r, idx, key) {
  closeMenu()
  if (key === 'edit') ruleFormFor.value = { rule: r }
  else if (key === 'up') move(idx, -1)
  else if (key === 'down') move(idx, 1)
  else if (key === 'del') removeRule(r)
}

// Import and export, the way theirs offers them under "more": the rules as
// a JSON array, in the shape the API takes.
const importOpen = ref(false)
const importText = ref('')
const exportOpen = ref(false)
const exportText = computed(() =>
  JSON.stringify(
    rules.value.map((r) => ({
      name: r.name,
      enabled: r.enabled,
      sourceIps: r.sourceIps || undefined,
      sourcePorts: r.sourcePorts || undefined,
      network: r.network || undefined,
      destIps: r.destIps || undefined,
      domains: r.domains || undefined,
      ports: r.ports || undefined,
      clients: r.clients || undefined,
      groups: r.groups || undefined,
      interfaces: r.interfaces || undefined,
      outboundTag: r.outboundTag,
      note: r.note || undefined,
    })),
    null,
    2,
  ),
)
async function copyExport() {
  try {
    await navigator.clipboard.writeText(exportText.value)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
async function runImport() {
  let parsed
  try {
    parsed = JSON.parse(importText.value)
  } catch {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  const list = Array.isArray(parsed) ? parsed : Array.isArray(parsed?.rules) ? parsed.rules : null
  if (!list) {
    notify(t('outbound.importInvalidJson'), 'error')
    return
  }
  busy.value = true
  let added = 0
  try {
    for (const item of list) {
      if (!item || typeof item !== 'object') continue
      await api.post('/api/routing/rules', item)
      added++
    }
    notify(tn('routing.imported', added), 'success')
    importOpen.value = false
    importText.value = ''
    await load()
  } catch (err) {
    notify(err.message, 'error')
    if (added) await load()
  } finally {
    busy.value = false
  }
}

// ── the tester ───────────────────────────────────────────────────────────────

const probe = ref({ target: '', port: 443, protocol: 'tcp', clientId: 0, interfaceId: 0 })

// ── balancers ────────────────────────────────────────────────────────────────
const balancerFormFor = ref(null)
async function setBalancerEnabled(b, on) {
  const was = b.enabled
  b.enabled = on
  try {
    await api.patch(`/api/balancers/${b.id}`, { tag: b.tag, strategy: b.strategy, members: b.memberList, note: b.note, enabled: on })
  } catch (err) {
    b.enabled = was
    notify(err.message, 'error')
  }
}
function removeBalancer(b) {
  ask.value = {
    title: t('routing.removeBalancerTitle'),
    subject: b.tag,
    body: t('routing.removeBalancerBody'),
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.del(`/api/balancers/${b.id}`)
      notify(t('routing.balancerRemoved'), 'success')
      await load()
    },
  }
}
const answer = ref(null)
const testing = ref(false)
const testError = ref('')

// Switch to the rules and mark the row the answer named.
function showDecidingRule() {
  tab.value = 'rules'
  nextTick(() => {
    document.querySelector('tr.decided')?.scrollIntoView({ block: 'center' })
  })
}

async function testRoute() {
  testing.value = true
  testError.value = ''
  answer.value = null
  try {
    answer.value = await api.post('/api/routing/test', probe.value)
    decidedBy.value = answer.value.ruleId || null
  } catch (err) {
    testError.value = err.message
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <section class="view">
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
          <span>{{ t('outbound.saveHint') }}</span>
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
          <div class="alert warning block centered mb-12">
            <Icon name="alert" :size="14" />
            <span>{{ t('routing.blockNotice') }}</span>
          </div>

          <div class="setting-list">
            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.defaultOutbound') }}</div>
                <div class="setting-desc">{{ t('routing.defaultOutboundHint') }}</div>
              </div>
              <div class="setting-ctl">
                <select id="def-ob" v-model="basic.defaultOutbound">
                  <option v-for="o in outbounds" :key="o.id" :value="o.tag" :disabled="!o.enabled">{{ o.tag }}</option>
                </select>
                <p v-if="fieldError.defaultOutbound" class="field-error">{{ fieldError.defaultOutbound }}</p>
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.failClosed') }}</div>
                <div class="setting-desc">{{ t('routing.failClosedDesc') }}</div>
              </div>
              <div class="setting-ctl">
                <Toggle v-model="basic.failClosed" :label="t('routing.failClosed')" />
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.blockBitTorrent') }}</div>
              </div>
              <div class="setting-ctl">
                <Toggle v-model="basic.blockBitTorrent" :label="t('routing.blockBitTorrent')" />
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.blockIps') }}</div>
              </div>
              <div class="setting-ctl">
                <TagInput v-model="basic.blockIps" :suggestions="ipSuggestions" />
                <p v-if="fieldError.blockIps" class="field-error">{{ fieldError.blockIps }}</p>
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.blockDomains') }}</div>
              </div>
              <div class="setting-ctl">
                <TagInput v-model="basic.blockDomains" />
                <p v-if="fieldError.blockDomains" class="field-error">{{ fieldError.blockDomains }}</p>
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.blockPorts') }}</div>
                <div class="setting-desc">{{ t('routing.portHint') }}</div>
              </div>
              <div class="setting-ctl">
                <TagInput v-model="basic.blockPorts" />
                <p v-if="fieldError.blockPorts" class="field-error">{{ fieldError.blockPorts }}</p>
              </div>
            </div>
          </div>

          <div class="alert warning block centered mb-12">
            <Icon name="alert" :size="14" />
            <span>{{ t('routing.directNotice') }}</span>
          </div>

          <div class="setting-list">
            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.directIps') }}</div>
              </div>
              <div class="setting-ctl">
                <TagInput v-model="basic.directIps" :suggestions="ipSuggestions" />
                <p v-if="fieldError.directIps" class="field-error">{{ fieldError.directIps }}</p>
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.directDomains') }}</div>
              </div>
              <div class="setting-ctl">
                <TagInput v-model="basic.directDomains" />
                <p v-if="fieldError.directDomains" class="field-error">{{ fieldError.directDomains }}</p>
              </div>
            </div>

            <div class="setting-item">
              <div class="setting-meta">
                <div class="setting-title">{{ t('routing.ipv4Routing') }}</div>
                <div class="setting-desc">{{ t('routing.ipv4RoutingDesc') }}</div>
              </div>
              <div class="setting-ctl">
                <TagInput v-model="basic.ipv4Domains" :suggestions="SERVICE_SUGGESTIONS" />
                <p v-if="fieldError.ipv4Domains" class="field-error">{{ fieldError.ipv4Domains }}</p>
              </div>
            </div>
          </div>

          <p v-if="resolver" class="muted small resolver">
            {{ t('routing.resolverStatus').replace('{names}', resolver.names).replace('{addresses}', resolver.addresses) }}
          </p>
        </template>

        <!-- ── Routing Rules ─────────────────────────────────────────── -->
        <template v-else-if="tab === 'rules'">
          <div class="toolbar-group mb-16">
            <button class="btn primary" @click="ruleFormFor = {}">
              <Icon name="plus" :size="14" />
              <span>{{ t('routing.tab.rules') }}</span>
            </button>
            <div class="more-wrap">
              <button class="btn more-btn" :aria-expanded="moreOpen" @click="moreOpen = !moreOpen">
                <Icon name="more" :size="14" />
                <span>{{ t('outbound.more') }}</span>
              </button>
              <div v-if="moreOpen" class="rowmenu below" role="menu">
                <button class="menu-item" role="menuitem" @click="moreOpen = false; importOpen = true">
                  <Icon name="download" :size="14" />{{ t('routing.importRules') }}
                </button>
                <button class="menu-item" role="menuitem" :disabled="!rules.length" @click="moreOpen = false; exportOpen = true">
                  <Icon name="upload" :size="14" />{{ t('routing.exportRules') }}
                </button>
              </div>
            </div>
          </div>

          <div v-if="isMobile" class="rule-list">
            <div v-if="!rules.length" class="rule-empty">—</div>
            <div v-for="(r, i) in rules" :key="r.id" class="rule-card" :class="{ 'rule-disabled': !r.enabled }">
              <div class="rule-card-head">
                <Icon name="menu" :size="14" class="drag-handle" :title="t('routing.dragToReorder')" />
                <span class="rule-number">#{{ i + 1 }}</span>
                <button class="act round sm" :aria-label="t('action.more')" :aria-expanded="menu?.rule?.id === r.id" @click="openMenuFor(r, i, $event)">
                  <AntIcon name="MoreOutlined" />
                </button>
                <Toggle :model-value="r.enabled" :label="r.name" small :loading="isPending(r.id)" style="margin-inline-start: 8px" @update:model-value="(v) => setRuleEnabled(r, v)" />
              </div>
              <div class="rule-flow">
                <div class="flow-side">
                  <span class="flow-label">{{ t('nav.interfaces') }}</span>
                  <span v-if="inboundsOf(r).length" class="tag blue flow-tag">{{ inboundsOf(r).join(', ') }}</span>
                  <span v-else class="criterion-empty">any</span>
                </div>
                <span class="flow-arrow">→</span>
                <div class="flow-side flow-side-target">
                  <span class="flow-label">{{ isBalancer(r.outboundTag) ? t('routing.tab.balancers') : t('nav.outbounds') }}</span>
                  <span v-if="isBalancer(r.outboundTag)" class="tag purple flow-tag"><AntIcon name="ClusterOutlined" /> {{ r.outboundTag }}</span>
                  <span v-else-if="r.outboundTag" class="tag flow-tag" :class="r.outboundTag === 'blocked' ? 'red' : 'green'"><AntIcon name="ExportOutlined" /> {{ r.outboundTag }}</span>
                  <span v-else class="criterion-empty">—</span>
                </div>
              </div>
              <div v-if="sourceOf(r).length || destOf(r).length || r.network" class="rule-criteria">
                <span v-for="(c, k) in sourceOf(r)" :key="'s' + k" class="criterion-chip" :title="`${t('routing.col.source')}: ${c.text}`">
                  <span class="criterion-chip-label">{{ t('routing.col.source') }}</span><span class="criterion-chip-value ltr">{{ c.text }}</span>
                </span>
                <span v-if="r.network" class="criterion-chip">
                  <span class="criterion-chip-label">{{ t('routing.col.network') }}</span><span class="criterion-chip-value">{{ r.network }}</span>
                </span>
                <span v-for="(c, k) in destOf(r)" :key="'d' + k" class="criterion-chip" :title="`${t('routing.col.dest')}: ${c.text}`">
                  <span class="criterion-chip-label">{{ t('routing.col.dest') }}</span><span class="criterion-chip-value ltr">{{ c.text }}</span>
                </span>
              </div>
              <div v-if="r.name || r.note" class="rule-comment" :title="r.note || r.name">
                <span class="rule-comment-text">{{ r.name }}<template v-if="r.note"> — {{ r.note }}</template></span>
              </div>
            </div>
          </div>

          <div v-else class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th class="w-num center">#</th>
                  <th class="w-act2">{{ t('table.actions') }}</th>
                  <th class="w-sm">{{ t('table.enabled') }}</th>
                  <th>{{ t('routing.col.source') }}</th>
                  <th>{{ t('routing.col.comment') }}</th>
                  <th class="w-sm">{{ t('routing.col.network') }}</th>
                  <th>{{ t('routing.col.dest') }}</th>
                  <th>{{ t('nav.interfaces') }}</th>
                  <th>{{ t('nav.outbounds') }}</th>
                  <th>{{ t('routing.tab.balancers') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!rules.length" class="empty-row">
                  <td colspan="10">
                    <div class="card-empty">
                      <Icon name="route" :size="32" />
                      <div>{{ t('common.nothingYet') }}</div>
                    </div>
                  </td>
                </tr>
                <tr
                  v-for="(r, i) in rules"
                  :key="r.id"
                  :class="{ decided: decidedBy === r.id, off: !r.enabled }"
                >
                  <td class="w-num">
                    <div class="rownum">
                      <Icon name="menu" :size="13" class="drag" :title="t('routing.dragToReorder')" />
                      <span class="num row-index">{{ i + 1 }}</span>
                    </div>
                  </td>
                  <td class="w-act2">
                    <div class="action-buttons start">
                      <button class="act round" :aria-label="t('action.edit')" :title="t('action.edit')" @click="ruleFormFor = { rule: r }">
                        <Icon name="edit" :size="13" />
                      </button>
                      <button class="act round" :aria-label="t('action.more')" :title="t('action.more')" :aria-expanded="menu?.rule?.id === r.id" @click="openMenuFor(r, i, $event)">
                        <Icon name="more" :size="13" />
                      </button>
                    </div>
                  </td>
                  <td>
                    <Toggle :model-value="r.enabled" :label="r.name" :loading="isPending(r.id)" @update:model-value="(v) => setRuleEnabled(r, v)" />
                  </td>
                  <td>
                    <div v-if="sourceOf(r).length" class="crit">
                      <span v-for="(c, k) in sourceOf(r)" :key="k" class="tag ltr" :class="c.kind === 'user' || c.kind === 'group' ? 'geekblue' : ''">{{ c.text }}</span>
                    </div>
                    <span v-else class="muted">—</span>
                  </td>
                  <td>
                    <strong>{{ r.name }}</strong>
                    <div v-if="r.note" class="muted small">{{ r.note }}</div>
                  </td>
                  <td>
                    <span v-if="r.network" class="tag">{{ r.network }}</span>
                    <span v-else class="muted">—</span>
                  </td>
                  <td>
                    <div v-if="destOf(r).length" class="crit">
                      <span v-for="(c, k) in destOf(r)" :key="k" class="tag ltr" :class="c.kind === 'domain' ? 'geekblue' : ''">{{ c.text }}</span>
                    </div>
                    <span v-else class="muted">—</span>
                  </td>
                  <td>
                    <div v-if="inboundsOf(r).length" class="crit">
                      <span v-for="n in inboundsOf(r)" :key="n" class="tag">{{ n }}</span>
                    </div>
                    <span v-else class="muted">—</span>
                  </td>
                  <td>
                    <span v-if="!isBalancer(r.outboundTag)" class="tag" :class="r.outboundTag === 'blocked' ? 'red' : 'green'">{{ r.outboundTag }}</span>
                    <span v-else class="muted">—</span>
                  </td>
                  <td>
                    <span v-if="isBalancer(r.outboundTag)" class="tag purple">{{ r.outboundTag }}</span>
                    <span v-else class="muted">—</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>

        <!-- ── Balancers ─────────────────────────────────────────────── -->
        <template v-else-if="tab === 'balancers'">
          <div class="toolbar-group mb-16">
            <button class="btn primary" @click="balancerFormFor = {}">
              <Icon name="plus" :size="14" />
              <span>{{ t('routing.tab.balancers') }}</span>
            </button>
          </div>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th class="w-act2">{{ t('table.actions') }}</th>
                  <th class="w-sm">{{ t('table.enabled') }}</th>
                  <th>{{ t('outbound.tag') }}</th>
                  <th>{{ t('routing.balancer.strategy') }}</th>
                  <th>{{ t('nav.outbounds') }}</th>
                  <th>{{ t('routing.rule.note') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!balancers.length" class="empty-row">
                  <td colspan="6">
                    <div class="card-empty">
                      <Icon name="swap" :size="32" />
                      <div>{{ t('common.nothingYet') }}</div>
                    </div>
                  </td>
                </tr>
                <tr v-for="b in balancers" :key="b.id" :class="{ off: !b.enabled }">
                  <td class="w-act2">
                    <div class="action-buttons start">
                      <button class="act round" :aria-label="t('action.edit')" :title="t('action.edit')" @click="balancerFormFor = { balancer: b }"><Icon name="edit" :size="13" /></button>
                      <button class="act round" :aria-label="t('action.delete')" :title="t('action.delete')" @click="removeBalancer(b)"><Icon name="trash" :size="13" /></button>
                    </div>
                  </td>
                  <td><Toggle :model-value="b.enabled" :label="b.tag" @update:model-value="(v) => setBalancerEnabled(b, v)" /></td>
                  <td><span class="tag purple">{{ b.tag }}</span></td>
                  <td>{{ b.strategy }}</td>
                  <td>
                    <div class="crit"><span v-for="m in b.memberList" :key="m" class="tag green">{{ m }}</span></div>
                  </td>
                  <td class="muted small">{{ b.note }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>

        <!-- ── Route Tester ──────────────────────────────────────────── -->
        <template v-else>
          <div class="alert info block mb-12">
            <Icon name="info" :size="14" />
            <span>{{ t('routing.testerNotice') }}</span>
          </div>

          <form class="tester-row" @submit.prevent="testRoute">
            <input v-model="probe.target" class="ltr grow" :placeholder="t('routing.testTarget')" :aria-label="t('routing.testTarget')" />
            <input v-model.number="probe.port" class="ltr port" type="number" min="1" max="65535" :placeholder="t('routing.testPort')" :aria-label="t('routing.testPort')" />
            <select v-model="probe.protocol" class="net" :aria-label="t('routing.testProtocol')">
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="icmp">ICMP</option>
            </select>
            <select v-model.number="probe.interfaceId" class="inb" :aria-label="t('routing.testInbound')">
              <option :value="0">{{ t('routing.testInbound') }}</option>
              <option v-for="i in interfaces" :key="i.id" :value="i.id">{{ i.name }}</option>
            </select>
            <button class="btn primary" type="submit" :disabled="testing || !probe.target">
              <span v-if="testing" class="spin sm"></span>
              <Icon v-else name="zap" :size="14" />
              <span>{{ t('routing.testRoute') }}</span>
            </button>
          </form>

          <p v-if="testError" class="field-error">{{ testError }}</p>

          <div v-if="answer" class="answer">
            <div v-if="answer.ruleId || answer.blocked" class="answer-head">
              <span>{{ t('routing.matchedOutbound') }}:</span>
              <span class="tag lg" :class="answer.blocked ? 'red' : 'green'">{{ answer.outbound || '—' }}</span>
              <template v-if="answer.balancer">
                <span class="muted">{{ t('routing.viaBalancer') }}:</span>
                <span class="tag purple">{{ answer.balancer }}</span>
              </template>
              <button v-if="decidedBy" type="button" class="btn sm" @click="showDecidingRule">{{ t('routing.showRule') }}</button>
            </div>
            <div v-else class="alert warning block">
              <Icon name="alert" :size="14" />
              <span>{{ t('routing.noRuleMatched') }} <span class="tag green">{{ answer.outbound }}</span></span>
            </div>
            <p class="muted small">{{ answer.reason }}</p>
            <ul v-if="answer.steps?.length" class="steps">
              <li v-for="(s, i) in answer.steps" :key="i">{{ s }}</li>
            </ul>
          </div>
        </template>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="menu" class="rowmenu" role="menu" :style="{ top: menu.y + 'px', left: menu.x + 'px' }">
        <button
          v-for="m in menuFor(menu.idx)"
          :key="m.key"
          class="menu-item"
          :class="{ danger: m.danger }"
          :disabled="m.disabled"
          role="menuitem"
          @click="pick(menu.rule, menu.idx, m.key)"
        >
          <Icon :name="m.icon" :size="14" :class="{ flip: m.flip }" />{{ m.label }}
        </button>
      </div>
    </Teleport>

    <RoutingRuleForm
      v-if="ruleFormFor"
      :rule="ruleFormFor.rule"
      :outbounds="outbounds"
      :balancers="balancers"
      @saved="((ruleFormFor = null), load())"
      @cancel="ruleFormFor = null"
    />
    <BalancerForm
      v-if="balancerFormFor"
      :balancer="balancerFormFor.balancer"
      :outbounds="outbounds"
      @saved="((balancerFormFor = null), load())"
      @cancel="balancerFormFor = null"
    />

    <div v-if="importOpen" class="modal-backdrop" @click.self="importOpen = false">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="rr-import-title">
        <div class="card-head">
          <h2 id="rr-import-title">{{ t('routing.importRules') }}</h2>
          <button class="act" :aria-label="t('common.close')" @click="importOpen = false"><Icon name="close" :size="16" /></button>
        </div>
        <div class="card-body">
          <div class="field"><textarea v-model="importText" class="ltr mono" rows="12" spellcheck="false" placeholder="[ { ... } ]"></textarea></div>
        </div>
        <div class="modal-foot">
          <button type="button" class="btn" @click="importOpen = false">{{ t('common.close') }}</button>
          <button class="btn primary" :disabled="busy || !importText.trim()" @click="runImport">
            <span v-if="busy" class="spin"></span>
            <template v-else>{{ t('routing.importRules') }}</template>
          </button>
        </div>
      </div>
    </div>

    <div v-if="exportOpen" class="modal-backdrop" @click.self="exportOpen = false">
      <div class="modal" role="dialog" aria-modal="true" aria-labelledby="rr-export-title">
        <div class="card-head">
          <h2 id="rr-export-title">{{ t('routing.exportRules') }}</h2>
          <button class="act" :aria-label="t('common.close')" @click="exportOpen = false"><Icon name="close" :size="16" /></button>
        </div>
        <div class="card-body">
          <div class="field"><textarea class="ltr mono" rows="12" readonly spellcheck="false" :value="exportText"></textarea></div>
        </div>
        <div class="modal-foot">
          <button type="button" class="btn" @click="exportOpen = false">{{ t('common.close') }}</button>
          <button class="btn primary" @click="copyExport"><Icon name="copy" :size="14" /><span>{{ t('action.copy') }}</span></button>
        </div>
      </div>
    </div>

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :subject="ask?.subject || ''"
      :confirm-label="ask?.confirmLabel || ''"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </section>
</template>

<style scoped>
.card-body {
  padding: 24px;
}
.mb-12 {
  margin-bottom: 12px;
}
.mb-16 {
  margin-bottom: 16px;
}
/* Their Alert: a full-width band; the hint ones centre their text. */
.alert.block {
  display: flex;
  width: 100%;
  box-sizing: border-box;
}
.alert.centered {
  justify-content: center;
}
.alert.centered > span {
  flex: 1;
  text-align: center;
}
.alert.info {
  background: rgba(22, 119, 255, 0.12);
  border-color: rgba(22, 119, 255, 0.45);
}
.alert.info svg {
  color: #1677ff;
  flex: none;
}

/* Their SettingListItem with paddings="small": title and description on
   the left half, the control on the right half, 10px 20px of padding and a
   hairline between rows. */
.setting-list {
  display: flex;
  flex-direction: column;
}
.setting-item {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px 8px;
  align-items: center;
  padding: 10px 20px;
  border-bottom: 1px solid var(--line-soft);
}
.setting-list .setting-item:last-child {
  border-bottom: 0;
}
.setting-meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.setting-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--ink);
}
.setting-desc {
  font-size: 14px;
  line-height: 1.5715;
  color: var(--faint);
}
.setting-ctl > select {
  width: 100%;
}
.resolver {
  margin: 12px 0 0;
}
@media (max-width: 992px) {
  .setting-item {
    grid-template-columns: 1fr;
  }
}

/* Rules table: their '#' cell with the drag handle, and the two circles. */
.w-act2 {
  width: 1%;
  white-space: nowrap;
}
.action-buttons.start {
  justify-content: flex-start;
  margin-inline-start: 0;
}
.drag {
  color: var(--faint);
  cursor: grab;
}
tr.decided td {
  background: var(--accent-soft);
}

/* Tester: one row, the way their Row gutter lays it out. */
.tester-row {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.tester-row .grow {
  flex: 1 1 240px;
}
.tester-row .port {
  width: 120px;
}
.tester-row .net {
  width: 110px;
}
.tester-row .inb {
  width: 160px;
}
.crit {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}
.answer {
  margin-top: 16px;
}
.answer-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.steps {
  margin: 8px 0 0;
  padding-inline-start: 18px;
  color: var(--muted);
  font-size: var(--t-sm);
}
</style>
