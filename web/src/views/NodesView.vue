<script setup>
// The servers this panel runs customers on: the figures across the top, a
// row each -- a card on a phone -- and what can be done to one. How a node
// reads is lib/nodes/rows.js; the rows, the form and the token dialog are
// their own; this holds the list and what is mid-request.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { mergeRows, useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import { takesImport, totals as totalsOf } from '../lib/nodes/rows.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import NodeImportDialog from '../components/NodeImportDialog.vue'
import NodesTable from '../components/nodes/NodesTable.vue'
import NodeCards from '../components/nodes/NodeCards.vue'
import NodeForm from '../components/nodes/NodeForm.vue'
import TokenDialog from '../components/nodes/TokenDialog.vue'
import { useIsMobile } from '../lib/mobile.js'

const nodes = ref([])
const isMobile = useIsMobile()
const loading = ref(true)

// Declared here, after the state it reads: useDelayed watches with `immediate`
// and evaluates its source during setup.
const showSkeleton = useDelayed(computed(() => loading.value && !nodes.value.length))
const loadError = ref(null)
const busy = ref(false)
const formFor = ref(null) // {} to add, { node } to edit
const issuing = ref(false)
// A 3x-ui or Pasarguard node whose own users are being brought in.
const importing = ref(null)
let timer = null

onMounted(() => {
  load()
  // The panel probes on its own every half minute; this only re-reads what it
  // already found, so it can be frequent without costing a remote request.
  timer = setInterval(() => load(true), 15_000)
})
onBeforeUnmount(() => clearInterval(timer))

async function load(quiet = false) {
  try {
    const fresh = await api.get('/api/nodes', { background: quiet })
    nodes.value = quiet ? mergeRows(nodes.value, fresh, pending.value) : fresh
    loadError.value = null
  } catch (e) {
    loadError.value = e
  } finally {
    loading.value = false
  }
}

const totals = computed(() => totalsOf(nodes.value))

async function saved(made) {
  formFor.value = null
  await load()
  // A panel with users of its own: offered to bring them in at once.
  if (made && takesImport(made)) importing.value = made
}

// Asking a node to update its own panel.
//
// The answer is passed back whole: "this build carries no release-signing key"
// is something an operator can act on, and is a different problem from the node
// not answering at all.
function askNodeUpdate(node) {
  ask.value = {
    title: t('update.askNodeTitle'),
    subject: node.name,
    body: t('update.askNodeBody'),
    confirmLabel: t('update.install'),
    run: async () => {
      const res = await api.post(`/api/nodes/${node.id}/update`)
      if (res?.started) {
        notify(t('update.nodeStarted').replace('{v}', res.to || ''), 'success')
      } else if (res?.updated) {
        notify(t('update.nodeUpdated').replace('{v}', res.to || ''), 'success')
      } else {
        notify(res?.notice || t('update.upToDate'), 'success')
      }
      await load()
    },
  }
}

// Which rows are mid-request, by node id.
//
// A probe crosses the network to another server: it can take seconds, and it
// can time out. Marking the whole page busy meant the operator could not probe
// a second node while the first was still answering, and the row they had just
// clicked looked exactly like the ones they had not.
const pending = ref(new Set())

function hold(id) {
  pending.value = new Set(pending.value).add(id)
}
function release(id) {
  const next = new Set(pending.value)
  next.delete(id)
  pending.value = next
}

async function probe(n) {
  hold(n.id)
  try {
    await api.post(`/api/nodes/${n.id}/probe`)
    // Quiet: the row's own spinner already says something is happening, and a
    // second indicator in the header for the same click reads as two things
    // going on at once.
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    release(n.id)
  }
}

// The box ticks now; the panel catches up.
async function toggle(n, enabled) {
  const was = n.enabled
  if (was === enabled) return
  n.enabled = enabled
  hold(n.id)
  try {
    const updated = await api.patch(`/api/nodes/${n.id}`, {
      name: n.name,
      address: n.address,
      enabled,
    })
    Object.assign(n, updated)
  } catch (e) {
    // Put the box back where it was rather than leaving it showing a state the
    // panel never reached.
    n.enabled = was
    notify(e.message, 'error')
  } finally {
    release(n.id)
    load(true)
  }
}

const ask = ref(null)

function remove(n) {
  ask.value = {
    title: t('node.confirmDeleteTitle'),
    body: t('node.confirmDeleteBody'),
    subject: n.name,
    // Worth saying plainly: this is a change to this panel's list, not to the
    // server at the other end. Its customers keep working.
    consequences: [t('node.consequenceRemote'), t('node.consequenceToken')],
    confirmLabel: t('action.delete'),
    run: async () => {
      try {
        await api.del(`/api/nodes/${n.id}`)
        await load()
      } catch (e) {
        notify(e.message, 'error')
      }
    },
  }
}

async function runConfirmed() {
  const spec = ask.value
  if (!spec) return
  busy.value = true
  try {
    await spec.run()
  } finally {
    busy.value = false
    ask.value = null
  }
}

// What a row or a card asks for.
const rowEvents = {
  onProbe: probe,
  onUpdate: askNodeUpdate,
  onImport: (n) => (importing.value = n),
  onEdit: (n) => (formFor.value = { node: n }),
  onRemove: remove,
  onToggle: toggle,
}
</script>

<template>
  <!-- No page heading. the classic panel opens this page straight on the figures, and
       both controls live in the table's card, where the rows they act on are.
       Add node takes the primary spot on the left; the token button sits at
       the far end the way their Node mTLS does -- both are how a node proves
       itself to this panel, and that is the position they gave that. -->
  <div class="strip card">
    <div class="strip-item">
      <span class="strip-label"><Icon name="server" :size="14" />{{ t('node.total') }}</span>
      <span class="strip-value num">{{ totals.all }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="check" :size="14" />{{ t('node.online') }}</span>
      <span class="strip-value num" style="color: var(--ok)">{{ totals.online }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="alert" :size="14" />{{ t('node.offline') }}</span>
      <span class="strip-value num" :style="totals.offline ? 'color: var(--bad)' : ''">
        {{ totals.offline }}
      </span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="clock" :size="14" />{{ t('node.avgLatency') }}</span>
      <span class="strip-value num ltr">{{ totals.latency === null ? '—' : totals.latency + ' ms' }}</span>
    </div>
  </div>

  <div class="card">
    <div class="card-toolbar spread">
      <button class="btn primary" @click="formFor = {}">
        <Icon name="plus" :size="14" />
        <span v-if="!isMobile">{{ t('node.add') }}</span>
      </button>
      <button class="btn" @click="issuing = true">
        <Icon name="key" :size="14" />
        <span>{{ t('node.issueToken') }}</span>
      </button>
    </div>

    <PageSpin v-if="showSkeleton" />
    <div v-else-if="loading" class="empty"></div>
    <ErrorState v-else-if="loadError" :error="loadError" @retry="load" />
    <NodeCards v-else-if="isMobile" :nodes="nodes" :pending="pending" v-bind="rowEvents" />
    <NodesTable v-else :nodes="nodes" :pending="pending" v-bind="rowEvents" />
  </div>

  <NodeForm v-if="formFor" :node="formFor.node || null" @close="formFor = null" @saved="saved" />
  <TokenDialog v-if="issuing" @close="issuing = false" />
  <NodeImportDialog v-if="importing" :node="importing" @close="importing = null" />

  <ConfirmDialog
    :open="!!ask"
    :title="ask?.title || ''"
    :body="ask?.body || ''"
    :subject="ask?.subject || ''"
    :consequences="ask?.consequences || []"
    :confirm-label="ask?.confirmLabel || ''"
    :busy="busy"
    @confirm="runConfirmed"
    @cancel="ask = null"
  />
</template>
