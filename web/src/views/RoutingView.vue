<script setup>
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import Toggle from '../components/Toggle.vue'
import TagInput from '../components/TagInput.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import RoutingRuleForm from '../components/RoutingRuleForm.vue'

const route = useRoute()
const router = useRouter()

const tabs = [
  { key: 'basic', icon: 'shield' },
  { key: 'rules', icon: 'route' },
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
const outbounds = ref([])
const fieldError = ref({})

// Declared after `rules`, not above it: useDelayed watches with `immediate` and
// reads its source during setup, so placed any earlier it reaches a const that
// does not exist yet and takes the page down.
const showSkeleton = useDelayed(computed(() => loading.value && !rules.value.length))

const basic = ref({
  blockBitTorrent: false,
  blockIps: [],
  blockDomains: [],
  blockPorts: [],
  directIps: [],
  directDomains: [],
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
    if (err.field) fieldError.value = { [err.field]: err.message }
    else notify(err.message, 'error')
  } finally {
    saving.value = false
  }
}

function revert() {
  basic.value = JSON.parse(clean.value)
  fieldError.value = {}
}

// ── rules ────────────────────────────────────────────────────────────────────

// The position each rule is actually evaluated at.
//
// Not the row index: a disabled rule is skipped entirely, so with rule two off,
// the rule sitting third in the list is the second one the router consults.
// Showing the row index there would be a number that looks like precedence and
// is not.
const evalOrder = computed(() => {
  const map = {}
  let n = 0
  for (const r of rules.value) map[r.id] = r.enabled ? ++n : null
  return map
})

// The rule the tester last said would decide. Held so the row can be pointed
// at: an answer that names a rule and leaves you to find it in a list of
// twenty has told you half of what you asked.
const decidedBy = ref(null)

const ruleFormFor = ref(null)
const ask = ref(null)
const busy = ref(false)
const pending = ref(new Set())
const isPending = (id) => pending.value.has(id)

async function setRuleEnabled(r, on) {
  const was = r.enabled
  if (was === on) return
  r.enabled = on
  pending.value = new Set(pending.value).add(r.id)
  try {
    const updated = await api.patch(`/api/routing/rules/${r.id}`, {
      name: r.name,
      match: r.match,
      value: r.value,
      outboundTag: r.outboundTag,
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
      await api.delete(`/api/routing/rules/${r.id}`)
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

// ── the tester ───────────────────────────────────────────────────────────────

const probe = ref({ target: '', port: 443, protocol: 'tcp', clientId: 0 })
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
    <header class="page-head">
      <div>
        <h1>{{ t('nav.routing') }}</h1>
        <p class="muted">{{ t('routing.lede') }}</p>
      </div>
    </header>

    <!-- Said once, at the top. Without it an operator can block a domain, see
         it listed, and never learn the kernel here cannot apply any of it. -->
    <div v-if="inactive" class="banner warn">
      <Icon name="alert" :size="16" />
      <span>{{ t('routing.inactive') }} — {{ inactive }}</span>
    </div>

    <div v-if="loadError" class="empty empty-cta">
      <Icon name="alert" :size="28" />
      <p>{{ loadError }}</p>
      <button class="btn" @click="load()">{{ t('action.retry') }}</button>
    </div>

    <table v-else-if="showSkeleton" class="skeleton card" aria-hidden="true">
      <tbody>
        <tr v-for="n in 5" :key="n">
          <td v-for="c in 7" :key="c"><span class="sk"></span></td>
        </tr>
      </tbody>
    </table>
    <div v-else-if="loading" class="empty"></div>

    <template v-else>
      <div class="tabs" role="tablist">
        <button
          v-for="x in tabs"
          :key="x.key"
          role="tab"
          class="tab"
          :class="{ on: tab === x.key }"
          :aria-selected="tab === x.key"
          @click="tab = x.key"
        >
          <Icon :name="x.icon" :size="15" />
          {{ t(`routing.tab.${x.key}`) }}
        </button>
      </div>

      <!-- ── basic ─────────────────────────────────────────────────────── -->
      <div v-if="tab === 'basic'" class="card">
        <div class="card-body form wide">
          <p class="notice">{{ t('routing.blockNotice') }}</p>

          <div class="row-field">
            <div class="row-label">
              <label>{{ t('routing.blockBitTorrent') }}</label>
              <p class="hint">{{ t('routing.blockBitTorrentHint') }}</p>
            </div>
            <Toggle
              v-model="basic.blockBitTorrent"
              :label="t('routing.blockBitTorrent')"
            />
          </div>

          <div class="row-field">
            <div class="row-label">
              <label>{{ t('routing.blockIps') }}</label>
              <p class="hint">{{ t('routing.targetHint') }}</p>
            </div>
            <div class="row-control">
              <TagInput v-model="basic.blockIps" :suggestions="groups" />
              <p v-if="fieldError.blockIps" class="field-error">{{ fieldError.blockIps }}</p>
            </div>
          </div>

          <div class="row-field">
            <div class="row-label">
              <label>{{ t('routing.blockDomains') }}</label>
              <p class="hint">{{ t('routing.domainHint') }}</p>
            </div>
            <div class="row-control">
              <TagInput v-model="basic.blockDomains" />
              <p v-if="fieldError.blockDomains" class="field-error">
                {{ fieldError.blockDomains }}
              </p>
            </div>
          </div>

          <div class="row-field">
            <div class="row-label">
              <label>{{ t('routing.blockPorts') }}</label>
              <p class="hint">{{ t('routing.portHint') }}</p>
            </div>
            <div class="row-control">
              <TagInput v-model="basic.blockPorts" />
              <p v-if="fieldError.blockPorts" class="field-error">{{ fieldError.blockPorts }}</p>
            </div>
          </div>

          <p class="notice">{{ t('routing.directNotice') }}</p>

          <div class="row-field">
            <div class="row-label">
              <label>{{ t('routing.directIps') }}</label>
              <p class="hint">{{ t('routing.targetHint') }}</p>
            </div>
            <div class="row-control">
              <TagInput v-model="basic.directIps" :suggestions="groups" />
              <p v-if="fieldError.directIps" class="field-error">{{ fieldError.directIps }}</p>
            </div>
          </div>

          <div class="row-field">
            <div class="row-label">
              <label>{{ t('routing.directDomains') }}</label>
              <p class="hint">{{ t('routing.domainHint') }}</p>
            </div>
            <div class="row-control">
              <TagInput v-model="basic.directDomains" />
            </div>
          </div>

          <div class="row-field">
            <div class="row-label">
              <label for="def-ob">{{ t('routing.defaultOutbound') }}</label>
              <p class="hint">{{ t('routing.defaultOutboundHint') }}</p>
            </div>
            <div class="row-control">
              <select id="def-ob" v-model="basic.defaultOutbound">
                <option v-for="o in outbounds" :key="o.id" :value="o.tag" :disabled="!o.enabled">
                  {{ o.tag }}
                </option>
              </select>
              <p v-if="fieldError.defaultOutbound" class="field-error">
                {{ fieldError.defaultOutbound }}
              </p>
            </div>
          </div>

          <p v-if="resolver" class="muted small">
            {{ t('routing.resolverStatus')
              .replace('{names}', resolver.names)
              .replace('{addresses}', resolver.addresses) }}
          </p>
        </div>

        <!-- The save bar appears only once something has changed, so a page
             being read never looks like a page with unsaved work on it. -->
        <div v-if="dirty" class="modal-foot sticky-foot">
          <span class="muted small">{{ t('form.unsaved') }}</span>
          <div class="spacer"></div>
          <button type="button" class="btn ghost" @click="revert">{{ t('action.revert') }}</button>
          <button type="button" class="btn primary" :disabled="saving" @click="save">
            <span v-if="saving" class="spin"></span>
            <template v-else>{{ t('action.save') }}</template>
          </button>
        </div>
      </div>

      <!-- ── rules ─────────────────────────────────────────────────────── -->
      <div v-else-if="tab === 'rules'" class="card">
        <div class="toolbar">
          <button class="btn primary" @click="ruleFormFor = {}">
            <Icon name="plus" :size="16" /> {{ t('routing.addRule') }}
          </button>
          <div class="spacer"></div>
          <span class="muted small">{{ t('routing.firstMatchWins') }}</span>
        </div>

        <table v-if="rules.length" class="table">
          <thead>
            <tr>
              <th class="w-gact">{{ t('table.actions') }}</th>
              <th class="w-step">#</th>
              <th>{{ t('table.enabled') }}</th>
              <th>{{ t('routing.rule.name') }}</th>
              <th>{{ t('routing.rule.match') }}</th>
              <th>{{ t('routing.rule.value') }}</th>
              <th>{{ t('routing.rule.outbound') }}</th>
              <th class="right">{{ t('routing.rule.order') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(r, i) in rules"
              :key="r.id"
              :class="{ decided: decidedBy === r.id, off: !r.enabled }"
            >
              <td class="w-gact">
                <div class="actions">
                  <button class="act" :title="t('action.edit')" @click="ruleFormFor = { rule: r }">
                    <Icon name="edit" :size="16" />
                  </button>
                  <button class="act danger" :title="t('action.delete')" @click="removeRule(r)">
                    <Icon name="trash" :size="16" />
                  </button>
                </div>
              </td>
              <!-- Where this rule sits in the order the router consults them.
                   A rule that is switched off is not consulted at all, so it
                   has no place in the sequence rather than a greyed-out one. -->
              <td class="w-step num">
                <span v-if="evalOrder[r.id]" class="step">{{ evalOrder[r.id] }}</span>
                <span v-else class="muted" :title="t('routing.disabled')">—</span>
              </td>
              <td>
                <Toggle
                  :model-value="r.enabled"
                  :label="r.name"
                  :loading="isPending(r.id)"
                  @update:model-value="(v) => setRuleEnabled(r, v)"
                />
              </td>
              <td>
                <strong>{{ r.name }}</strong>
                <div v-if="r.note" class="muted small">{{ r.note }}</div>
              </td>
              <td><span class="tag geekblue">{{ t(`routing.match.${r.match}`) }}</span></td>
              <td class="ltr small">{{ r.value }}</td>
              <td>
                <span class="tag" :class="r.outboundTag === 'blocked' ? 'red' : 'green'">
                  {{ r.outboundTag }}
                </span>
              </td>
              <td class="right">
                <div class="actions">
                  <button
                    class="act"
                    :title="t('routing.moveUp')"
                    :disabled="i === 0"
                    @click="move(i, -1)"
                  >
                    <Icon name="chevronUp" :size="15" />
                  </button>
                  <button
                    class="act"
                    :title="t('routing.moveDown')"
                    :disabled="i === rules.length - 1"
                    @click="move(i, 1)"
                  >
                    <Icon name="chevronDown" :size="15" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>

        <div v-else class="empty empty-cta">
          <Icon name="route" :size="28" />
          <p>{{ t('routing.noRules') }}</p>
          <button class="btn primary" @click="ruleFormFor = {}">{{ t('routing.addRule') }}</button>
        </div>
      </div>

      <!-- ── tester ────────────────────────────────────────────────────── -->
      <div v-else class="card">
        <div class="card-body">
          <p class="notice info">{{ t('routing.testerNotice') }}</p>

          <form class="tester-row" @submit.prevent="testRoute">
            <input
              v-model="probe.target"
              class="ltr"
              :placeholder="t('routing.testTarget')"
              :aria-label="t('routing.testTarget')"
            />
            <input
              v-model.number="probe.port"
              class="ltr narrow"
              type="number"
              :aria-label="t('routing.testPort')"
            />
            <select v-model="probe.protocol" :aria-label="t('routing.testProtocol')">
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="icmp">ICMP</option>
            </select>
            <button class="btn primary" type="submit" :disabled="testing || !probe.target">
              <span v-if="testing" class="spin"></span>
              <template v-else>{{ t('routing.testRoute') }}</template>
            </button>
          </form>

          <p v-if="testError" class="field-error">{{ testError }}</p>

          <div v-if="answer" class="answer">
            <div class="answer-head">
              <span class="muted">{{ t('routing.wouldUse') }}</span>
              <span class="tag lg" :class="answer.blocked ? 'red' : 'green'">
                {{ answer.outbound }}
              </span>
            </div>
            <p class="muted">{{ answer.reason }}</p>
            <!-- Closes the loop: the answer names a rule, and this is the way
                 to the row it names. -->
            <button v-if="decidedBy" type="button" class="btn sm" @click="showDecidingRule">
              {{ t('routing.showRule') }}
            </button>
            <ul v-if="answer.steps?.length" class="steps">
              <li v-for="(s, i) in answer.steps" :key="i">{{ s }}</li>
            </ul>
          </div>
        </div>
      </div>
    </template>

    <RoutingRuleForm
      v-if="ruleFormFor"
      :rule="ruleFormFor.rule"
      :outbounds="outbounds"
      @saved="((ruleFormFor = null), load())"
      @cancel="ruleFormFor = null"
    />

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
