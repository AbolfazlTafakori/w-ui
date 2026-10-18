<script setup>
// Routing Rules: the list, in the order that decides -- the first rule that
// matches a flow sends it -- each switched on or off, edited, moved, or
// deleted, and all of them imported or exported as the API takes them.
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, tn, notify } from '../../lib/store.js'
import { useIsMobile } from '../../lib/mobile.js'
import Icon from '../Icon.vue'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import ConfirmDialog from '../ConfirmDialog.vue'
import RoutingRuleForm from '../RoutingRuleForm.vue'

// The rules, in their order; a move is written back.
const rules = defineModel('rules', { type: Array, required: true })
const props = defineProps({
  outbounds: { type: Array, default: () => [] },
  balancers: { type: Array, default: () => [] },
  interfaces: { type: Array, default: () => [] },
  // Customers' names by id, for the rules that name customers.
  clientNames: { type: Object, default: () => ({}) },
  // The rule the tester last said would decide, marked in the table.
  decidedBy: { type: Number, default: null },
})
// reload(quiet): the rules changed on the server.
const emit = defineEmits(['reload'])
// On a phone the table becomes the classic panel's RuleCardList.
const isMobile = useIsMobile()

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
  return split(r.interfaces).map((id) => props.interfaces.find((i) => String(i.id) === id)?.name || `#${id}`)
}
const split = (s) => (s || '').split(',').map((x) => x.trim()).filter(Boolean)
function clientName(id) {
  return props.clientNames[id] || `#${id}`
}
function isBalancer(tag) {
  return props.balancers.some((b) => b.tag === tag)
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
    emit('reload', true)
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
      emit('reload')
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
    emit('reload')
  } catch (err) {
    notify(err.message, 'error')
    if (added) emit('reload')
  } finally {
    busy.value = false
  }
}
</script>

<template>
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

  <Teleport to="body">
    <div v-if="menu" v-fit class="rowmenu" role="menu" :style="{ top: menu.y + 'px', left: menu.x + 'px' }">
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
    @saved="((ruleFormFor = null), emit('reload'))"
    @cancel="ruleFormFor = null"
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
</template>
